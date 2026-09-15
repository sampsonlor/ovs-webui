package evidence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	_ "modernc.org/sqlite"
)

var ctx = context.Background()

func database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations.For(repository.Manager) {
		if _, err = db.Exec(m.SQL); err != nil {
			t.Fatalf("migration %d: %v", m.Version, err)
		}
	}
	return db
}
func write(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func principal() authn.Claims {
	return authn.Claims{PrincipalID: repository.NewID(), Revision: repository.NewID(), Capabilities: []string{"jobs.read", "jobs.cancel", "events.read", "audit.read", "configuration.validate"}}
}
func TestDurableJobsKeepIndependentOutcomesAndAtomicEvidence(t *testing.T) {
	db := database(t)
	c := principal()
	var j Job
	write(t, db, func(tx *sql.Tx) error {
		var err error
		j, err = CreateJob(ctx, tx, Job{Owner: c.PrincipalID, Operation: "createValidation", Capability: "configuration.validate", Cancellable: true})
		return err
	})
	tx, _ := db.BeginTx(ctx, nil)
	changed, err := ChangeJob(ctx, tx, j.ID, j.Sequence, Transition{State: "running", Dispatch: "prepared", Reason: "executor-admitted"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	still, err := LoadJob(ctx, db, j.ID)
	if err != nil || still.State != "queued" || still.Sequence != j.Sequence {
		t.Fatal("rolled-back transition leaked", err)
	}
	var records int
	_ = db.QueryRow("SELECT count(*) FROM evidence_records").Scan(&records)
	if records != 1 {
		t.Fatal("evidence escaped rollback")
	}
	write(t, db, func(tx *sql.Tx) error {
		var err error
		changed, err = ChangeJob(ctx, tx, j.ID, j.Sequence, Transition{State: "running", Dispatch: "sent", Commit: "unknown", Applied: "unknown", Reason: "provider-dispatched"}, time.Now())
		return err
	})
	write(t, db, func(tx *sql.Tx) error {
		var err error
		changed, err = ChangeJob(ctx, tx, j.ID, changed.Sequence, Transition{State: "cancel-requested", Reason: "operator-requested-cancellation"}, time.Now())
		return err
	})
	if changed.Commit != "unknown" || changed.Applied != "unknown" || changed.Business != "unknown" || changed.Completed != nil || !changed.CancelRequested {
		t.Fatal("cancel request invented a provider outcome")
	}
	write(t, db, func(tx *sql.Tx) error {
		var err error
		changed, err = ChangeJob(ctx, tx, j.ID, changed.Sequence, Transition{State: "succeeded", Business: "success", Commit: "committed", Applied: "pending", Confirmation: "pending", Reason: "commit-observed"}, time.Now())
		return err
	})
	if changed.Completed == nil || changed.Applied != "pending" || changed.Confirmation != "pending" {
		t.Fatal("job completion inferred Applied/confirmation")
	}
}
func TestRecordRedactionDeduplicationAndExternalAttribution(t *testing.T) {
	db := database(t)
	r := Record{Collection: "event", Origin: "External", Actor: repository.NewID(), Credential: repository.NewID(), Operation: "ovsdb-inventory-changed", Correlation: repository.NewID(), Result: "observed", DedupKey: "synthetic-provider-event-1", Details: map[string]any{"password": "synthetic-private-value", "Authorization": "Bearer synthetic-sensitive-value"}}
	var id string
	write(t, db, func(tx *sql.Tx) error { var err error; id, err = Append(ctx, tx, r); return err })
	write(t, db, func(tx *sql.Tx) error {
		same, err := Append(ctx, tx, r)
		if same != id {
			t.Fatal("duplicate event has new identity")
		}
		return err
	})
	var blob []byte
	_ = db.QueryRow("SELECT document FROM evidence_records WHERE id=?", id).Scan(&blob)
	if bytes.Contains(blob, []byte("synthetic-private")) || bytes.Contains(blob, []byte("Bearer")) || bytes.Contains(blob, []byte(r.Actor)) || bytes.Contains(blob, []byte(r.Credential)) {
		t.Fatal("secret or guessed actor persisted")
	}
	r.Result = "changed"
	tx, _ := db.BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := Append(ctx, tx, r); err == nil {
		t.Fatal("dedup key overwrote contradictory evidence")
	}
}
func TestEvidencePagesScopeSnapshotFiltersAndWireContract(t *testing.T) {
	db := database(t)
	c := principal()
	key := bytes.Repeat([]byte{7}, 32)
	now := time.Now()
	corr := repository.NewID()
	write(t, db, func(tx *sql.Tx) error {
		for i := 0; i < 5; i++ {
			if _, err := Append(ctx, tx, Record{Collection: "event", Origin: "External", Operation: "ovsdb-inventory-changed", Correlation: corr, Result: "observed", Created: now}); err != nil {
				return err
			}
		}
		for i := 0; i < 3; i++ {
			if _, err := CreateJob(ctx, tx, Job{Owner: c.PrincipalID, Operation: "createValidation", Capability: "configuration.validate"}); err != nil {
				return err
			}
		}
		return nil
	})
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/events", "/events/export", "/audit", "/audit/export", "/jobs", "/jobs/export"} {
		op, params, _ := contract.Match("GET", "/api/v1"+path)
		if op == nil {
			t.Fatal("missing route", path)
		}
		value, err := Read(ctx, db, c, op.ID, params, url.Values{}, key, now)
		if err != nil {
			t.Fatal(path, err)
		}
		encoded, _ := json.Marshal(value)
		if err = op.ValidateResponse(200, encoded); err != nil {
			t.Fatal(path, err)
		}
	}
	q := url.Values{"limit": {"1"}, "correlation_id": {corr}}
	first, err := Read(ctx, db, c, "listEvents", nil, q, key, now)
	if err != nil {
		t.Fatal(err)
	}
	page := first.(map[string]any)
	token := page["next_cursor"].(string)
	q.Set("cursor", token)
	write(t, db, func(tx *sql.Tx) error {
		_, err := Append(ctx, tx, Record{Collection: "event", Origin: "External", Operation: "ovsdb-inventory-changed", Correlation: corr, Result: "observed"})
		return err
	})
	second, err := Read(ctx, db, c, "listEvents", nil, q, key, now)
	if err != nil {
		t.Fatal("immutable append broke the fenced page", err)
	}
	if second.(map[string]any)["snapshot_id"] != page["snapshot_id"] {
		t.Fatal("mixed snapshot")
	}
	other := c
	other.PrincipalID = repository.NewID()
	if _, err = Read(ctx, db, other, "listEvents", nil, q, key, now); err == nil {
		t.Fatal("cross-principal cursor")
	}
	other = c
	other.Revision = repository.NewID()
	if _, err = Read(ctx, db, other, "listEvents", nil, q, key, now); err == nil {
		t.Fatal("revoked permission cursor")
	}
	if _, err = Read(ctx, db, c, "exportEvents", nil, q, key, now); err == nil {
		t.Fatal("cursor scope changed")
	}
	if _, err = Read(ctx, db, c, "listEvents", nil, q, key, now.Add(time.Minute)); err == nil {
		t.Fatal("expired cursor")
	}
	q = url.Values{"limit": {"1"}}
	value, err := Read(ctx, db, c, "listJobs", nil, q, key, now)
	if err != nil {
		t.Fatal(err)
	}
	jp := value.(map[string]any)
	q.Set("cursor", jp["next_cursor"].(string))
	id := jp["items"].([]map[string]any)[0]["id"].(string)
	write(t, db, func(tx *sql.Tx) error {
		j, err := LoadJob(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = ChangeJob(ctx, tx, id, j.Sequence, Transition{State: "cancelled", Business: "cancelled", Reason: "cancelled-before-dispatch"}, now)
		return err
	})
	if _, err = Read(ctx, db, c, "listJobs", nil, q, key, now); err == nil {
		t.Fatal("mutable jobs mixed across pages")
	}
	other = c
	other.PrincipalID = repository.NewID()
	if _, err = Read(ctx, db, other, "readJob", map[string]string{"job_id": id}, nil, key, now); err == nil {
		t.Fatal("other user's job disclosed")
	}
}
func TestJobAndRecordAdmissionBudgetsFailWithoutPartialWrites(t *testing.T) {
	db := database(t)
	c := principal()
	write(t, db, func(tx *sql.Tx) error {
		for i := 0; i < MaxQueued; i++ {
			if _, err := CreateJob(ctx, tx, Job{Owner: c.PrincipalID, Capability: "configuration.validate", Operation: "createValidation"}); err != nil {
				return err
			}
		}
		return nil
	})
	tx, _ := db.BeginTx(ctx, nil)
	_, err := CreateJob(ctx, tx, Job{Owner: c.PrincipalID, Capability: "configuration.validate", Operation: "createValidation"})
	tx.Rollback()
	if err == nil {
		t.Fatal("queue budget ignored")
	}
	var count int
	_ = db.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != MaxQueued {
		t.Fatal("failed admission left a job")
	}
	write(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO evidence_records(id,collection,created_at_ms,correlation_id,operation,source,fingerprint,document) SELECT 'synthetic-'||x,'event',0,'synthetic','seed','Unknown','synthetic',x'7b7d' FROM n`)
		return err
	})
	tx, _ = db.BeginTx(ctx, nil)
	_, err = Append(ctx, tx, Record{Collection: "event", Origin: "Unknown", Operation: "new-event", Result: "recorded"})
	tx.Rollback()
	if err == nil {
		t.Fatal("event capacity ignored")
	}
	if !strings.Contains(err.Error(), "EVIDENCE_CAPACITY_REACHED") {
		t.Fatal(err)
	}
}
