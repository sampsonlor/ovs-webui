//go:build linux

package secret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/sampsonlor/ovs-webui/internal/authn"
)

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !filepath.IsAbs(path) {
		return ErrUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return ErrUnavailable
	}
	return nil
}
func keyPath(dir string, version int) string {
	return filepath.Join(dir, fmt.Sprintf("key-%02d", version))
}
func LoadKeys(dir string) (Ring, error) {
	if privateDirectory(dir) != nil {
		return Ring{}, ErrUnavailable
	}
	b, err := ReadPrivateFile(filepath.Join(dir, "active"), 16)
	if err != nil {
		return Ring{}, err
	}
	active, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || active < 1 || active > 16 {
		return Ring{}, ErrUnavailable
	}
	keys := map[int][]byte{}
	// A prepared future key remains decryptable after a crash between the DB
	// rewrap and the atomic active-version switch. It is not used for new writes.
	for i := 1; i <= 16; i++ {
		b, err := authn.KeyFile(keyPath(dir, i), false)
		if err != nil {
			if _, e := os.Lstat(keyPath(dir, i)); errors.Is(e, os.ErrNotExist) && i > active {
				continue
			}
			return Ring{}, ErrUnavailable
		}
		keys[i] = b
	}
	return NewRing(active, keys)
}
func InitializeKeys(dir string, legacy []byte) (Ring, error) {
	if !filepath.IsAbs(dir) {
		return Ring{}, ErrUnavailable
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return Ring{}, ErrUnavailable
	}
	if privateDirectory(dir) != nil {
		return Ring{}, ErrUnavailable
	}
	if _, err := os.Lstat(filepath.Join(dir, "active")); !errors.Is(err, os.ErrNotExist) {
		return Ring{}, ErrUnavailable
	}
	path := keyPath(dir, 1)
	if len(legacy) != 0 {
		if len(legacy) != 32 {
			return Ring{}, ErrUnavailable
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return Ring{}, ErrUnavailable
		}
		_, err = f.Write(legacy)
		if err == nil {
			err = f.Sync()
		}
		f.Close()
		if err != nil {
			return Ring{}, ErrUnavailable
		}
	} else if _, err := authn.KeyFile(path, true); err != nil {
		return Ring{}, err
	}
	if err := ActivateKeys(dir, 1); err != nil {
		return Ring{}, err
	}
	return LoadKeys(dir)
}
func PrepareKey(dir string) (Ring, error) {
	r, err := LoadKeys(dir)
	if err != nil || r.Active >= 16 {
		return Ring{}, ErrUnavailable
	}
	version := r.Active + 1
	key, err := authn.KeyFile(keyPath(dir, version), false)
	if err != nil {
		if _, e := os.Lstat(keyPath(dir, version)); !errors.Is(e, os.ErrNotExist) {
			return Ring{}, ErrUnavailable
		}
		key, err = authn.KeyFile(keyPath(dir, version), true)
	}
	if err != nil {
		return Ring{}, err
	}
	r.Keys[version] = key
	r.Active = version
	return r, nil
}
func ActivateKeys(dir string, version int) error {
	if _, err := authn.KeyFile(keyPath(dir, version), false); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "active-")
	if err != nil {
		return ErrUnavailable
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(strconv.Itoa(version) + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil || os.Rename(name, filepath.Join(dir, "active")) != nil {
		return ErrUnavailable
	}
	d, err := os.Open(dir)
	if err != nil {
		return ErrUnavailable
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}
