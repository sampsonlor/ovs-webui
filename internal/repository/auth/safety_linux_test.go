//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type safeTestProvider struct {
	sends    atomic.Int32
	applied  atomic.Bool
	conflict atomic.Bool
	unknown  atomic.Bool
}

func (p *safeTestProvider) Prepare(_ context.Context, id, marker string, e plan.Envelope) (execution.Plan, error) {
	return execution.Plan{ID: id, Envelope: e, Generation: *e.Candidate.Generation, Marker: marker, Native: json.RawMessage(`{}`), Prepared: time.Now()}, nil
}
func (p *safeTestProvider) PrepareRollback(_ context.Context, original execution.Plan, marker string) (execution.Plan, error) {
	if p.conflict.Load() {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	original.Marker = "rollback"
	return original, nil
}
func (p *safeTestProvider) Commit(_ context.Context, plan execution.Plan, before func() error) execution.Outcome {
	if before() != nil {
		return execution.Outcome{Commit: "rejected", Applied: "not-applied", Reason: "dispatch-denied"}
	}
	p.sends.Add(1)
	if p.unknown.Load() {
		return execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: "lost-reply"}
	}
	n := "10"
	return execution.Outcome{Commit: "committed", Applied: "pending", Reason: "committed", Target: &n}
}
func (p *safeTestProvider) Observe(_ context.Context, plan execution.Plan, prior execution.Outcome) execution.Outcome {
	if p.unknown.Load() {
		return prior
	}
	prior.Commit = "committed"
	prior.Applied = "pending"
	prior.Reason = "applied-pending"
	if p.applied.Load() || plan.Marker == "rollback" {
		prior.Applied = "applied"
		prior.Reason = "applied"
		now := time.Now()
		prior.Observed = &now
	}
	return prior
}

type safeTestProbe struct{ failed atomic.Bool }

func (p *safeTestProbe) Domain() string { return "synthetic-management-path" }
func (p *safeTestProbe) Check(context.Context) error {
	if p.failed.Load() {
		return apitypes.Fail(503, "PROBE_FAILED")
	}
	return nil
}

func safeTestRequest(t *testing.T, r *Repository, g authn.LoginResult) safety.Request {
	req, _ := executionRequest(t, r, g)
	body, _ := json.Marshal(map[string]any{"request_id": req.ID, "candidate_id": req.Envelope.Candidate.ID, "candidate_revision": req.Envelope.Candidate.Revision, "validation_id": req.ValidationID, "mode": "safe-apply", "reason": "synthetic safety regression"})
	return safety.Request{Envelope: req.Envelope, Reservation: repository.NewID(), Command: authn.Command{Method: "POST", URI: "/api/v1/transactions", Epoch: g.Claims.RequestEpoch, RequestID: req.ID, Payload: body}}
}
func safeTestState(t *testing.T, r *Repository, id string) safety.Record {
	t.Helper()
	var s safety.Record
	err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		var b []byte
		if err := q.QueryRowContext(ctx, "SELECT document FROM safe_applies WHERE id=?", id).Scan(&b); err != nil {
			return err
		}
		return json.Unmarshal(b, &s)
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func safeWait(t *testing.T, e *executions.Engine, id string) execution.Record {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		r, err := e.Read(testContext, id)
		if err == nil && r.State != "admitted" && r.State != "committing" {
			time.Sleep(10 * time.Millisecond)
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("asynchronous field worker did not finish")
	return execution.Record{}
}
func safeConfigure(t *testing.T, r *Repository, p *safeTestProvider, probe *safeTestProbe, clock *tlscontrol.Clock) *executions.Engine {
	t.Helper()
	e, err := r.ConfigureExecution(p)
	if err != nil {
		t.Fatal(err)
	}
	base := *clock
	if err = r.ConfigureSafety(executions.SafetyOptions{Probe: probe, Clock: func() tlscontrol.Clock {
		current := tlscontrol.Now()
		current.BootID = clock.BootID
		current.NS += clock.NS - base.NS
		current.Wall = current.Wall.Add(clock.Wall.Sub(base.Wall))
		return current
	}}); err != nil {
		t.Fatal(err)
	}
	return e
}
func safeAdmit(t *testing.T, r *Repository, e *executions.Engine, g authn.LoginResult, in safety.Request) string {
	t.Helper()
	out, err := r.AdmitSafeApply(testContext, g.Grant, in)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Receipt.Resource.ID
	safeWait(t, e, id)
	return id
}
func safeTick(t *testing.T, e *executions.Engine) {
	t.Helper()
	if err := e.SafetyTick(testContext); err != nil {
		t.Fatal(err)
	}
}

func TestSafeApplyAppliedGateConfirmAndSingleLKG(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	in := safeTestRequest(t, r, g)
	p, probe := &safeTestProvider{}, &safeTestProbe{}
	clock := tlscontrol.Now()
	e := safeConfigure(t, r, p, probe, &clock)
	id := safeAdmit(t, r, e, g, in)
	safeTick(t, e)
	if s := safeTestState(t, r, id); s.Confirmation != nil || s.HealthyAt != nil {
		t.Fatal("commit opened confirmation before Applied", s)
	}
	p.applied.Store(true)
	if err := e.Reconcile(testContext, id); err != nil {
		t.Fatal(err)
	}
	safeTick(t, e)
	s := safeTestState(t, r, id)
	if s.State != "awaiting-confirmation" || s.Confirmation == nil || s.Confirmation.Wall.Sub(s.Confirmation.Started) != safety.ConfirmationWindow {
		t.Fatal(s)
	}
	var protection int
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", id).Scan(&protection)
	}); err != nil || protection != 1 {
		t.Fatal("protection ended at Applied", err, protection)
	}
	record, _ := e.Read(testContext, id)
	cmd := request(t, r, "POST", "/transactions/"+id+"/decisions", map[string]any{"decision": "confirm", "expected_sequence": record.Sequence}, "")
	ack, err := r.ExecuteAuth(testContext, g.Grant, cmd)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ExecuteAuth(testContext, g.Grant, cmd)
	if err != nil || !replay.Replayed || ack.Receipt.Resource.ID != id {
		t.Fatal(replay, err)
	}
	if s = safeTestState(t, r, id); s.State != "confirmed" {
		t.Fatal(s)
	}
	var lkg int
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM last_known_good WHERE transaction_id=?", id).Scan(&lkg); err != nil {
			return err
		}
		return q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", id).Scan(&protection)
	}); err != nil || lkg != 1 || protection != 0 {
		t.Fatal(lkg, protection, err)
	}
	resolved, err := r.ResolveSafeApply(testContext, g.Grant, in)
	if err != nil || resolved.Pending || resolved.Envelope == nil || len(resolved.Envelope.Candidate.Intents) != 0 || resolved.Envelope.Verify(r.key, g.Claims.PrincipalID) != nil {
		t.Fatal(resolved, err)
	}
	if !resolved.Confirmed || resolved.Envelope.Candidate.Consumed != nil || resolved.Envelope.Candidate.ID == in.Envelope.Candidate.ID {
		t.Fatal("confirmation did not create a fresh workspace", resolved)
	}
	newIntent := in.Envelope.Candidate.Intents[0]
	preparePlan(t, r, g.Grant, *resolved.Envelope, plan.Intent{ID: repository.NewID(), Operation: newIntent.Operation, Object: newIntent.Object, Value: newIntent.Value})
	resolved2, err := r.ResolveSafeApply(testContext, g.Grant, in)
	if err != nil || plan.Digest(resolved) != plan.Digest(resolved2) {
		t.Fatal("resolution is not replayable", err)
	}
	view := read(t, r, g, "/transactions/"+id)
	if view["safe_apply"] != "confirmed" {
		t.Fatal(view)
	}
}

