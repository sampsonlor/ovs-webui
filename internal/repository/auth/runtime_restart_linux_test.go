//go:build linux

package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/runtimeops"
	"github.com/sampsonlor/ovs-webui/internal/runtimecontrol"
)

type runtimeRestartFixture struct {
	state runtimecontrol.UnitState
	sends int
	last  runtimecontrol.Target
	err   error
}

func (f *runtimeRestartFixture) Inspect(context.Context, runtimecontrol.Target) (runtimecontrol.UnitState, error) {
	return f.state, nil
}

func (f *runtimeRestartFixture) TryRestart(_ context.Context, target runtimecontrol.Target) (string, error) {
	f.sends++
	f.last = target
	f.state.JobPending = true
	return "/org/freedesktop/systemd1/job/45", f.err
}

func TestRuntimeRestartUsesElevatedOnceOnlyAdmissionAndDurableFence(t *testing.T) {
	r, _ := fixture(t)
	supervisor := &runtimeRestartFixture{state: runtimecontrol.UnitState{InvocationID: "synthetic-first-invocation", MainPID: 24, Active: true}}
	r.WithRuntimeRestart(supervisor, true, "synthetic-boot")
	ordinary := login(t, r, "admin", false)
	command := request(t, r, "POST", "/health/service-restarts", map[string]any{"service": "web"}, "")
	_, err := r.ExecuteAuth(testContext, ordinary.Grant, command)
	wantCode(t, err, "REAUTHENTICATION_REQUIRED")
	if supervisor.sends != 0 {
		t.Fatal("ordinary management grant sent a restart")
	}

	admin := login(t, r, "admin", true)
	accepted, err := r.ExecuteAuth(testContext, admin.Grant, command)
	if err != nil || accepted.Receipt.Job == nil || supervisor.sends != 1 || supervisor.last != runtimecontrol.TargetWeb {
		t.Fatalf("expected one typed web restart with a durable job: %+v %v", accepted, err)
	}
	requireFence := func() {
		t.Helper()
		err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return runtimeops.RequireNoRestart(ctx, q) })
		wantCode(t, err, "MANAGEMENT_RESTART_IN_PROGRESS")
	}
	requireFence()
	other := request(t, r, "POST", "/health/service-restarts", map[string]any{"service": "manager"}, "")
	_, err = r.ExecuteAuth(testContext, admin.Grant, other)
	wantCode(t, err, "MANAGEMENT_RESTART_IN_PROGRESS")
	r.reconcileRuntimeRestart(testContext)
	requireFence()

	supervisor.state.JobPending = false
	supervisor.state.InvocationID = "synthetic-next-invocation"
	r.reconcileRuntimeRestart(testContext)
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		if err := runtimeops.RequireNoRestart(ctx, q); err != nil {
			return err
		}
		job, err := evidence.LoadJob(ctx, q, accepted.Receipt.Job.ID)
		if err == nil && (job.Business != "unknown" || job.Reason != "restart-new-invocation-observed") {
			t.Errorf("an invocation change was promoted to a causal success: %+v", job)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := r.ExecuteAuth(testContext, admin.Grant, command)
	if err != nil || replay.Receipt.Job == nil || replay.Receipt.Job.ID != accepted.Receipt.Job.ID || supervisor.sends != 1 {
		t.Fatal("a replay replaced the original job or resent the restart", err)
	}

	supervisor.err = &runtimecontrol.SystemdError{Code: "SYSTEMD_REPLY_UNPROVEN", OutcomeUnknown: true}
	next := request(t, r, "POST", "/health/service-restarts", map[string]any{"service": "manager"}, "")
	if _, err := r.ExecuteAuth(testContext, admin.Grant, next); err != nil {
		t.Fatal(err)
	}
	supervisor.state.JobPending = false
	r.reconcileRuntimeRestart(testContext)
	requireFence()
	if _, err := r.ExecuteAuth(testContext, admin.Grant, next); err != nil || supervisor.sends != 2 {
		t.Fatal("ambiguous dispatch was resent after losing its reply", err)
	}
	r.WithRuntimeRestart(supervisor, true, "synthetic-boot")
	r.reconcileRuntimeRestart(testContext)
	requireFence()
	r.WithRuntimeRestart(supervisor, true, "synthetic-next-boot")
	r.reconcileRuntimeRestart(testContext)
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return runtimeops.RequireNoRestart(ctx, q)
	}); err != nil {
		t.Fatal("a previous-boot dispatch retained an active fence", err)
	}
	if _, err := r.ExecuteAuth(testContext, admin.Grant, next); err != nil || supervisor.sends != 2 {
		t.Fatal("previous-boot reconciliation resent the original restart", err)
	}
}

func TestRuntimeRestartAdmissionRejectsAnActiveTLSTrial(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	supervisor := &runtimeRestartFixture{}
	r.WithRuntimeRestart(supervisor, true, "synthetic-boot")
	if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE tls_authority SET trial_id='synthetic-tls-trial' WHERE singleton=1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	in := request(t, r, "POST", "/health/service-restarts", map[string]any{"service": "manager"}, "")
	_, err := r.ExecuteAuth(testContext, admin.Grant, in)
	wantCode(t, err, "TLS_RECOVERY_IN_PROGRESS")
	if supervisor.sends != 0 {
		t.Fatal("TLS trial allowed a service restart")
	}
}
