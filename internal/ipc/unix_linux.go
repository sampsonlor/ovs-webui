//go:build linux

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type SocketOptions struct {
	Path                        string
	OwnerUID, GroupGID, PeerUID uint32
}

type Listener struct {
	inner    *net.UnixListener
	lock     *os.File
	path     string
	identity os.FileInfo
	peerUID  uint32
	slots    chan struct{}
	once     sync.Once
}

// The daemons always pass OwnerUID=0. Non-root owners are useful only for
// package tests; there is no public daemon flag to relax root ownership.
func ListenUnix(opts SocketOptions) (*Listener, error) {
	if !filepath.IsAbs(opts.Path) || filepath.Clean(opts.Path) != opts.Path || len(opts.Path) > 103 {
		return nil, errors.New("invalid socket path")
	}
	if err := secureDirectory(filepath.Dir(opts.Path), opts.OwnerUID); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(opts.Path+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("socket lock unavailable")
	}
	lock := os.NewFile(uintptr(fd), "ipc-lock")
	ok := false
	defer func() {
		if !ok {
			_ = lock.Close()
		}
	}()
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != opts.OwnerUID {
		return nil, errors.New("unsafe socket lock")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("manager already running")
	}
	if old, err := os.Lstat(opts.Path); err == nil {
		stat := old.Sys().(*syscall.Stat_t)
		if old.Mode()&os.ModeSocket == 0 || stat.Uid != opts.OwnerUID || stat.Gid != opts.GroupGID || old.Mode().Perm() != 0660 {
			return nil, errors.New("unsafe existing socket")
		}
		probe, dialErr := net.DialTimeout("unix", opts.Path, 100*time.Millisecond)
		if dialErr == nil {
			_ = probe.Close()
			return nil, errors.New("socket already active")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, errors.New("existing socket state unknown")
		}
		if err := os.Remove(opts.Path); err != nil {
			return nil, errors.New("stale socket cleanup failed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("socket path unavailable")
	}
	inner, err := net.ListenUnix("unix", &net.UnixAddr{Name: opts.Path, Net: "unix"})
	if err != nil {
		return nil, errors.New("socket bind failed")
	}
	inner.SetUnlinkOnClose(false)
	cleanup := func() { _ = inner.Close(); _ = os.Remove(opts.Path) }
	if err := os.Chown(opts.Path, int(opts.OwnerUID), int(opts.GroupGID)); err != nil {
		cleanup()
		return nil, errors.New("socket ownership failed")
	}
	if err := os.Chmod(opts.Path, 0660); err != nil {
		cleanup()
		return nil, errors.New("socket mode failed")
	}
	identity, err := os.Lstat(opts.Path)
	if err != nil {
		cleanup()
		return nil, err
	}
	ok = true
	return &Listener{inner: inner, lock: lock, path: opts.Path, identity: identity, peerUID: opts.PeerUID, slots: make(chan struct{}, MaxConnections)}, nil
}

func secureDirectory(path string, owner uint32) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("socket directory must exist without symlinks")
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != 0 && stat.Uid != owner {
			return errors.New("untrusted socket ancestor owner")
		}
		stickyRoot := current != path && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0022 != 0 && !stickyRoot {
			return errors.New("socket directory is writable by another principal")
		}
		if current == path && stat.Uid != owner {
			return errors.New("incorrect socket directory owner")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func (l *Listener) Accept() (net.Conn, error) {
	for {
		conn, err := l.inner.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, err := PeerUID(conn)
		if err != nil || uid != l.peerUID {
			_ = conn.Close()
			continue
		}
		select {
		case l.slots <- struct{}{}:
			return &peerConn{Conn: conn, release: func() { <-l.slots }}, nil
		default:
			_ = conn.Close()
		}
	}
}
func (l *Listener) Addr() net.Addr { return l.inner.Addr() }
func (l *Listener) Close() error   { return l.inner.Close() }

// Release is called by the process owner after HTTP shutdown/connection close.
// http.Server closes its listener before draining requests, so Close must not
// release the singleton lock early and admit a second manager during shutdown.
func (l *Listener) Release() {
	l.once.Do(func() {
		_ = l.inner.Close()
		if current, err := os.Lstat(l.path); err == nil && os.SameFile(l.identity, current) {
			_ = os.Remove(l.path)
		}
		// Keep the lock inode: unlinking it would allow two competing locks.
		_ = l.lock.Close()
	})
}

type peerConn struct {
	net.Conn
	session connectionState
	once    sync.Once
	release func()
}

func (c *peerConn) state() *connectionState { return &c.session }
func (c *peerConn) Close() error            { err := c.Conn.Close(); c.once.Do(c.release); return err }

func PeerUID(conn net.Conn) (uint32, error) {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("Unix connection required")
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var socketErr error
	if err = raw.Control(func(fd uintptr) {
		cred, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if socketErr != nil || cred == nil {
		return 0, errors.New("peer credentials unavailable")
	}
	return cred.Uid, nil
}

func DialPeer(ctx context.Context, path string, expectedUID uint32) (net.Conn, error) {
	dialer := net.Dialer{Timeout: HeaderTimeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, errors.New("IPC_UNAVAILABLE")
	}
	uid, err := PeerUID(conn)
	if err != nil || uid != expectedUID {
		_ = conn.Close()
		return nil, errors.New("IPC_SERVER_PEER_DENIED")
	}
	return conn, nil
}
