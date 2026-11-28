//go:build linux

// Package backupop runs the offline pair coordinator as the trusted
// installation identity. Each SQLite command owns just its own live database.
package backupop

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"golang.org/x/sys/unix"
)

const (
	DedicatedWebBinary = "/usr/libexec/ovs-webd"
	readyPrefix        = "OVS-WEB-BACKUP-READY "
	beginMarker        = 1
)

type PairRequest struct {
	ManagerDatabase, WebDatabase, OutputRoot, Version string
	WebUID, WebGID                                    uint32
}

// WebComponent is called by the web daemon's explicit offline command. Its
// current owner opens and holds web.db before the coordinator starts copying.
func WebComponent(ctx context.Context, options sqlite.Options, receive, send *os.File) error {
	if os.Geteuid() == 0 || options.Kind != repository.Web || !repository.ValidID(options.SoftwareVersion) {
		return errors.New("BACKUP_WEB_OWNER_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	input, err := boundedPipe(receive)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := boundedPipe(send)
	if err != nil {
		return err
	}
	defer output.Close()
	deadline, _ := ctx.Deadline()
	if input.SetReadDeadline(deadline) != nil || output.SetWriteDeadline(deadline) != nil {
		return errors.New("BACKUP_COORDINATOR_PIPES_REQUIRED")
	}
	store, err := sqlite.OpenCurrentSchema(ctx, options)
	if store != nil {
		defer store.Close()
	}
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, readyPrefix+options.SoftwareVersion+"\n"); err != nil {
		return err
	}
	var command [1]byte
	if _, err := io.ReadFull(input, command[:]); err != nil || command[0] != beginMarker {
		return errors.New("BACKUP_COORDINATOR_HANDSHAKE_INVALID")
	}
	return store.WriteBackupComponent(ctx, output)
}

func boundedPipe(file *os.File) (*os.File, error) {
	fail := errors.New("BACKUP_COORDINATOR_PIPES_REQUIRED")
	if file == nil {
		return nil, fail
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, fail
	}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, fail
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fail
	}
	return os.NewFile(uintptr(fd), "backup-coordinator-pipe"), nil
}

// CreateOfflinePair keeps manager.db reserved while the separate web UID owns
// its real web.db. It never stops services or opens the web database as root.
func CreateOfflinePair(ctx context.Context, req PairRequest) (sqlite.BackupPairManifest, error) {
	if os.Geteuid() != 0 || req.WebUID == 0 || req.WebGID == 0 || req.WebUID == ^uint32(0) ||
		req.WebGID == ^uint32(0) || !repository.ValidID(req.Version) || len(req.Version) > 128 ||
		!cleanPath(req.ManagerDatabase) || !cleanPath(req.WebDatabase) || !cleanPath(req.OutputRoot) ||
		req.ManagerDatabase == req.WebDatabase {
		return sqlite.BackupPairManifest{}, errors.New("BACKUP_OFFLINE_SCOPE_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	manager, err := sqlite.OpenCurrentSchema(ctx, sqlite.Options{
		Path: req.ManagerDatabase, Kind: repository.Manager, SoftwareVersion: req.Version,
	})
	if manager != nil {
		defer manager.Close()
	}
	if err != nil {
		return sqlite.BackupPairManifest{}, err
	}
	webBinary, err := openPackagedWebBinary()
	if err != nil {
		return sqlite.BackupPairManifest{}, err
	}
	defer webBinary.Close()
	child := exec.CommandContext(ctx, "/proc/self/fd/3", "--backup-component-stdout", "--database="+req.WebDatabase)
	child.Args[0] = DedicatedWebBinary
	child.ExtraFiles = []*os.File{webBinary}
	child.Env = []string{"LANG=C"}
	child.Dir = "/"
	child.Stderr = io.Discard
	child.WaitDelay = time.Second
	child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: req.WebUID, Gid: req.WebGID}}
	input, err := child.StdinPipe()
	if err != nil {
		return sqlite.BackupPairManifest{}, err
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		return sqlite.BackupPairManifest{}, err
	}
	defer output.Close()
	if err := child.Start(); err != nil {
		return sqlite.BackupPairManifest{}, errors.New("BACKUP_WEB_OWNER_UNAVAILABLE")
	}
	waited := false
	checkExit := func() error {
		waited = true
		if err := child.Wait(); err != nil {
			return errors.New("BACKUP_WEB_COMPONENT_INCOMPLETE")
		}
		return nil
	}
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	web := bufio.NewReaderSize(output, 256)
	ready, err := web.ReadSlice('\n')
	if err != nil || string(ready) != readyPrefix+req.Version+"\n" {
		return sqlite.BackupPairManifest{}, errors.New("BACKUP_WEB_GUARD_OR_VERSION_UNPROVEN")
	}
	if _, err := input.Write([]byte{beginMarker}); err != nil {
		return sqlite.BackupPairManifest{}, errors.New("BACKUP_COORDINATOR_HANDSHAKE_INVALID")
	}
	if err := input.Close(); err != nil {
		return sqlite.BackupPairManifest{}, errors.New("BACKUP_COORDINATOR_HANDSHAKE_INVALID")
	}
	managerRead, managerWrite := io.Pipe()
	defer managerRead.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = managerWrite.CloseWithError(manager.WriteBackupComponent(ctx, managerWrite))
	}()
	defer func() {
		_ = managerRead.Close()
		<-done
	}()
	return sqlite.PublishBackupPairStreams(ctx, req.OutputRoot, managerRead, &completeReader{in: web, complete: checkExit})
}

type completeReader struct {
	in       io.Reader
	complete func() error
}

func (r *completeReader) Read(dst []byte) (int, error) {
	n, err := r.in.Read(dst)
	if err == io.EOF && r.complete != nil {
		finish := r.complete
		r.complete = nil
		if failure := finish(); failure != nil {
			return n, failure
		}
	}
	return n, err
}

func cleanPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func openPackagedWebBinary() (*os.File, error) {
	fail := errors.New("BACKUP_WEB_EXECUTABLE_UNPROVEN")
	for directory := filepath.Dir(DedicatedWebBinary); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || !rootImmutable(info) {
			return nil, fail
		}
		if directory == "/" {
			break
		}
	}
	file, err := os.OpenFile(DedicatedWebBinary, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fail
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !rootImmutable(info) ||
		info.Mode().Perm()&0111 != 0111 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		_ = file.Close()
		return nil, fail
	}
	return file, nil
}

func rootImmutable(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == 0 && info.Mode().Perm()&0022 == 0
}
