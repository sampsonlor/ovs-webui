//go:build linux

package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyRotationCrashBoundariesPermissionsAndMissingRestoreKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	first, err := InitializeKeys(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeKeys(dir, nil); err == nil {
		t.Fatal("overwrote key")
	}
	next, err := PrepareKey(dir)
	if err != nil || next.Active != 2 {
		t.Fatal(err)
	}
	blob, err := next.Seal("db", TLS, "identity", "key", NewValue([]byte("synthetic-key")))
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := LoadKeys(dir)
	if err != nil || interrupted.Active != 1 {
		t.Fatal("active switched before commit", err)
	}
	if _, err := interrupted.Open("db", TLS, "identity", "key", blob); err != nil {
		t.Fatal("prepared key lost after restart", err)
	}
	again, err := PrepareKey(dir)
	if err != nil || !bytes.Equal(again.Keys[2], next.Keys[2]) {
		t.Fatal("interrupted rotation overwrote key")
	}
	if err = ActivateKeys(dir, 2); err != nil {
		t.Fatal(err)
	}
	current, err := LoadKeys(dir)
	if err != nil || current.Active != 2 || !bytes.Equal(current.Keys[1], first.Keys[1]) {
		t.Fatal("old key lost", err)
	}
	if err = os.Chmod(keyPath(dir, 2), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadKeys(dir); err == nil {
		t.Fatal("group-readable key accepted")
	}
	_ = os.Chmod(keyPath(dir, 2), 0600)
	link := filepath.Join(t.TempDir(), "key-link")
	if err = os.Link(keyPath(dir, 2), link); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadKeys(dir); err == nil {
		t.Fatal("hardlinked key accepted")
	}
	_ = os.Remove(link)
	if err = os.Rename(keyPath(dir, 1), filepath.Join(dir, "missing-key")); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadKeys(dir); err == nil {
		t.Fatal("partial backup key set accepted")
	}
	symlink := filepath.Join(t.TempDir(), "keys-link")
	if err = os.Symlink(dir, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadKeys(symlink); err == nil {
		t.Fatal("directory symlink accepted")
	}
}
