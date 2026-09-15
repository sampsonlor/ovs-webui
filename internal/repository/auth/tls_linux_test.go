//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tlsCommand(t *testing.T, r *Repository, path, etag string, d *tlscontrol.Descriptor) tlscontrol.Command {
	t.Helper()
	c := request(t, r, "POST", path, map[string]any{}, etag)
	return tlscontrol.Command{Method: c.Method, URI: c.URI, Epoch: c.Epoch, RequestID: c.RequestID, Precondition: c.Precondition, Candidate: d}
}
func candidate(t *testing.T, r *Repository, c authn.LoginResult) string {
	t.Helper()
	d := &tlscontrol.Descriptor{ID: repository.NewID(), Hostname: "console.example", Fingerprint: strings.Repeat("a", 64), InputDigest: strings.Repeat("b", 64), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	cmd := tlsCommand(t, r, "/certificates", "", d)
	out, err := r.ExecuteTLS(testContext, c.Grant, cmd)
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.ExecuteTLS(testContext, c.Grant, cmd)
	if err != nil || !again.Replayed || again.Receipt.Job.ID != out.Receipt.Job.ID {
		t.Fatal("candidate replay failed", err)
	}
	return d.ID
}
func tlsETag(t *testing.T, r *Repository, c authn.LoginResult, id string) string {
	return `"` + read(t, r, c, "/certificates/"+id)["revision"].(string) + `"`
}

func TestTLSActivationRequiresElevationFreshProofAndDurableConfirmation(t *testing.T) {
	r, _ := fixture(t)
	c := login(t, r, "admin", true)
	id := candidate(t, r, c)
	cmd := tlsCommand(t, r, "/certificates/"+id+"/activations", tlsETag(t, r, c, id), nil)
	out, err := r.ExecuteTLS(testContext, c.Grant, cmd)
	if err != nil || out.Receipt.State == "completed" {
		t.Fatal("activation was not pending", err)
	}
	again, err := r.ExecuteTLS(testContext, c.Grant, cmd)
	if err != nil || !again.Replayed || again.Receipt.Job.ID != out.Receipt.Job.ID {
		t.Fatal("activation replay", err)
	}
	confirm := tlsCommand(t, r, "/certificates/"+id+"/confirmations", tlsETag(t, r, c, id), nil)
	if _, err = r.ExecuteTLS(testContext, c.Grant, confirm); err == nil {
		t.Fatal("confirmation without real connection proof")
	}
	confirm.ServedID = id
	if _, err = r.ExecuteTLS(testContext, c.Grant, confirm); err != nil {
		t.Fatal(err)
	}
	state, err := r.TLSState(testContext)
	if err != nil || state.ActiveID != id || state.TrialID != "" {
		t.Fatal("confirmation not durable", err)
	}
	job := read(t, r, c, "/jobs/"+out.Receipt.Job.ID)
	if job["state"] != "succeeded" || job["sequence"] != "2" {
		t.Fatal("job evidence stale", job)
	}
	replay, err := r.ExecuteTLS(testContext, c.Grant, confirm)
	if err != nil || !replay.Replayed {
		t.Fatal("confirmation replay failed", err)
	}
	if err = r.RevokeAuth(testContext, c.Grant); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ExecuteTLS(testContext, c.Grant, cmd); err == nil {
		t.Fatal("revoked grant replayed TLS mutation")
	}
}
func TestTLSDeadlineSurvivesRestartAndBootChangeWithFailureEvidence(t *testing.T) {
	r, o := fixture(t)
	c := login(t, r, "admin", true)
	id := candidate(t, r, c)
	cmd := tlsCommand(t, r, "/certificates/"+id+"/activations", tlsETag(t, r, c, id), nil)
	out, err := r.ExecuteTLS(testContext, c.Grant, cmd)
	if err != nil {
		t.Fatal(err)
	}
	original, err := r.TLSState(testContext)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.store.Close()
	r = openFixture(t, o)
	restored, err := r.TLSState(testContext)
	if err != nil || restored != original {
		t.Fatal("restart renewed lease", err)
	}
	r.tlsNow = func() tlscontrol.Clock {
		return tlscontrol.Clock{BootID: "different-boot", NS: original.StartedNS, Wall: time.Now()}
	}
	if read(t, r, c, "/certificates/"+id)["state"] != "rolled-back" {
		t.Fatal("first read exposed expired trial")
	}
	rolled, err := r.TLSState(testContext)
	if err != nil || rolled.TrialID != "" || rolled.ActiveID != "" {
		t.Fatal("boot recovery failed", err)
	}
	job := read(t, r, c, "/jobs/"+out.Receipt.Job.ID)
	if job["state"] != "failed" || job["sequence"] != "2" {
		t.Fatal("rollback evidence missing", job)
	}
	var receipt []byte
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT receipt FROM api_receipts WHERE epoch=? AND request_id=?", cmd.Epoch, cmd.RequestID).Scan(&receipt)
	}); err != nil {
		t.Fatal(err)
	}
	var doc apitypes.Receipt
	_ = json.Unmarshal(receipt, &doc)
	if doc.State != "completed" {
		t.Fatal("rollback receipt remained pending")
	}
}
func TestVerifiedAuthBackupRestoreRotatesEpochAndRevokesOldContexts(t *testing.T) {
	r, o := fixture(t)
	c := login(t, r, "admin", true)
	tokenCommand := request(t, r, "POST", "/tokens", map[string]any{"name": "restore-test", "scopes": []string{"state.read"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "")
	token, err := r.ExecuteAuth(testContext, c.Grant, tokenCommand)
	if err != nil {
		t.Fatal(err)
	}
	var issued map[string]any
	_ = json.Unmarshal(token.Body, &issued)
	snapshot, err := r.store.Backup(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlite.VerifyBackup(testContext, snapshot, repository.Manager); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(snapshot, "database.sqlite"))
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	o.Path = filepath.Join(root, "manager.db")
	if err = os.WriteFile(o.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored := openFixture(t, o)
	if err = restored.PrepareRestore(testContext); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.InspectAuth(testContext, c.Grant); err == nil {
		t.Fatal("restored grant valid")
	}
	// Existing token body is the dedicated one-time response, never its receipt.
	if raw, ok := issued["secret"].(string); ok {
		if _, err = restored.InspectAuth(testContext, raw); err == nil {
			t.Fatal("restored token valid")
		}
	} else {
		t.Fatal("missing test token")
	}
	fresh := login(t, restored, "admin", false)
	if fresh.Claims.Epoch == c.Claims.Epoch || fresh.Claims.RequestEpoch == c.Claims.RequestEpoch {
		t.Fatal("restore reused authority epoch")
	}
	id := candidate(t, r, c)
	cmd := tlsCommand(t, restored, "/certificates/"+id+"/activations", `"stale"`, nil)
	if _, err = restored.ExecuteTLS(testContext, fresh.Grant, cmd); err == nil {
		t.Fatal("unelevated restore session activated cert")
	}
}
