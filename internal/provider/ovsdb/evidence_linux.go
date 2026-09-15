//go:build linux

package ovsdb

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func peer(conn net.Conn, uid uint32) (string, int, error) {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return "", 0, errors.New("OVSDB_UNIX_REQUIRED")
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return "", 0, err
	}
	var cred *unix.Ucred
	var e error
	if err = raw.Control(func(fd uintptr) { cred, e = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || e != nil || cred == nil || cred.Uid != uid {
		return "", 0, errors.New("OVSDB_PEER_DENIED")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cred.Pid))
	if err != nil {
		return "", 0, errors.New("OVSDB_PEER_UNKNOWN")
	}
	tail := strings.LastIndex(string(data), ")")
	if tail < 0 {
		return "", 0, errors.New("OVSDB_PEER_UNKNOWN")
	}
	fields := strings.Fields(string(data[tail+1:]))
	if len(fields) < 20 {
		return "", 0, errors.New("OVSDB_PEER_UNKNOWN")
	}
	return fmt.Sprintf("%d:%s", cred.Pid, fields[19]), int(cred.Pid), nil
}
func witness(path string, pid int, prior inventory.FileWitness) inventory.FileWitness {
	w := inventory.FileWitness{}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return w
	}
	f := os.NewFile(uintptr(fd), "ovsdb-witness")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size < 0 {
		return w
	}
	w.Available = true
	w.Device = uint64(st.Dev)
	w.Inode = st.Ino
	w.Size = st.Size
	w.Offset = max(0, st.Size-4096)
	w.Length = int(st.Size - w.Offset)
	digest := func(offset int64, n int) (string, bool) {
		if offset < 0 || n < 0 || n > 4096 {
			return "", false
		}
		b := make([]byte, n)
		read, err := f.ReadAt(b, offset)
		if read != n || (err != nil && err != io.EOF) {
			return "", false
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]), true
	}
	var ok bool
	w.Digest, ok = digest(w.Offset, w.Length)
	if !ok {
		return inventory.FileWitness{}
	}
	if prior.Available && prior.Device == w.Device && prior.Inode == w.Inode && w.Size >= prior.Offset+int64(prior.Length) {
		v, ok := digest(prior.Offset, prior.Length)
		w.PriorMatches = ok && v == prior.Digest
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err == nil && len(entries) <= 1024 {
		for _, e := range entries {
			var target unix.Stat_t
			if unix.Stat(fmt.Sprintf("/proc/%d/fd/%s", pid, e.Name()), &target) == nil && uint64(target.Dev) == w.Device && target.Ino == w.Inode {
				w.ServerHasFile = true
				break
			}
		}
	}
	// Hardened mgrd intentionally has no CAP_SYS_PTRACE. When /proc/fd is
	// unavailable, verify the explicit canonical DB path in the root-owned
	// endpoint process argv; retain file/journal/row evidence independently.
	// Never retain or publish argv (it may contain sensitive paths/options).
	if !w.ServerHasFile {
		argv, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err == nil && len(argv) <= 65536 {
			canonical, err := filepath.EvalSymlinks(path)
			if err == nil {
				for _, arg := range strings.Split(string(argv), "\x00") {
					if !filepath.IsAbs(arg) {
						continue
					}
					candidate, err := filepath.EvalSymlinks(arg)
					if err == nil && candidate == canonical {
						w.ServerHasFile = true
						break
					}
				}
			}
		}
	}
	return w
}
func evidence(o Options, d discovered, rows inventory.Rows, identity string, pid int, prior inventory.Evidence, continuous bool) inventory.Evidence {
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	e := inventory.Evidence{Endpoint: filepath.Clean(o.Socket), Database: "Open_vSwitch", Schema: d.public.Digest, Peer: identity, Boot: strings.TrimSpace(string(boot)), ObservedAt: time.Now().UTC(), Continuous: continuous, Anchors: []string{}}
	for id := range rows["Open_vSwitch"] {
		e.Root = id
	}
	for table, objects := range rows {
		if table == "Open_vSwitch" {
			continue
		}
		for id := range objects {
			e.Anchors = append(e.Anchors, inventory.Key(table, id))
		}
	}
	sort.Strings(e.Anchors)
	e.File = witness(o.DatabaseFile, pid, prior.File)
	return e
}
func validateOptions(o Options) error {
	for _, p := range []string{o.Socket, o.DatabaseFile} {
		if !filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\r\n") {
			return errors.New("OVSDB_PATH_INVALID")
		}
	}
	if len(o.Socket) > 100 || o.PeerUID == uint32(1<<32-1) {
		return errors.New("OVSDB_ENDPOINT_INVALID")
	}
	return nil
}
