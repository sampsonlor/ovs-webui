//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type treeWriteProvider struct {
	*safeTestProvider
	bindings []plan.Binding
	root     string
}

func (p *treeWriteProvider) Prepare(ctx context.Context, id, marker string, in plan.Envelope) (execution.Plan, error) {
	if err := execution.CheckAdmission(ctx); err != nil {
		return execution.Plan{}, err
	}
	out, err := p.safeTestProvider.Prepare(ctx, id, marker, in)
	out.GraphBindings = p.bindings
	out.Root = p.root
	return out, err
}
func (p *treeWriteProvider) PrepareRollback(ctx context.Context, in execution.Plan, marker string) (execution.Plan, error) {
	if err := execution.CheckAdmission(ctx); err != nil {
		return execution.Plan{}, err
	}
	return p.safeTestProvider.PrepareRollback(ctx, in, marker)
}
func (p *treeWriteProvider) Commit(ctx context.Context, in execution.Plan, before func() error) execution.Outcome {
	if err := execution.CheckAdmission(ctx); err != nil {
		return execution.Outcome{Commit: "rejected", Applied: "not-applied", Reason: "admission-denied"}
	}
	return p.safeTestProvider.Commit(ctx, in, before)
}

func TestSpanningTreeWriteKeepsSeparateCapabilityAndCredentialCeiling(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, provider, i := treePlan(t, r, g)
	i.Operation = plan.SpanningTreeConfigure
	p := provider.snapshot.SpanningTrees[i.Object.ManagementID]
	p.Executable = true
	provider.snapshot.SpanningTrees[i.Object.ManagementID] = p
	stage := func(credential string) error {
		_, err := r.PrepareCandidate(testContext, credential, treeStageRequest(e, i))
		return err
	}
	wantCode(t, stage(g.Grant), "CAPABILITY_DENIED")
	role := provisionTreeRole(t, r, g)
	if err := r.SetSpanningTreeExecutionRole(testContext, role, true); err != nil {
		t.Fatal(err)
	}
	wantCode(t, stage(g.Grant), "CAPABILITY_DENIED")
	fresh := login(t, r, "admin", false)
	e = prepareTreePlan(t, r, fresh.Grant, e, i)
	if e.Candidate.Intents[0].Operation != plan.SpanningTreeConfigure || e.Verify(r.key, fresh.Claims.PrincipalID) != nil {
		t.Fatal("execution did not receive an independent sealed intent")
	}
	if err := r.SetSpanningTreeExecutionRole(testContext, role, false); err != nil {
		t.Fatal(err)
	}
	wantCode(t, stage(fresh.Grant), "CAPABILITY_DENIED")
}

func provisionTreeRole(t *testing.T, r *Repository, g authn.LoginResult) string {
	t.Helper()
	var role string
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", g.Claims.PrincipalID).Scan(&role)
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetSpanningTreeExecutionRole(testContext, role, true); err != nil {
		t.Fatal(err)
	}
	return role
}

func TestSpanningTreeSafeApplyReservesMembersAndRecoversWithoutReachability(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	provisionTreeRole(t, r, g)
	g = login(t, r, "admin", false)
	env, source, i := treePlan(t, r, g)
	i.Operation = plan.SpanningTreeConfigure
	tree := source.snapshot.SpanningTrees[i.Object.ManagementID]
	tree.Executable = true
	source.snapshot.SpanningTrees[i.Object.ManagementID] = tree
	env = prepareTreePlan(t, r, g.Grant, env, i)
	v, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(env, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	p := &treeWriteProvider{safeTestProvider: &safeTestProvider{}, root: repository.NewID(), bindings: []plan.Binding{i.Object, {Table: "Port", ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Generation: i.Object.Generation}, {Table: "Interface", ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Generation: i.Object.Generation}}}
	p.applied.Store(true)
	engine, err := r.ConfigureExecution(p)
	if err != nil {
		t.Fatal(err)
	}
	clock := tlscontrol.Now()
	probe := &safeTestProbe{}
	base := clock
	if err = r.ConfigureSafety(executions.SafetyOptions{Probe: probe, Clock: func() tlscontrol.Clock {
		current := tlscontrol.Now()
		current.NS += clock.NS - base.NS
		current.Wall = current.Wall.Add(clock.Wall.Sub(base.Wall))
		return current
	}}); err != nil {
		t.Fatal(err)
	}
	id := planRequestID()
	payload, _ := json.Marshal(map[string]any{"request_id": id, "candidate_id": env.Candidate.ID, "candidate_revision": env.Candidate.Revision, "validation_id": v.Receipt.Resource.ID, "mode": "safe-apply", "reason": "synthetic tree execution"})
	in := safety.Request{Envelope: env, Reservation: repository.NewID(), Command: authn.Command{Method: "POST", URI: "/api/v1/transactions", RequestID: id, Epoch: g.Claims.RequestEpoch, Payload: payload}}
	tx := safeAdmit(t, r, engine, g, in)
	if s := safeTestState(t, r, tx); s.State != "awaiting-confirmation" {
		t.Fatal("dedicated field capability failed safety settlement", s)
	}
	var protections int
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", tx).Scan(&protections)
	}); err != nil || protections != 4 {
		t.Fatal("graph protection incomplete", protections, err)
	}
	// Reachability loss must start compensation, never prohibit its admission.
	probe.failed.Store(true)
	for n := 0; n < 3; n++ {
		safeTick(t, engine)
	}
	if s := safeTestState(t, r, tx); s.State != "rolling-back" {
		t.Fatal("management loss prevented compensation", s)
	}
	if p.sends.Load() != 2 {
		t.Fatal("compensation did not dispatch exactly once", p.sends.Load())
	}
	for n := 0; n < 3; n++ {
		safeTick(t, engine)
	}
	if p.sends.Load() != 2 {
		t.Fatal("watchdog replayed compensation", p.sends.Load())
	}
}
