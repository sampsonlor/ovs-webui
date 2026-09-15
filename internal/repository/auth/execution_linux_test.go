//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
)

type executionProvider struct {
	prepares, sends int
	before          func()
	after           func()
	outcome         execution.Outcome
	observed        execution.Outcome
}

func (p *executionProvider) Prepare(ctx context.Context, id, marker string, e plan.Envelope) (execution.Plan, error) {
	p.prepares++
	return execution.Plan{ID: id, Envelope: e, Generation: *e.Candidate.Generation, Marker: marker, Native: json.RawMessage(`{}`), Prepared: time.Now()}, nil
}
func (p *executionProvider) Commit(ctx context.Context, plan execution.Plan, before func() error) execution.Outcome {
	if p.before != nil {
		p.before()
	}
	if err := before(); err != nil {
		return execution.Outcome{Commit: "rejected", Applied: "not-applied", Reason: "admission-denied"}
	}
	p.sends++
	if p.after != nil {
		p.after()
	}
	return p.outcome
}
func (p *executionProvider) Observe(context.Context, execution.Plan, execution.Outcome) execution.Outcome {
	return p.observed
}

type executionLease func(context.Context, execution.Request) error

func (f executionLease) Check(ctx context.Context, in execution.Request) error { return f(ctx, in) }

var isolatedLease execution.Lease = executionLease(func(context.Context, execution.Request) error { return nil })

func executionRequest(t *testing.T, r *Repository, g authn.LoginResult) (execution.Request, *planProvider) {
	e, p, i := planSetup(t, r, g)
	e = preparePlan(t, r, g.Grant, e, i)
	v, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	return execution.Request{ID: planRequestID(), ValidationID: v.Receipt.Resource.ID, Envelope: e}, p
}
func TestExecutionRequiresSafetyAndCurrentCredentialAtDispatch(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	in, _ := executionRequest(t, r, g)
	p := &executionProvider{}
	engine, err := r.ConfigureExecution(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Submit(testContext, in, r.ExecutionAuthorizer(g.Grant), nil)
	wantCode(t, err, "SAFE_APPLY_REQUIRED")
	if p.prepares != 0 || p.sends != 0 {
		t.Fatal("ungated provider call")
	}
	p.before = func() {
		if err := r.RevokeAuth(testContext, g.Grant); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.Submit(testContext, in, r.ExecutionAuthorizer(g.Grant), isolatedLease)
	if err != nil {
		t.Fatal(err)
	}
	record, err := engine.Read(testContext, out.Receipt.Resource.ID)
	if err != nil || record.State != "failed" || record.Outcome.Commit != "rejected" || p.sends != 0 {
		t.Fatal(record, err, p.sends)
	}
	if _, err = engine.Submit(testContext, in, r.ExecutionAuthorizer(g.Grant), isolatedLease); err == nil {
		t.Fatal("revoked credential replayed receipt")
	}
}
func TestExecutionReceiptRecoveryProtectionAndOutcomeSeparation(t *testing.T) {
	r, o := fixture(t)
	g := login(t, r, "admin", false)
	in, snapshot := executionRequest(t, r, g)
	p := &executionProvider{outcome: execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: "reply-lost"}, observed: execution.Outcome{Commit: "committed", Applied: "unknown", Reason: "applied-target-unavailable"}}
	engine, err := r.ConfigureExecution(p)
	if err != nil {
		t.Fatal(err)
	}
	out, err := engine.Submit(testContext, in, r.ExecutionAuthorizer(g.Grant), isolatedLease)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Receipt.Resource.ID
	if p.sends != 1 {
		t.Fatal(p.sends)
	}
	if err = r.store.Close(); err != nil {
		t.Fatal(err)
	}
	r = openFixture(t, o)
	r.WithInventory(snapshot)
	engine, err = r.ConfigureExecution(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.InitializeEvidence(testContext); err != nil {
		t.Fatal(err)
	}
	if err = engine.Recover(testContext); err != nil {
		t.Fatal(err)
	}
	record, err := engine.Read(testContext, id)
	if err != nil || record.State != "recovery-required" || record.Outcome.Commit != "committed" || record.Outcome.Applied != "unknown" || record.Outcome.Target != nil || p.sends != 1 {
		t.Fatal(record, err)
	}
	prior, err := engine.Submit(testContext, in, r.ExecutionAuthorizer(g.Grant), nil)
	if err != nil || !prior.Replayed || prior.Receipt.Resource.ID != id || p.sends != 1 {
		t.Fatal(prior, err)
	}
	second := in
	second.ID = planRequestID()
	_, err = engine.Submit(testContext, second, r.ExecutionAuthorizer(g.Grant), isolatedLease)
	wantCode(t, err, "FIELD_PROTECTED")
	view := read(t, r, g, "/transactions/"+id)
	if view["commit_outcome"] != "committed" || view["applied_outcome"] != "unknown" || view["health"] != "unknown" || view["safe_apply"] != "recovery-required" {
		t.Fatal(view)
	}
	list := read(t, r, g, "/transactions?limit=1")
	if len(list["items"].([]any)) != 1 {
		t.Fatal(list)
	}
	serialized, _ := json.Marshal(view)
	if strings.Contains(string(serialized), record.Plan.Marker) || strings.Contains(string(serialized), g.Grant) {
		t.Fatal("private dispatch data leaked")
	}
	beforeSends := p.sends
	reconcile := request(t, r, "POST", "/transactions/"+id+"/reconciliations", map[string]any{}, "")
	ack, err := r.ExecuteAuth(testContext, g.Grant, reconcile)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ExecuteAuth(testContext, g.Grant, reconcile)
	if err != nil || !replay.Replayed || p.sends != beforeSends || ack.Receipt.Job.ID != replay.Receipt.Job.ID {
		t.Fatal(err, replay)
	}
	if err = evidence.Prune(testContext, r.store, time.Now().Add(200*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		var count int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", id).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("unsettled protection pruned")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestExecutionRechecksValidationScopeAndWorkspaceWitness(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	in, snapshot := executionRequest(t, r, g)
	a := r.ExecutionAuthorizer(g.Grant)
	check := func(in execution.Request) error {
		return r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { _, err := a(ctx, q, in, false); return err })
	}
	if err := check(in); err != nil {
		t.Fatal(err)
	}
	copy := in
	copy.ValidationID = repository.NewID()
	wantCode(t, check(copy), "NOT_FOUND")
	snapshot.snapshot.Policy = "changed"
	wantCode(t, check(in), "AUTHORITY_POLICY_CHANGED")
	snapshot.snapshot.Policy = "reviewed-authority"
	if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE candidate_witnesses SET sequence=sequence+1 WHERE owner_id=?", g.Claims.PrincipalID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, check(in), "WORKSPACE_WITNESS_CHANGED")
}
