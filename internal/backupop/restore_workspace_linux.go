package backupop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"golang.org/x/sys/unix"
)

const freshRestorePrefix = "restore-ready-"
const stagingRestorePrefix = "restore-stage-"

type restoreOwner struct{ uid, gid uint32 }

type restoreWorkspace struct {
	root      *os.File
	rootPath  string
	pending   string
	final     string
	stage     sqlite.RestoreStageManifest
	web       restoreOwner
	guards    []*os.File
	published bool
}

func openRestoreWorkspace(ctx context.Context, stageRoot, stageID, destination string, web restoreOwner) (*restoreWorkspace, error) {
	work, err := openRestoreWorkspaceRoot(ctx, stageRoot, stageID, destination, web)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			work.close()
		}
	}()
	if _, err := os.Lstat(filepath.Join(destination, work.final)); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return nil, repository.ErrConflict
		}
		return nil, repository.ErrUnavailable
	}
	stage, err := sqlite.VerifyStagedRestorePair(ctx, stageRoot, stageID)
	if err != nil {
		return nil, err
	}
	work.stage = stage
	work.pending = ".preparing-" + repository.NewID()
	if err := unix.Mkdirat(int(work.root.Fd()), work.pending, 0700); err != nil {
		work.pending = ""
		return nil, repository.ErrUnavailable
	}
	if !work.rootUnchanged() || os.Chown(work.path(), 0, int(web.gid)) != nil || os.Chmod(work.path(), 0710) != nil {
		return nil, repository.ErrUnavailable
	}
	for _, snapshot := range []sqlite.BackupPairSnapshot{stage.BackupPair.Manager, stage.BackupPair.Web} {
		kind := snapshot.BackupManifest.Kind
		from := filepath.Join(stageRoot, stagingRestorePrefix+stageID, string(kind), "database.sqlite")
		into := work.database(kind)
		owner := work.owner(kind)
		if err := os.Mkdir(filepath.Dir(into), 0700); err != nil {
			return nil, repository.ErrUnavailable
		}
		if err := copyRestoreImage(ctx, from, into, snapshot); err != nil {
			return nil, err
		}
		guard, err := os.OpenFile(into+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, repository.ErrUnavailable
		}
		if guard.Close() != nil || os.Chown(into+".lock", int(owner.uid), int(owner.gid)) != nil {
			return nil, repository.ErrUnavailable
		}
		if os.Chown(into, int(owner.uid), int(owner.gid)) != nil ||
			os.Chown(filepath.Dir(into), int(owner.uid), int(owner.gid)) != nil {
			return nil, repository.ErrUnavailable
		}
	}
	keep = true
	return work, nil
}

