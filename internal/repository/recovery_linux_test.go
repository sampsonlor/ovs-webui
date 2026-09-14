//go:build linux

package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/manager"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/repository/web"
)

var ctx = context.Background()

func open(t *testing.T, o sqlite.Options) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func pair(t *testing.T) (sqlite.Options, sqlite.Options) {
	t.Helper()
	w := sqlite.Options{Path: filepath.Join(t.TempDir(), "web.db"), Kind: repository.Web, SoftwareVersion: "test"}
	m := sqlite.Options{Path: filepath.Join(t.TempDir(), "manager.db"), Kind: repository.Manager, SoftwareVersion: "test"}
	for _, o := range []sqlite.Options{w, m} {
		if err := sqlite.Initialize(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	return w, m
}
func count(t *testing.T, s *sqlite.Store, table string) int {
	t.Helper()
	n := 0
	if err := s.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestLostHandoffReplyAndNewDraftSurviveBothRestarts(t *testing.T) {
	wo, mo := pair(t)
	ws, ms := open(t, wo), open(t, mo)
	w, _ := web.New(ws)
	m, _ := manager.New(ms)
	draft, err := w.SaveCandidate(ctx, "candidate", "synthetic-user", "", json.RawMessage(`{"intents":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	h, err := w.PrepareHandoff(ctx, "original-request", draft.ID, draft.OwnerID, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	_ = ms.Close()
	ws, ms = open(t, wo), open(t, mo)
	w, _ = web.New(ws)
	m, _ = manager.New(ms)
	pending, err := w.Pending(ctx, 8)
	if err != nil || len(pending) != 1 || pending[0].TransactionID != h.TransactionID {
		t.Fatal("lost pending immutable handoff", err)
	}
	first, err := m.Receive(ctx, pending[0])
	if err != nil {
		t.Fatal(err)
	} // Simulate losing this reply before web acknowledgement.
	newer, err := w.SaveCandidate(ctx, draft.ID, draft.OwnerID, draft.Revision, json.RawMessage(`{"newer_draft":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = ms.Close()
	ms = open(t, mo)
	m, _ = manager.New(ms)
	same, err := m.Receive(ctx, h)
	if err != nil || same.ID != first.ID {
		t.Fatal("retried receipt duplicated work", err)
	}
	for _, table := range []string{"request_receipts", "transaction_journal", "jobs", "audit", "events"} {
		if count(t, ms, table) != 1 {
			t.Fatal("duplicated receipt evidence", table)
		}
	}
	repeated, err := w.PrepareHandoff(ctx, h.RequestID, draft.ID, draft.OwnerID, draft.Revision)
	if err != nil || repeated.TransactionID != h.TransactionID || string(repeated.Document) != string(h.Document) {
		t.Fatal("retry changed immutable request", err)
	}
	changed := h
	changed.OwnerID = "different-owner"
	if _, err = m.Receive(ctx, changed); err != repository.ErrConflict {
		t.Fatal("idempotency identity conflict accepted", err)
	}
	if err = w.Acknowledge(ctx, same); err != nil {
		t.Fatal(err)
	}
	if err = w.Acknowledge(ctx, same); err != nil {
		t.Fatal("ack not idempotent", err)
	}
	_ = ws.Close()
	ws = open(t, wo)
	w, _ = web.New(ws)
	pending, err = w.Pending(ctx, 8)
	if err != nil || len(pending) != 0 {
		t.Fatal("ack did not survive restart", err)
	}
	kept, err := w.Candidate(ctx, draft.ID, draft.OwnerID)
	if err != nil || kept.Revision != newer.Revision {
		t.Fatal("receipt removed a newer draft", err)
	}
	if _, err = w.Candidate(ctx, draft.ID, "another-user"); err != repository.ErrNotFound {
		t.Fatal("cross-owner draft leaked")
	}
}
func TestAuthoritySeparationAndDurableMetadata(t *testing.T) {
	wo, mo := pair(t)
	ws, ms := open(t, wo), open(t, mo)
	w, _ := web.New(ws)
	m, _ := manager.New(ms)
	if _, err := web.New(ms); err != repository.ErrInvalid {
		t.Fatal("manager opened as web")
	}
	if _, err := manager.New(ws); err != repository.ErrInvalid {
		t.Fatal("web opened as manager")
	}
	if err := w.PutMetadata(ctx, repository.Metadata{Kind: "role", OwnerID: "user", ID: "administrator", Document: json.RawMessage(`{}`)}); err != repository.ErrInvalid {
		t.Fatal("web metadata accepted authority kind")
	}
	for _, kind := range []repository.MetadataKind{repository.Profile, repository.Label, repository.Preference, repository.Override} {
		if err := w.PutMetadata(ctx, repository.Metadata{Kind: kind, OwnerID: "user", ID: "metadata", Document: json.RawMessage(`{"synthetic":true}`)}); err != nil {
			t.Fatal(err)
		}
	}
	session := repository.SessionMapping{SessionHash: [32]byte{1}, PrincipalID: "user", GrantSecretRef: "session/fixture-secret-ref", ExpiresAt: time.Now().Add(time.Hour)}
	if err := w.SaveSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := m.PutPrincipal(ctx, repository.Principal{ID: "user", Name: "synthetic-user", Disabled: true, PasswordVerifier: []byte("synthetic-verifier")}); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	_ = ms.Close()
	ws, ms = open(t, wo), open(t, mo)
	w, _ = web.New(ws)
	m, _ = manager.New(ms)
	got, err := w.Session(ctx, session.SessionHash)
	if err != nil || got.GrantSecretRef != session.GrantSecretRef {
		t.Fatal("session reference not durable", err)
	}
	p, err := m.Principal(ctx, "user")
	if err != nil || !p.Disabled {
		t.Fatal("authority not durable", err)
	}
	if count(t, ws, "metadata") != 4 {
		t.Fatal("metadata lost")
	}
	var n int
	if err := ws.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name IN ('principals','roles','grants','transaction_journal')").Scan(&n)
	}); err != nil || n != 0 {
		t.Fatal("manager authority tables in web database", err)
	}
}
func TestReceiptEvidenceIsOneAtomicWrite(t *testing.T) {
	wo, mo := pair(t)
	ws, ms := open(t, wo), open(t, mo)
	w, _ := web.New(ws)
	m, _ := manager.New(ms)
	draft, err := w.SaveCandidate(ctx, "candidate", "user", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	h, err := w.PrepareHandoff(ctx, "request", "candidate", "user", draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	err = ms.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TRIGGER fail_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'synthetic-secret-error'); END;")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Receive(ctx, h); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("expected redacted atomic failure", err)
	}
	for _, table := range []string{"request_receipts", "transaction_journal", "jobs", "audit", "events"} {
		if count(t, ms, table) != 0 {
			t.Fatal("partial receipt survived rollback", table)
		}
	}
}
