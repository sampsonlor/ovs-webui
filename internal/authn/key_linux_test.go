//go:build linux

package authn

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyFileRequiresExplicitPrivateSingleLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auth.key")
	if _, err := KeyFile(path, false); err == nil {
		t.Fatal("missing key loaded without initialization")
	}
	key, err := KeyFile(path, true)
	if err != nil || len(key) != 32 {
		t.Fatalf("explicit initialization failed: %v", err)
	}
	if _, err = KeyFile(path, true); err == nil {
		t.Fatal("existing key was overwritten")
	}
	loaded, err := KeyFile(path, false)
	if err != nil || !bytes.Equal(loaded, key) {
		t.Fatal("key changed during reload", err)
	}
	if err = os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = KeyFile(path, false); err == nil {
		t.Fatal("group-readable key accepted")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.key")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = KeyFile(link, false); err == nil {
		t.Fatal("symlink key accepted")
	}
	if err = os.Link(path, filepath.Join(dir, "hard.key")); err != nil {
		t.Fatal(err)
	}
	if _, err = KeyFile(path, false); err == nil {
		t.Fatal("multiply-linked key accepted")
	}
	if err = os.Chmod(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if _, err = KeyFile(filepath.Join(dir, "new.key"), true); err == nil {
		t.Fatal("key initialized in shared directory")
	}
}
