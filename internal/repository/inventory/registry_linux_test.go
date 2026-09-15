//go:build linux

package inventory

import (
	"context"
	"database/sql"
	domain "github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentIdentitiesTombstonesAndReviewedReconciliation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	options := sqlite.Options{Path: filepath.Join(dir, "manager.db"), Kind: repository.Manager, SoftwareVersion: "test"}
	if err := sqlite.Initialize(ctx, options); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	reg, _ := New(store)
	o := domain.Observation{Evidence: evidence(), Rows: domain.Rows{"Port": {"original": {UUID: "original", Values: map[string]any{"name": "same-name"}}}}}
	d, err := reg.Reconcile(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	id := d.Bindings["Port/original"].ManagementID
	generation := d.Generation
	_ = store.Close()
	store, err = sqlite.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ = New(store)
	o.Evidence.ObservedAt = o.Evidence.ObservedAt.Add(time.Second)
	o.Evidence.Continuous = false
	d, err = reg.Reconcile(ctx, o)
	if err != nil || d.Generation != generation || d.Bindings["Port/original"].ManagementID != id {
		t.Fatal("ordinary restart changed identity", err)
	}
	delete(o.Rows["Port"], "original")
	d, err = reg.Reconcile(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	o.Rows["Port"]["new"] = domain.Row{UUID: "new", Values: map[string]any{"name": "same-name"}}
	d, err = reg.Reconcile(ctx, o)
	if err != nil || d.Bindings["Port/new"].ManagementID == id {
		t.Fatal("name inherited old identity", err)
	}
	var tombstone string
	err = store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT state FROM identities WHERE management_id=?", id).Scan(&tombstone)
	})
	if err != nil || tombstone != "tombstone" {
		t.Fatal("tombstone missing", err)
	}
	o.Evidence.File.Inode++
	d, err = reg.Reconcile(ctx, o)
	if err != nil || d.State != "reconciliation-required" || len(d.Bindings) != 0 {
		t.Fatal("replacement auto-associated", err)
	}
	if _, err = reg.Accept(ctx, o, "bad-digest", "reviewed synthetic restore"); err == nil {
		t.Fatal("unreviewed evidence accepted")
	}
	d, err = reg.Accept(ctx, o, d.PendingDigest, "reviewed synthetic restore")
	if err != nil || d.Generation == generation || d.State != "confirmed" {
		t.Fatal("explicit reconciliation failed", err)
	}
	var audits int
	err = store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT count(*) FROM auth_audit WHERE operation='inventory-explicit-reconciliation'").Scan(&audits)
	})
	if err != nil || audits != 1 {
		t.Fatal("review audit missing", err)
	}
}
