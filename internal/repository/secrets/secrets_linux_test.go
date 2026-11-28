//go:build linux

package secrets

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"os"
	"path/filepath"
	"testing"
)

func TestPartitionOwnershipEncryptedBackupAndVersionedRewrap(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	o := sqlite.Options{Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "secret-test"}
	if err := sqlite.Initialize(ctx, o); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ring, _ := secret.NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{4}, 32)})
	s, err := New(ctx, db, secret.TLS, ring)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, db, secret.Privileged, ring); err == nil {
		t.Fatal("web DB exposes privileged partition")
	}
	raw := "synthetic-TLS-private-35"
	if err = db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.Put(ctx, tx, "test-identity", "tls-identity", secret.NewValue([]byte(raw)))
	}); err != nil {
		t.Fatal(err)
	}
	other, _ := New(ctx, db, secret.Session, ring)
	if _, err = other.Get(ctx, "test-identity", "tls-identity"); err == nil {
		t.Fatal("cross-consumer read")
	}
	if _, err = s.Get(ctx, "test-identity", "other-purpose"); err == nil {
		t.Fatal("wrong purpose read")
	}
	backup, err := db.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlite.VerifyBackup(ctx, backup, repository.Web); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(backup, "database.sqlite"))
	if err != nil || bytes.Contains(data, []byte(raw)) {
		t.Fatal("backup exposed secret", err)
	}
	next, _ := secret.NewRing(2, map[int][]byte{1: ring.Keys[1], 2: bytes.Repeat([]byte{5}, 32)})
	if err = s.Rewrap(ctx, next); err != nil {
		t.Fatal(err)
	}
	s.Ring = next
	value, err := s.Get(ctx, "test-identity", "tls-identity")
	if err != nil || string(value.Bytes()) != raw {
		t.Fatal(err)
	}
	s.Ring = ring
	if _, err = s.Get(ctx, "test-identity", "tls-identity"); err == nil {
		t.Fatal("missing current key accepted")
	}
	if !db.Status().Writable {
		t.Fatal("missing key damaged storage")
	}
	// Install the verified snapshot in a distinct private directory. Identity/AAD
	// remains the snapshot identity; ciphertext alone cannot recover its secret.
	restoredDir := t.TempDir()
	_ = os.Chmod(restoredDir, 0700)
	o.Path = filepath.Join(restoredDir, "web.db")
	if err = os.WriteFile(o.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	wrong, _ := secret.NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{6}, 32)})
	rs, _ := New(ctx, restored, secret.TLS, wrong)
	if _, err = rs.Get(ctx, "test-identity", "tls-identity"); err == nil {
		t.Fatal("backup decrypted with wrong key")
	}
	rs.Ring = ring
	if v, err := rs.Get(ctx, "test-identity", "tls-identity"); err != nil || string(v.Bytes()) != raw {
		t.Fatal("valid restore failed", err)
	}
}

func TestRestoreVerifiesRetainedCiphertextAndAdvertisedVersion(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	options := sqlite.Options{
		Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "restore-test",
	}
	if err := sqlite.Initialize(ctx, options); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := VerifyRetained(ctx, db); err != nil {
		t.Fatal(err)
	}
	if required, err := HasRetainedTLSState(ctx, db); err != nil || required {
		t.Fatalf("empty TLS state: required=%t error=%v", required, err)
	}
	oldKey, nextKey := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	old, _ := secret.NewRing(1, map[int][]byte{1: oldKey})
	both, _ := secret.NewRing(2, map[int][]byte{1: oldKey, 2: nextKey})
	tls, err := New(ctx, db, secret.TLS, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := tls.Put(ctx, tx, "synthetic-tls-id", "tls-identity", secret.NewValue([]byte("discardable-fixture"))); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO tls_local_state VALUES(1,X'00')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if required, err := HasRetainedTLSState(ctx, db); err != nil || !required {
		t.Fatalf("stored TLS state: required=%t error=%v", required, err)
	}
	if err := VerifyRetained(ctx, db); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("accepted missing TLS owner: %v", err)
	}
	tls.Ring = both
	if err := VerifyRetained(ctx, db, tls); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE secret_records SET key_version=2")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRetained(ctx, db, tls); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("accepted envelope with a different advertised version: %v", err)
	}
}
