//go:build linux

package inventory

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	domain "github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

func TestCreationReservationNeedsCommitEvidenceAndNeverRecycles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	options := sqlite.Options{Path: filepath.Join(dir, "manager.db"), Kind: repository.Manager, SoftwareVersion: "test"}
	must(sqlite.Initialize(ctx, options))
	store, err := sqlite.Open(ctx, options)
	must(err)
	defer func() { _ = store.Close() }()
	reg, err := New(store)
	must(err)
	o := domain.Observation{Evidence: evidence(), Rows: domain.Rows{"Bridge": {}, "Port": {}, "Interface": {}}}
	decision, err := reg.Reconcile(ctx, o)
	must(err)
	bind := func(table string) candidate.Binding {
		return candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: decision.Generation}
	}
	i := candidate.StoredIntent{Operation: candidate.BridgeCreate, Object: bind("Bridge"), Creation: &candidate.BridgeCreation{Name: "br-new", Root: o.Evidence.Root, Port: bind("Port"), Interface: bind("Interface"), AfterPresent: true}}
	c := candidate.Candidate{Intents: []candidate.StoredIntent{i}}
	transaction, marker := repository.NewID(), strings.Repeat("a", 64)
	must(store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO request_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)", repository.NewID(), repository.NewID(), transaction, repository.NewID(), "owner", "candidate", "revision", "hash", []byte(`{}`), "received", 1); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO transaction_journal VALUES(?,1,'admitted',?)", transaction, []byte(`{}`)); err != nil {
			return err
		}
		return ReserveCreations(ctx, tx, transaction, marker, c)
	}))
	decision, err = reg.Reconcile(ctx, o)
	must(err)
	if len(decision.Bindings) != 0 {
		t.Fatal("uncommitted reservations appeared in inventory")
	}
	must(store.Close())
	store, err = sqlite.Open(ctx, options)
	must(err)
	reg, err = New(store)
	must(err)
	for _, b := range candidate.CreationBindings(i) {
		value := marker
		if b.Table == "Port" {
			value = "forged"
		}
		o.Rows[b.Table][b.OVSUUID] = domain.Row{UUID: b.OVSUUID, Values: map[string]any{"name": "br-new", "external_ids": map[string]any{CreationMarker: value}}}
	}
	if _, err = reg.Reconcile(ctx, o); err == nil {
		t.Fatal("wrong native marker activated reserved identity")
	}
	must(store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var count int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM identities WHERE state='pending'").Scan(&count); err != nil {
			return err
		}
		if count != 3 {
			t.Fatal("failed observation partially activated identities")
		}
		return nil
	}))
	for _, b := range candidate.CreationBindings(i) {
		o.Rows[b.Table][b.OVSUUID].Values["external_ids"] = map[string]any{CreationMarker: marker}
	}
	decision, err = reg.Reconcile(ctx, o)
	must(err)
	for _, b := range candidate.CreationBindings(i) {
		if got := decision.Bindings[domain.Key(b.Table, b.OVSUUID)]; got.ManagementID != b.ManagementID || got.State != "active" {
			t.Fatal("assigned identity lost", got)
		}
	}
	saved := o.Rows
	o.Rows = domain.Rows{"Bridge": {}, "Port": {}, "Interface": {}}
	decision, err = reg.Reconcile(ctx, o)
	must(err)
	for _, b := range candidate.CreationBindings(i) {
		if !decision.Retired[b.OVSUUID] {
			t.Fatal("deleted identity lacks tombstone")
		}
	}
	if err = store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error { return ReserveCreations(ctx, tx, transaction, marker, c) }); err == nil {
		t.Fatal("old UUIDs were reserved twice")
	}
	o.Rows = saved
	if _, err = reg.Reconcile(ctx, o); err == nil {
		t.Fatal("old native UUID reappeared with an old marker")
	}
}
