//go:build linux

package sessions

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

func sessionFixture(t *testing.T) (*Repository, sqlite.Options) {
	t.Helper()
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	o := sqlite.Options{Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "session-test"}
	if err := sqlite.Initialize(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	return sessionOpen(t, o), o
}
func sessionOpen(t *testing.T, o sqlite.Options) *Repository {
	t.Helper()
	store, err := sqlite.Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	r, err := New(context.Background(), store, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestEncryptedMappingsSurviveRestartWithoutPlaintext(t *testing.T) {
	r, o := sessionFixture(t)
	ctx := context.Background()
	cookie := authn.Secret("ovss_")
	m := Mapping{Grant: authn.Secret("ovsg_"), PrincipalID: repository.NewID(), CSRF: authn.Secret("ovsc_"), ExpiresAt: time.Now().Add(time.Hour)}
	if err := r.Save(ctx, cookie, m); err != nil {
		t.Fatal(err)
	}
	var blob []byte
	var storedHash []byte
	if err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT session_hash,envelope FROM browser_sessions").Scan(&storedHash, &blob)
	}); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cookie, m.Grant, m.CSRF} {
		if bytes.Contains(blob, []byte(secret)) || bytes.Contains(storedHash, []byte(secret)) {
			t.Fatal("plaintext in web database")
		}
	}
	_ = r.store.Close()
	r = sessionOpen(t, o)
	restored, err := r.Load(ctx, cookie)
	if err != nil || restored.Grant != m.Grant || !restored.ExpiresAt.Equal(m.ExpiresAt) {
		t.Fatal("restart mapping failed", err)
	}
	other := authn.Secret("ovss_")
	hash := authn.Hash(other)
	if err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO browser_sessions VALUES(?,?,?)", hash[:], blob, m.ExpiresAt.Unix())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Load(ctx, other); err == nil {
		t.Fatal("ciphertext moved to a forged cookie")
	}
	r.now = func() time.Time { return m.ExpiresAt.Add(time.Second) }
	if _, err = r.Load(ctx, cookie); err == nil {
		t.Fatal("expired encrypted mapping accepted")
	}
}
func TestTamperedEnvelopeAndWrongKeyCannotCreateIdentity(t *testing.T) {
	r, _ := sessionFixture(t)
	ctx := context.Background()
	cookie := authn.Secret("ovss_")
	m := Mapping{Grant: authn.Secret("ovsg_"), PrincipalID: repository.NewID(), CSRF: authn.Secret("ovsc_"), ExpiresAt: time.Now().Add(time.Hour)}
	if err := r.Save(ctx, cookie, m); err != nil {
		t.Fatal(err)
	}
	original := r.keys
	r.keys, _ = secret.NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{3}, 32)})
	if _, err := r.Load(ctx, cookie); err == nil {
		t.Fatal("wrong key decrypted mapping")
	}
	r.keys = original
	hash := authn.Hash(cookie)
	tampered := []byte(`{"principal_id":"administrator","role":"Administrator"}`)
	if err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE browser_sessions SET envelope=?,expires_at=? WHERE session_hash=?", tampered, time.Now().Add(100*time.Hour).Unix(), hash[:])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Load(ctx, cookie); err == nil {
		t.Fatal("web database forgery authenticated")
	}
	if !r.store.Status().Writable {
		t.Fatal("invalid mapping damaged storage")
	}
}
