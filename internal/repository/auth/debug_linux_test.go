//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/runtimehealth"
)

func TestManagementDebugRequiresCurrentElevatedBrowserGrant(t *testing.T) {
	r, _ := fixture(t)
	debug := runtimehealth.NewDebug(time.Now)
	r.WithManagementDebug(debug)
	admin := login(t, r, "admin", false)
	req := request(t, r, "POST", "/health/debug", map[string]any{"duration_seconds": 300}, "")
	_, err := r.ExecuteAuth(testContext, admin.Grant, req)
	wantCode(t, err, "REAUTHENTICATION_REQUIRED")

	elevated := login(t, r, "admin", true)
	token := execute(t, r, elevated, "POST", "/tokens", map[string]any{
		"name": "synthetic-debug-token", "scopes": []string{"management.debug.write"},
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}, "")
	var created struct{ Secret string }
	if err := json.Unmarshal(token.Body, &created); err != nil || created.Secret == "" {
		t.Fatal("missing synthetic token", err)
	}
	_, err = r.ExecuteAuth(testContext, created.Secret, req)
	wantCode(t, err, "REAUTHENTICATION_REQUIRED")

	createUser(t, r, elevated, "reader", roleID(t, r, elevated, "Reader"))
	reader := login(t, r, "reader", true)
	_, err = r.ExecuteAuth(testContext, reader.Grant, req)
	wantCode(t, err, "CAPABILITY_DENIED")
	if debug.Level() != slog.LevelInfo {
		t.Fatal("rejected command changed the log level")
	}
	invalid := request(t, r, "POST", "/health/debug", map[string]any{"duration_seconds": 301}, "")
	_, err = r.ExecuteAuth(testContext, elevated.Grant, invalid)
	wantCode(t, err, "INVALID_REQUEST")

	expired := elevated.Claims.ElevatedUntil.Add(time.Second)
	r.now = func() time.Time { return expired }
	_, err = r.ExecuteAuth(testContext, elevated.Grant, req)
	wantCode(t, err, "REAUTHENTICATION_REQUIRED")
}

func TestManagementDebugReceiptAndExpiryCannotRenewLease(t *testing.T) {
	r, options := fixture(t)
	now := time.Now()
	clock := func() time.Time { return now }
	debug := runtimehealth.NewDebug(clock)
	r.WithManagementDebug(debug)
	admin := login(t, r, "admin", true)
	req := request(t, r, "POST", "/health/debug", map[string]any{"duration_seconds": 300}, "")
	first, err := r.ExecuteAuth(testContext, admin.Grant, req)
	if err != nil || first.Status != 202 || first.Receipt.Job == nil || first.Receipt.Resource == nil ||
		first.Receipt.Resource.ID != runtimehealth.ResourceID {
		t.Fatal("missing durable Debug receipt", first, err)
	}
	lease, active := debug.State()
	if !active || lease.ExpiresAt.Sub(now) != 300*time.Second {
		t.Fatal("incorrect first lease", lease)
	}
	job := read(t, r, admin, "/jobs/"+first.Receipt.Job.ID)
	if job["state"] != "succeeded" || job["correlation_id"] != first.Receipt.CorrelationID ||
		job["reason_code"] != "debug-lease-authorized-300s" {
		t.Fatal("incorrect durable job", job)
	}

	now = now.Add(time.Minute)
	replay, err := r.ExecuteAuth(testContext, admin.Grant, req)
	replayedLease, active := debug.State()
	if err != nil || !replay.Replayed || replay.Receipt.CorrelationID != first.Receipt.CorrelationID ||
		!active || !replayedLease.ExpiresAt.Equal(lease.ExpiresAt) {
		t.Fatal("replay renewed or replaced lease", replay, err)
	}
	equivalent := req
	equivalent.Payload = []byte(strings.Replace(string(req.Payload), `"duration_seconds":300`, `"duration_seconds":3e2`, 1))
	replay, err = r.ExecuteAuth(testContext, admin.Grant, equivalent)
	if err != nil || !replay.Replayed || replay.Receipt.CorrelationID != first.Receipt.CorrelationID {
		t.Fatal("equivalent duration changed receipt identity", replay, err)
	}
	changed := req
	changed.Payload = []byte(strings.Replace(string(req.Payload), `"duration_seconds":300`, `"duration_seconds":600`, 1))
	_, err = r.ExecuteAuth(testContext, admin.Grant, changed)
	wantCode(t, err, "IDEMPOTENCY_MISMATCH")

	now = lease.ExpiresAt
	if debug.Level() != slog.LevelInfo {
		t.Fatal("Debug active at its exact deadline")
	}
	for range 2 {
		if err := r.recordDebugExpiry(testContext); err != nil {
			t.Fatal(err)
		}
	}
	assertDebugAudit(t, r, lease, admin.Grant)

	live := request(t, r, "POST", "/health/debug", map[string]any{"duration_seconds": 900}, "")
	if _, err := r.ExecuteAuth(testContext, admin.Grant, live); err != nil || debug.Level() != slog.LevelDebug {
		t.Fatal("second lease was not active before restart", err)
	}
	if err := r.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openFixture(t, options)
	restartedDebug := runtimehealth.NewDebug(clock)
	reopened.WithManagementDebug(restartedDebug)
	restartReplay, err := reopened.ExecuteAuth(testContext, admin.Grant, live)
	if err != nil || !restartReplay.Replayed || restartedDebug.Level() != slog.LevelInfo {
		t.Fatal("replay restored Debug after manager restarted", restartReplay, err)
	}

	execute(t, reopened, admin, "POST", "/health/debug", map[string]any{"duration_seconds": 600}, "")
	execute(t, reopened, admin, "POST", "/health/debug", map[string]any{"duration_seconds": 0}, "")
	if restartedDebug.Level() != slog.LevelInfo {
		t.Fatal("explicit audited disable did not end Debug")
	}
}

func assertDebugAudit(t *testing.T, r *Repository, lease runtimehealth.DebugLease, grant string) {
	t.Helper()
	err := r.store.Read(testContext, func(ctx context.Context, conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, "SELECT document FROM evidence_records WHERE collection='audit' AND job_id=?", lease.JobID)
		if err != nil {
			return err
		}
		defer rows.Close()
		events := map[string]int{}
		for rows.Next() {
			var document []byte
			var record evidence.Record
			if err := rows.Scan(&document); err != nil {
				return err
			}
			if err := json.Unmarshal(document, &record); err != nil {
				return err
			}
			if strings.Contains(string(document), grant) || record.Job != lease.JobID ||
				record.Correlation != lease.Correlation || record.Actor != lease.Principal ||
				record.Credential != lease.Credential || record.RequestID != lease.RequestID {
				t.Error("audit lacked safe original context")
			}
			events[record.Result]++
		}
		if len(events) != 2 || events["debug-lease-authorized-300s"] != 1 || events["debug-lease-expired"] != 1 {
			t.Error("missing or repeated Debug audit", events)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
