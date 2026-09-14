//go:build linux

package authn

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Open the owner-only directory without following links, then use openat and
// O_NOFOLLOW. Explicit initialization never overwrites or regenerates a lost key.
func KeyFile(path string, initialize bool) ([]byte, error) {
	fail := errors.New("AUTH_KEY_UNAVAILABLE")
	if !filepath.IsAbs(path) {
		return nil, fail
	}
	dir, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fail
	}
	defer unix.Close(dir)
	var ds unix.Stat_t
	if unix.Fstat(dir, &ds) != nil || ds.Uid != uint32(os.Geteuid()) || ds.Mode&0077 != 0 {
		return nil, fail
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if initialize {
		flags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	}
	fd, err := unix.Openat(dir, filepath.Base(path), flags, 0600)
	if err != nil {
		return nil, fail
	}
	f := os.NewFile(uintptr(fd), "auth-key")
	defer f.Close()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0077 != 0 || stat.Nlink != 1 {
		return nil, fail
	}
	key := make([]byte, 32)
	if initialize {
		if _, err = rand.Read(key); err != nil {
			return nil, fail
		}
		if n, e := f.Write(key); e != nil || n != len(key) {
			return nil, fail
		}
		if f.Sync() != nil || unix.Fsync(dir) != nil {
			return nil, fail
		}
	} else {
		if stat.Size != 32 {
			return nil, fail
		}
		if n, e := f.Read(key); e != nil || n != 32 {
			return nil, fail
		}
	}
	return key, nil
}
