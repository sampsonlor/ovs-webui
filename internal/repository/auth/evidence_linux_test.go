//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

func seedJob(t *testing.T, r *Repository, c authn.LoginResult, state string) (evidence.Job, apitypes.Result) {
	t.Helper()
	epoch, err := r.receipts.Epoch(testContext)
	if err != nil {
		t.Fatal(err)
	}
	command := requests.Command{Principal: c.Claims.PrincipalID, Credential: c.Claims.CredentialID, Capability: "configuration.validate", Epoch: epoch, Domain: "management", ID: apitypes.RequestID(time.Now()), Operation: "createValidation", Method: "POST", URI: "/api/v1/validations", Payload: json.RawMessage(`{}`)}
	var j evidence.Job
	result, err := r.receipts.Execute(testContext, command, func(ctx context.Context) error {
		_, err := r.CheckAuth(ctx, c.Grant, authn.Check{Capability: "configuration.validate"})
		return err
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		var err error
		j, err = evidence.CreateJob(ctx, tx, evidence.Job{ID: repository.NewID(), State: state, Cancellable: true, Handler: "provider", Applied: "unknown"})
		return requests.Mutation{Status: 202, Body: json.RawMessage(`{}`), Job: &apitypes.Ref{Kind: "job", ID: j.ID}, Resource: &apitypes.Ref{Kind: "job", ID: j.ID}}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return j, result
}

func TestSharedJobCancellationReplayCurrentAuthorityAndAudit(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	role := roleID(t, r, admin, "Administrator")
	createUser(t, r, admin, "second-admin", role)
	other := login(t, r, "second-admin", false)
	queued, original := seedJob(t, r, admin, "queued")
	if queued.Correlation != original.Receipt.CorrelationID {
		t.Fatal("job/receipt correlation split")
	}
	input := request(t, r, "POST", "/jobs/"+queued.ID+"/cancellations", map[string]any{}, "")
	if _, err := r.ExecuteAuth(testContext, other.Grant, input); err == nil {
		t.Fatal("cross-user cancellation admitted")
	}
	if _, err := r.ReadAuth(testContext, other.Grant, authn.Query{Method: "GET", URI: "/api/v1/jobs/" + queued.ID}); err == nil {
		t.Fatal("cross-user job read")
	}
	first, err := r.ExecuteAuth(testContext, admin.Grant, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ExecuteAuth(testContext, admin.Grant, input)
	if err != nil || !replay.Replayed || replay.Receipt.Job.ID != first.Receipt.Job.ID {
		t.Fatal("cancellation replay ran again", err)
	}
	job := read(t, r, admin, "/jobs/"+queued.ID)
	if job["state"] != "cancelled" || job["commit_outcome"] != "not-sent" || job["applied_outcome"] != "unknown" {
		t.Fatal("pre-dispatch cancellation lost outcome separation", job)
	}
	receipt := read(t, r, admin, "/requests/"+original.Receipt.RequestID+"?domain=management&epoch="+original.Receipt.Epoch)
	if receipt["state"] != "completed" {
		t.Fatal("target receipt not settled atomically")
	}
	audit := read(t, r, admin, "/audit?correlation_id="+first.Receipt.CorrelationID)
	items := audit["items"].([]any)
	if len(items) != 1 {
		t.Fatal("duplicate or missing cancellation audit", items)
	}
	record := items[0].(map[string]any)
	if record["request_id"] != input.RequestID || record["job_id"] != first.Receipt.Job.ID || record["actor_id"] != admin.Claims.PrincipalID || record["credential_id"] != admin.Claims.CredentialID {
		t.Fatal("missing authoritative audit links", record)
	}
	_ = read(t, r, admin, "/audit/"+record["id"].(string))
	_ = read(t, r, admin, "/audit/export?correlation_id="+first.Receipt.CorrelationID)
	if err = r.RevokeAuth(testContext, admin.Grant); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ExecuteAuth(testContext, admin.Grant, input); err == nil {
		t.Fatal("revoked credential recovered a receipt")
	}
}
func TestRunningCancellationAndRestartNeverInventProviderOutcome(t *testing.T) {
	r, o := fixture(t)
	admin := login(t, r, "admin", false)
	job, original := seedJob(t, r, admin, "running")
	input := request(t, r, "POST", "/jobs/"+job.ID+"/cancellations", map[string]any{}, "")
	if _, err := r.ExecuteAuth(testContext, admin.Grant, input); err != nil {
		t.Fatal(err)
	}
	current := read(t, r, admin, "/jobs/"+job.ID)
	if current["state"] != "cancel-requested" || current["business_outcome"] != "unknown" || current["completed_at"] != nil {
		t.Fatal("sent work claimed cancelled")
	}
	if err := r.store.Close(); err != nil {
		t.Fatal(err)
	}
	r = openFixture(t, o)
	if err := r.InitializeEvidence(testContext); err != nil {
		t.Fatal(err)
	}
	current = read(t, r, admin, "/jobs/"+job.ID)
	if current["state"] != "needs-attention" || current["dispatch_state"] != "unknown" || current["business_outcome"] != "unknown" {
		t.Fatal("restart replayed or completed uncertain job", current)
	}
	receipt := read(t, r, admin, "/requests/"+original.Receipt.RequestID+"?domain=management&epoch="+original.Receipt.Epoch)
	if receipt["state"] != "accepted" {
		t.Fatal("unresolved receipt incorrectly terminal")
	}
	before := current["sequence"]
	if err := r.InitializeEvidence(testContext); err != nil {
		t.Fatal(err)
	}
	if read(t, r, admin, "/jobs/"+job.ID)["sequence"] != before {
		t.Fatal("repeated startup duplicated recovery")
	}
	audit := read(t, r, admin, "/audit?filter=job-recovery-required")
	if item := audit["items"].([]any)[0].(map[string]any); item["actor_id"] != nil {
		t.Fatal("system recovery attributed to job owner")
	}
}
func TestSharedEvidenceRetentionProtectsUnresolvedReferencesAndRevokesCursor(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", false)
	job, original := seedJob(t, r, admin, "running")
	now := time.Now()
	old := now.Add(-200 * 24 * time.Hour)
	var protectedID, expiredID string
	if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		protectedID, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "protected-old-evidence", Result: "recorded", Job: job.ID, Correlation: job.Correlation, RequestID: original.Receipt.RequestID, Created: old})
		if err != nil {
			return err
		}
		expiredID, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Unknown", Operation: "expired-old-evidence", Result: "recorded", Created: old})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page := read(t, r, admin, "/audit?limit=1")
	cursor := page["next_cursor"].(string)
	if err := evidence.Prune(testContext, r.store, now); err != nil {
		t.Fatal(err)
	}
	_ = read(t, r, admin, "/audit/"+protectedID)
	if _, err := r.ReadAuth(testContext, admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/audit/" + expiredID}); err == nil {
		t.Fatal("expired unreferenced audit retained")
	}
	_, err := r.ReadAuth(testContext, admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/audit?limit=1&cursor=" + cursor})
	wantCode(t, err, "CURSOR_EXPIRED")
	if err = evidence.Prune(testContext, r.store, now.Add(-time.Minute)); err == nil {
		t.Fatal("clock rollback pruned evidence")
	}
	if err = r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE api_receipts SET created_at_ms=? WHERE request_id=?", old.UnixMilli(), original.Receipt.RequestID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = r.receipts.Prune(testContext); err != nil {
		t.Fatal(err)
	}
	_ = read(t, r, admin, "/requests/"+original.Receipt.RequestID+"?domain=management&epoch="+original.Receipt.Epoch)
}
func TestEvidenceFailureRollsBackSecurityEffectJobAndReceipt(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TRIGGER synthetic_evidence_failure BEFORE INSERT ON evidence_records BEGIN SELECT RAISE(ABORT,'synthetic evidence failure'); END")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	input := request(t, r, "POST", "/roles", map[string]any{"name": "synthetic-must-not-exist", "capabilities": []string{"state.read"}}, "")
	if _, err := r.ExecuteAuth(testContext, admin.Grant, input); err == nil {
		t.Fatal("effect admitted without evidence")
	}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		for _, query := range []string{"SELECT count(*) FROM auth_roles WHERE name='synthetic-must-not-exist'", "SELECT count(*) FROM api_receipts WHERE request_id='" + input.RequestID + "'", "SELECT count(*) FROM evidence_jobs WHERE json_extract(document,'$.request_id')='" + input.RequestID + "'"} {
			var n int
			if err := q.QueryRowContext(ctx, query).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Fatal("partial effect or evidence after rollback")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