func TestSafeApplyAuthorityDeadlineAndConflict(t *testing.T) {
	for _, scenario := range []string{"timeout", "revoked", "boot-changed", "wall-reversed", "conflict", "probe-loss", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			r, _ := fixture(t)
			g := login(t, r, "admin", false)
			in := safeTestRequest(t, r, g)
			p, probe := &safeTestProvider{}, &safeTestProbe{}
			p.applied.Store(true)
			clock := tlscontrol.Now()
			e := safeConfigure(t, r, p, probe, &clock)
			if scenario == "unknown" {
				p.unknown.Store(true)
			}
			id := safeAdmit(t, r, e, g, in)
			safeTick(t, e)
			switch scenario {
			case "revoked":
				if err := r.RevokeAuth(testContext, g.Grant); err != nil {
					t.Fatal(err)
				}
			case "boot-changed":
				clock.BootID = repository.NewID()
			case "wall-reversed":
				clock.Wall = clock.Wall.Add(-time.Second)
			case "probe-loss":
				probe.failed.Store(true)
			default:
				clock.NS += int64(121 * time.Second)
				clock.Wall = clock.Wall.Add(121 * time.Second)
			}
			if scenario == "conflict" {
				p.conflict.Store(true)
			}
			for i := 0; i < 4; i++ {
				safeTick(t, e)
			}
			s := safeTestState(t, r, id)
			if scenario == "conflict" {
				if s.State != "rollback-conflict" || p.sends.Load() != 1 {
					t.Fatal(s, p.sends.Load())
				}
			} else if scenario == "unknown" {
				if s.State != "recovery-required" || p.sends.Load() != 1 {
					t.Fatal(s, p.sends.Load())
				}
			} else if scenario == "probe-loss" {
				if s.State != "recovery-required" || p.sends.Load() != 2 {
					t.Fatal(s, p.sends.Load())
				}
				before, _ := e.Read(testContext, id)
				for i := 0; i < 3; i++ {
					safeTick(t, e)
				}
				after, _ := e.Read(testContext, id)
				if before.Sequence != after.Sequence {
					t.Fatal("unchanged recovery appended duplicate state transitions")
				}
				probe.failed.Store(false)
				safeTick(t, e)
				if safeTestState(t, r, id).State != "rolled-back" {
					t.Fatal("reachability recovery missing")
				}
			} else if s.State != "rolled-back" || p.sends.Load() != 2 {
				t.Fatal(s, p.sends.Load())
			}
			var protection int
			_ = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
				return q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", id).Scan(&protection)
			})
			if (scenario == "conflict" || scenario == "unknown") && protection != 1 {
				t.Fatal("unresolved protection released")
			}
		})
	}
}

func TestSafeApplyResolutionFencesLateAdmission(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	in := safeTestRequest(t, r, g)
	p, probe := &safeTestProvider{}, &safeTestProbe{}
	clock := tlscontrol.Now()
	safeConfigure(t, r, p, probe, &clock)
	out, err := r.ResolveSafeApply(testContext, g.Grant, in)
	if err != nil || out.Pending || out.Envelope == nil || len(out.Envelope.Candidate.Intents) != 1 {
		t.Fatal(out, err)
	}
	if _, err = r.AdmitSafeApply(testContext, g.Grant, in); err == nil || p.sends.Load() != 0 {
		t.Fatal("late dispatch after resolution", err)
	}
}