func openRestoreWorkspaceRoot(ctx context.Context, stageRoot, stageID, destination string, web restoreOwner) (*restoreWorkspace, error) {
	if os.Geteuid() != 0 || !apitypes.ManagementID(stageID) || web.uid == 0 || web.gid == 0 ||
		web.uid == ^uint32(0) || web.gid == ^uint32(0) || !cleanPath(stageRoot) || !cleanPath(destination) ||
		overlapRestorePaths(stageRoot, destination) || overlapRestorePaths("/var/lib/ovs-webui", destination) ||
		overlapRestorePaths("/etc/ovs-webui", destination) || overlapRestorePaths("/run/ovs-webui", destination) {
		return nil, repository.ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, repository.ErrCanceled
	}
	if !restoreTraversableRoot(destination, web.gid) {
		return nil, repository.ErrUnavailable
	}
	fd, err := unix.Open(destination, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	root := os.NewFile(uintptr(fd), "restore-destination")
	work := &restoreWorkspace{root: root, rootPath: destination, final: freshRestorePrefix + stageID, web: web}
	keep := false
	defer func() {
		if !keep {
			work.close()
		}
	}()
	if !work.rootUnchanged() {
		return nil, repository.ErrUnavailable
	}
	rootInfo, err := root.Stat()
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	for _, live := range []string{"/var/lib/ovs-webui", "/var/lib/ovs-webui/manager", "/var/lib/ovs-webui/web", "/etc/ovs-webui", "/run/ovs-webui"} {
		info, err := os.Stat(live)
		if err == nil && os.SameFile(info, rootInfo) {
			return nil, repository.ErrInvalid
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, repository.ErrUnavailable
		}
	}
	keep = true
	return work, nil
}

func (w *restoreWorkspace) lockSlots() (func(), error) {
	if len(w.guards) != 0 || !w.rootUnchanged() {
		return nil, repository.ErrUnavailable
	}
	release := func() {
		for _, guard := range w.guards {
			_ = guard.Close()
		}
		w.guards = nil
	}
	for _, kind := range []repository.Kind{repository.Manager, repository.Web} {
		path, owner := w.database(kind), w.owner(kind)
		guard, err := openRestoreFile(path+".lock", owner.uid, true)
		if err != nil {
			release()
			return nil, err
		}
		w.guards = append(w.guards, guard)
	}
	if !w.slotsUnchanged() {
		release()
		return nil, repository.ErrUnavailable
	}
	return release, nil
}

func (w *restoreWorkspace) slotsUnchanged() bool {
	if len(w.guards) != 2 {
		return false
	}
	for i, kind := range []repository.Kind{repository.Manager, repository.Web} {
		path := w.database(kind)
		if !exactRestoreSlot(path, w.owner(kind)) {
			return false
		}
		locked, err := w.guards[i].Stat()
		current, statErr := os.Lstat(path + ".lock")
		if err != nil || statErr != nil || !os.SameFile(locked, current) {
			return false
		}
	}
	return true
}

func exactRestoreSlot(path string, owner restoreOwner) bool {
	var info unix.Stat_t
	parent := filepath.Dir(path)
	if unix.Lstat(parent, &info) != nil || info.Mode != unix.S_IFDIR|0700 || info.Uid != owner.uid || info.Gid != owner.gid {
		return false
	}
	for _, name := range []string{path, path + ".lock"} {
		if unix.Lstat(name, &info) != nil || info.Mode != unix.S_IFREG|0600 || info.Nlink != 1 ||
			info.Uid != owner.uid || info.Gid != owner.gid {
			return false
		}
	}
	dir, err := os.Open(parent)
	if err != nil {
		return false
	}
	defer dir.Close()
	entries, err := dir.Readdirnames(3)
	return (err == nil || errors.Is(err, io.EOF)) && len(entries) == 2
}

func (w *restoreWorkspace) path() string {
	if w.published {
		return filepath.Join(w.rootPath, w.final)
	}
	return filepath.Join(w.rootPath, w.pending)
}

func (w *restoreWorkspace) database(kind repository.Kind) string {
	return filepath.Join(w.path(), string(kind), string(kind)+".db")
}

func (w *restoreWorkspace) owner(kind repository.Kind) restoreOwner {
	if kind == repository.Web {
		return w.web
	}
	return restoreOwner{}
}

func (w *restoreWorkspace) rootUnchanged() bool {
	if !restoreTraversableRoot(w.rootPath, w.web.gid) {
		return false
	}
	opened, err := w.root.Stat()
	if err != nil {
		return false
	}
	current, err := os.Lstat(w.rootPath)
	return err == nil && os.SameFile(opened, current)
}

func (w *restoreWorkspace) close() {
	defer w.root.Close()
	if w.pending != "" && !w.published && w.rootUnchanged() {
		_ = os.RemoveAll(w.path())
	}
}

// publish requires the coordinator to hold both prepared SQLite process
// guards, confirm the owner's committed postconditions and write its manifest.
func (w *restoreWorkspace) publish(ctx context.Context) (string, error) {
	if ctx.Err() != nil {
		return "", repository.ErrCanceled
	}
	if !w.rootUnchanged() || !w.slotsUnchanged() || syncRestoreDirectory(w.path()) != nil {
		return "", repository.ErrUnavailable
	}
	fd := int(w.root.Fd())
	if err := unix.Renameat2(fd, w.pending, fd, w.final, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return "", repository.ErrConflict
		}
		return "", repository.ErrUnavailable
	}
	w.published = true
	path := filepath.Join(w.rootPath, w.final)
	if w.root.Sync() != nil || !w.rootUnchanged() {
		return path, repository.ErrCommitUnknown
	}
	return path, nil
}

func overlapRestorePaths(first, second string) bool {
	return first == second || strings.HasPrefix(first, strings.TrimSuffix(second, "/")+"/") ||
		strings.HasPrefix(second, strings.TrimSuffix(first, "/")+"/")
}

func restoreTraversableRoot(path string, gid uint32) bool {
	for p := path; ; p = filepath.Dir(p) {
		var info unix.Stat_t
		if unix.Lstat(p, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Uid != 0 ||
			!restoreOwnerCanTraverse(info.Mode, info.Gid, gid) {
			return false
		}
		// A sticky root-owned ancestor such as /tmp cannot rename a
		// root-owned child on behalf of an unprivileged user.
		if info.Mode&0022 != 0 && (p == path || info.Mode&unix.S_ISVTX == 0) {
			return false
		}
		if p == "/" {
			return true
		}
	}
}

func restoreOwnerCanTraverse(mode, ownerGID, webGID uint32) bool {
	if ownerGID == webGID {
		return mode&0010 != 0
	}
	return mode&0001 != 0
}

func copyRestoreImage(ctx context.Context, source, destination string, expected sqlite.BackupPairSnapshot) error {
	input, err := openRestoreFile(source, 0, false)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || info.Size() != expected.DatabaseBytes || info.Size() <= 0 || info.Size() > sqlite.MaxBackupPairDatabaseBytes {
		return repository.ErrUnavailable
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return repository.ErrUnavailable
	}
	hash := sha256.New()
	_, err = io.CopyN(io.MultiWriter(output, hash), restoreReader{ctx, input}, info.Size())
	if err == nil && hex.EncodeToString(hash.Sum(nil)) == expected.BackupManifest.SHA256 {
		err = output.Sync()
	} else if err == nil {
		err = repository.ErrUnavailable
	}
	closeErr := output.Close()
	if ctx.Err() != nil {
		return repository.ErrCanceled
	}
	if err != nil || closeErr != nil {
		return repository.ErrUnavailable
	}
	return nil
}

type restoreReader struct {
	ctx    context.Context
	source io.Reader
}

func (r restoreReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func openRestoreFile(path string, uid uint32, guard bool) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	var info unix.Stat_t
	if unix.Fstat(fd, &info) != nil || info.Uid != uid || info.Nlink != 1 || info.Mode != unix.S_IFREG|0600 ||
		guard && (info.Size != 0 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil) {
		_ = unix.Close(fd)
		return nil, repository.ErrUnavailable
	}
	return os.NewFile(uintptr(fd), path), nil
}

func syncRestoreDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
