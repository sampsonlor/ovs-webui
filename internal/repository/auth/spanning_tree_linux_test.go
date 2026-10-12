//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

func treePlan(t *testing.T, r *Repository, g authn.LoginResult) (plan.Envelope, *planProvider, plan.Intent) {
	t.Helper()
	e, p, _ := planSetup(t, r, g)
	b := plan.Binding{Table: "Bridge", ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Generation: p.snapshot.Generation}
	params := map[string]*string{}
	for _, k := range plan.SpanningTreeBasicKeys {
		params[k] = nil
	}
	p.snapshot.SpanningTrees = map[string]plan.SpanningTreeBridge{b.ManagementID: {Binding: b, Configuration: plan.SpanningTreeConfig{Parameters: params}, Known: true, Supported: true, Authority: "local-managed", Dependency: "private-graph"}}
	i := plan.Intent{ID: repository.NewID(), Operation: plan.SpanningTreePrepare, Object: b, SpanningTree: &plan.SpanningTreeRequest{Protocol: "rstp", Parameters: map[string]*string{"rstp-priority": planPointer("4096")}}}
	return e, p, i
}

func TestSpanningTreeFormalDraftNeverBecomesExecutableWithSafetyEnabled(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", true)
	provider := &safeTestProvider{}
	clock := tlscontrol.Now()
	safeConfigure(t, r, provider, &safeTestProbe{}, &clock)
	if !r.SafetyAvailable() {
		t.Fatal("test requires the actual configured safety admission service")
	}
	e, p, i := treePlan(t, r, g)
	e = preparePlan(t, r, g.Grant, e, i)
	if e.Verify(r.key, g.Claims.PrincipalID) != nil || e.Candidate.Intents[0].SpanningTree.Before.Parameters["rstp-priority"] != nil {
		t.Fatal("manager did not seal a distinct native original")
	}
	in := validationRequest(e, g.Claims.RequestEpoch)
	ack, err := r.ValidateCandidate(testContext, g.Grant, in)
	if err != nil {
		t.Fatal(err)
	}
	v := readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID)
	if v.State != "blocked" || v.Usable || v.ExecutionReady || len(v.Diff) != 9 || !slices.ContainsFunc(v.Checks, func(c plan.Gate) bool { return c.Code == "SPANNING_TREE_EXECUTION_PENDING" && c.State == "blocked" }) {
		t.Fatal("draft acquired execution authority", v)
	}
	replay, err := r.ValidateCandidate(testContext, g.Grant, in)
	if err != nil || !replay.Replayed || replay.Receipt.Job.ID != ack.Receipt.Job.ID {
		t.Fatal("review receipt was not durable/idempotent", replay, err)
	}
	id := planRequestID()
	body, _ := json.Marshal(map[string]any{"request_id": id, "candidate_id": e.Candidate.ID, "candidate_revision": e.Candidate.Revision, "validation_id": v.ID, "mode": "safe-apply", "reason": "synthetic draft boundary"})
	_, err = r.AdmitSafeApply(testContext, g.Grant, safety.Request{Envelope: e, Reservation: repository.NewID(), Command: authn.Command{Method: "POST", URI: "/api/v1/transactions", Epoch: g.Claims.RequestEpoch, RequestID: id, Payload: body}})
	if err == nil || provider.sends.Load() != 0 {
		t.Fatal("blocked draft reached execution", err)
	}
	p.snapshot.Policy = "revoked-root-grant"
	bridge := p.snapshot.SpanningTrees[i.Object.ManagementID]
	bridge.Authority = "unknown"
	p.snapshot.SpanningTrees[i.Object.ManagementID] = bridge
	v = readPlanValidation(t, r, g.Grant, e, v.ID)
	if v.Usable || v.ExecutionReady || !slices.ContainsFunc(v.Invalidations, func(c plan.Gate) bool { return c.Code == "AUTHORITY_POLICY_CHANGED" }) {
		t.Fatal("old review retained revoked root authority", v)
	}
}

func TestSpanningTreePreparationRequiresCurrentCapabilityAndCredentialCeiling(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, _, i := treePlan(t, r, g)
	setCaps := func(caps []string) {
		t.Helper()
		body, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, g.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(credential string) error {
		id := planRequestID()
		body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []plan.Intent{i}})
		_, err := r.PrepareCandidate(testContext, credential, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
		return err
	}
	setCaps(slices.DeleteFunc(slices.Clone(g.Claims.Capabilities), func(c string) bool { return c == plan.SpanningTreePrepareCapability }))
	wantCode(t, prepare(g.Grant), "CAPABILITY_DENIED")
	limited := login(t, r, "admin", false)
	setCaps(g.Claims.Capabilities)
	wantCode(t, prepare(limited.Grant), "CAPABILITY_DENIED")
	if err := prepare(g.Grant); err != nil {
		t.Fatal("current independent permission denied", err)
	}
}
