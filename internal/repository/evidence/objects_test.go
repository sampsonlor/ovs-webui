package evidence

import (
	"bytes"
	"database/sql"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestObjectEvidenceKeepsTransactionIdentityAndSnapshotScope(t *testing.T) {
	db := database(t)
	claims, key, now := principal(), bytes.Repeat([]byte{3}, 32), time.Now()
	target, replacement, transaction := repository.NewID(), repository.NewID(), repository.NewID()
	appendRecord := func(objects []apitypes.Ref) {
		write(t, db, func(tx *sql.Tx) error {
			_, err := Append(ctx, tx, Record{Collection: "audit", Origin: "Manager", Operation: "safe-apply-state", Result: "confirmed", Object: &apitypes.Ref{Kind: "transaction", ID: transaction}, Transaction: transaction, RelatedObjects: objects})
			return err
		})
	}
	refs := []apitypes.Ref{{Kind: "interface", ID: target}}
	appendRecord(refs)
	appendRecord(refs)
	appendRecord(nil)
	query := url.Values{"object_id": {target}, "limit": {"1"}}
	first, err := Read(ctx, db, claims, "listAudit", nil, query, key, now)
	if err != nil {
		t.Fatal(err)
	}
	page := first.(map[string]any)
	record := page["items"].([]map[string]any)[0]
	if record["transaction_id"] != transaction || record["object_ref"].(map[string]any)["id"] != transaction {
		t.Fatal("primary transaction reference changed")
	}
	found := false
	for _, ref := range record["object_refs"].([]apitypes.Ref) {
		found = found || ref.ID == target && ref.Kind == "interface"
	}
	if !found {
		t.Fatal("direct target missing from detail references")
	}
	query.Set("cursor", page["next_cursor"].(string))
	appendRecord(refs) // New evidence must not leak into the sealed old snapshot.
	second, err := Read(ctx, db, claims, "listAudit", nil, query, key, now)
	if err != nil || second.(map[string]any)["next_cursor"] != nil {
		t.Fatal("object snapshot mixed appended evidence", err)
	}
	query.Set("object_id", replacement)
	if _, err = Read(ctx, db, claims, "listAudit", nil, query, key, now); err == nil {
		t.Fatal("cursor crossed exact object identity")
	}
	query.Del("cursor")
	empty, err := Read(ctx, db, claims, "listAudit", nil, query, key, now)
	if err != nil || len(empty.(map[string]any)["items"].([]map[string]any)) != 0 {
		t.Fatal("same-name replacement could inherit old evidence", err)
	}
	query.Set("object_id", transaction)
	all, err := Read(ctx, db, claims, "listAudit", nil, url.Values{"object_id": {transaction}}, key, now)
	if err != nil || len(all.(map[string]any)["items"].([]map[string]any)) != 4 {
		t.Fatal("existing primary-object filtering regressed", err)
	}
	claims.Capabilities = []string{"inventory.read"}
	if _, err = Read(ctx, db, claims, "listAudit", nil, query, key, now); err == nil {
		t.Fatal("object scope granted Audit permission")
	}
}

func TestRelatedObjectJobEventsRemainAtomicBoundedAndDeduplicated(t *testing.T) {
	db := database(t)
	target := apitypes.Ref{Kind: "interface", ID: repository.NewID()}
	request := WithRequest(ctx, Request{Principal: repository.NewID(), Capability: "configuration.validate", Operation: "createValidation"})
	var job Job
	write(t, db, func(tx *sql.Tx) error {
		var err error
		job, err = CreateJob(request, tx, Job{RelatedObjects: []apitypes.Ref{target}})
		return err
	})
	tx, _ := db.BeginTx(ctx, nil)
	_, err := ChangeJob(request, tx, job.ID, job.Sequence, Transition{State: "running", Reason: "executor-admitted"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	var count int
	if err = db.QueryRow("SELECT count(*) FROM evidence_record_objects WHERE object_id=?", target.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("rolled-back job association escaped", count, err)
	}
	write(t, db, func(tx *sql.Tx) error {
		_, err := ChangeJob(request, tx, job.ID, job.Sequence, Transition{State: "running", Reason: "executor-admitted"}, time.Now())
		return err
	})
	if err = db.QueryRow("SELECT count(*) FROM evidence_record_objects WHERE object_id=?", target.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("job transition lost immutable association", count, err)
	}
	r := Record{Collection: "event", Origin: "External", Operation: "observed", Result: "recorded", Correlation: repository.NewID(), RelatedObjects: []apitypes.Ref{target}, DedupKey: "bounded-object-observation"}
	write(t, db, func(tx *sql.Tx) error { _, err := Append(ctx, tx, r); return err })
	write(t, db, func(tx *sql.Tx) error { _, err := Append(ctx, tx, r); return err })
	for _, bad := range [][]apitypes.Ref{{target, target}, {{Kind: "interface", ID: "native-name-is-not-an-identity"}}, make([]apitypes.Ref, MaxRelatedObjects+1)} {
		tx, _ := db.BeginTx(ctx, nil)
		invalid := r
		invalid.DedupKey = ""
		invalid.RelatedObjects = bad
		if _, err = Append(ctx, tx, invalid); err == nil {
			t.Fatal("invalid related object accepted")
		}
		if _, err = CreateJob(request, tx, Job{RelatedObjects: bad}); err == nil {
			t.Fatal("invalid job target accepted")
		}
		tx.Rollback()
	}
	r.RelatedObjects = []apitypes.Ref{{Kind: "interface", ID: repository.NewID()}}
	tx, _ = db.BeginTx(ctx, nil)
	if _, err = Append(ctx, tx, r); err == nil {
		t.Fatal("dedup reused with contradictory object identity")
	}
	tx.Rollback()
}

func TestObjectIndexMigrationPreservesLegacyCoverageAndCascadesPruning(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	steps := migrations.For(repository.Manager)
	for _, m := range steps[:len(steps)-1] {
		if _, err = db.Exec(m.SQL); err != nil {
			t.Fatal(err)
		}
	}
	id, primary := repository.NewID(), repository.NewID()
	if _, err = db.Exec("INSERT INTO evidence_records(id,collection,created_at_ms,correlation_id,operation,object_id,source,fingerprint,document) VALUES(?,'audit',0,?,'legacy',?,'Unknown','legacy',?)", id, repository.NewID(), primary, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(steps[len(steps)-1].SQL); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM evidence_record_objects WHERE record_id=? AND object_id=?", id, primary).Scan(&count); err != nil || count != 1 {
		t.Fatal("legacy primary reference lost", err)
	}
	if _, err = db.Exec("DELETE FROM evidence_records WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM evidence_record_objects").Scan(&count); err != nil || count != 0 {
		t.Fatal("retention left orphan associations", err)
	}
}
