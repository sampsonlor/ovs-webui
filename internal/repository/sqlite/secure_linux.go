//go:build linux

package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	driver "modernc.org/sqlite"
)

func init() {
	if _, err := driver.OFDLocking(true); err != nil {
		panic("SQLite OFD locking unavailable")
	}
}

type fileGuard struct {
	lock     *os.File
	path     string
	owner    uint32
	identity os.FileInfo
}

func secureDirectory(path string, owner uint32) error {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("STORAGE_UNSAFE_DIRECTORY")
		}
		uid := info.Sys().(*syscall.Stat_t).Uid
		if uid != 0 && uid != owner {
			return errors.New("STORAGE_UNSAFE_DIRECTORY")
		}
		sticky := p != path && uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0022 != 0 && !sticky {
			return errors.New("STORAGE_UNSAFE_DIRECTORY")
		}
		if p == path && (uid != owner || info.Mode().Perm() != 0700) {
			return errors.New("STORAGE_UNSAFE_DIRECTORY")
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
func acquireGuard(path string) (*fileGuard, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("STORAGE_UNSAFE_PATH")
	}
	owner := uint32(os.Geteuid())
	if err := secureDirectory(filepath.Dir(path), owner); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("STORAGE_LOCK_UNAVAILABLE")
	}
	f := os.NewFile(uintptr(fd), "storage-lock")
	info, err := f.Stat()
	if err != nil || !safeFile(info, owner) || syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		_ = f.Close()
		return nil, errors.New("STORAGE_LOCK_UNAVAILABLE")
	}
	return &fileGuard{lock: f, path: path, owner: owner}, nil
}
func safeFile(info os.FileInfo, owner uint32) bool {
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && info.Sys().(*syscall.Stat_t).Uid == owner && info.Sys().(*syscall.Stat_t).Nlink == 1
}
func (g *fileGuard) check() error {
	if err := secureDirectory(filepath.Dir(g.path), g.owner); err != nil {
		return err
	}
	info, err := os.Lstat(g.path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("STORAGE_MISSING")
	}
	if err != nil || !safeFile(info, g.owner) {
		return errors.New("STORAGE_UNSAFE_FILE")
	}
	if g.identity != nil && !os.SameFile(g.identity, info) {
		return errors.New("STORAGE_REPLACED")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		side, err := os.Lstat(g.path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !safeFile(side, g.owner) {
			return errors.New("STORAGE_UNSAFE_SIDECAR")
		}
	}
	g.identity = info
	return nil
}
func (g *fileGuard) close() {
	if g != nil && g.lock != nil {
		_ = g.lock.Close()
	}
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func checkPrivateFile(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("STORAGE_UNSAFE_PATH")
	}
	if err := secureDirectory(filepath.Dir(path), uint32(os.Geteuid())); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !safeFile(info, uint32(os.Geteuid())) {
		return errors.New("STORAGE_UNSAFE_FILE")
	}
	return nil
}
