//go:build linux

package sessions

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacySessionRotationPreservesExpiryAndExplicitRestoreRevokes(t *testing.T) {
	r, o := sessionFixture(t)
	ctx := context.Background()
	cookie := authn.Secret("ovss_")
	hash := authn.Hash(cookie)
	m := Mapping{Grant: authn.Secret("ovsg_"), PrincipalID: repository.NewID(), CSRF: authn.Secret("ovsc_"), ExpiresAt: time.Now().Add(time.Hour)}
	plain, _ := json.Marshal(m)
	blob, err := authn.Seal(r.keys.Keys[1], plain, r.aad(hash))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO browser_sessions VALUES(?,?,?)", hash[:], blob, m.ExpiresAt.Unix())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := secret.NewRing(2, map[int][]byte{1: r.keys.Keys[1], 2: bytes.Repeat([]byte{3}, 32)})
	if err = r.Rewrap(ctx, next); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Load(ctx, cookie); err != nil || got.Grant != m.Grant || !got.ExpiresAt.Equal(m.ExpiresAt) {
		t.Fatal("rotation extended or lost session", err)
	}
	epoch, _ := r.Epoch(ctx)
	snapshot, err := r.store.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlite.VerifyBackup(ctx, snapshot, repository.Web); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(snapshot, "database.sqlite"))
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	o.Path = filepath.Join(root, "web.db")
	if err = os.WriteFile(o.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restored, err := NewWithKeys(ctx, db, next)
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.PrepareRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Load(ctx, cookie); err == nil {
		t.Fatal("restored cookie authenticated")
	}
	newEpoch, _ := restored.Epoch(ctx)
	if newEpoch == epoch {
		t.Fatal("restore reused request epoch")
	}
}
