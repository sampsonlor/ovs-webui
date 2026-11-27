package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/runtimeops"
	"github.com/sampsonlor/ovs-webui/internal/runtimecontrol"
)

const restartOperation = "restartManagementService"

type managementRestarter struct {
	supervisor runtimecontrol.Supervisor
	instance   string
	bootID     string
	enabled    bool
	dispatch   atomic.Bool
}

type runtimeRestartRecord struct {
	job, owner, phase, invocation, systemdJob, bootID string
	jobState                                          string
	target                                            runtimecontrol.Target
	updated                                           int64
}

func (r *Repository) WithRuntimeRestart(supervisor runtimecontrol.Supervisor, enabled bool, bootID string) *Repository {
	r.runtimeRestart = &managementRestarter{supervisor: supervisor, enabled: enabled, instance: repository.NewID(), bootID: bootID}
	return r
}

func (r *Repository) restartManagementService(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	op, body, err := r.candidateCommand(input, restartOperation)
	if err != nil {
		return apitypes.Result{}, err
	}
	var intent struct {
		Service runtimecontrol.Target `json:"service"`
	}
	if err := json.Unmarshal(body, &intent); err != nil ||
		intent.Service != runtimecontrol.TargetManager && intent.Service != runtimecontrol.TargetWeb {
		return apitypes.Result{}, apitypes.Fail(422, "INVALID_SERVICE_TARGET")
	}
	if r.runtimeRestart == nil {
		return apitypes.Result{}, apitypes.Fail(503, "MANAGEMENT_RESTART_UNAVAILABLE")
	}
	result, next, err := r.admitRuntimeRestart(ctx, credential, input, op, body, intent.Service)
	if err == nil && next.job != "" {
		// The console may disconnect as soon as the accepted web service
		// restarts. Its request context cannot cancel the durable dispatch
		// observation or the acknowledgement write.
		dispatch, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		r.dispatchRuntimeRestart(dispatch, credential, op, next)
	}
	return result, err
}

func (r *Repository) admitRuntimeRestart(ctx context.Context, credential string, input authn.Command, op *apicontract.Operation, body []byte, target runtimecontrol.Target) (apitypes.Result, runtimeRestartRecord, error) {
	unlock, err := r.lock(ctx)
	if err != nil {
		return apitypes.Result{}, runtimeRestartRecord{}, err
	}
	defer unlock()
	claims, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return apitypes.Result{}, runtimeRestartRecord{}, err
	}
	authorize := func(ctx context.Context, q querier) error {
		var err error
		claims, err = r.checkOperation(ctx, q, credential, op, nil, true)
		return err
	}
	control := r.runtimeRestart
	c := requests.Command{Principal: claims.PrincipalID, Credential: claims.CredentialID, Capability: op.Capability,
		Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID, Method: input.Method, URI: input.URI, Payload: body}
	var record runtimeRestartRecord
	out, err := r.receipts.Execute(ctx, c, func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, conn *sql.Conn) error { return authorize(ctx, conn) })
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		result := requests.Mutation{Status: 202, Body: json.RawMessage(`{}`)}
		if err := authorize(ctx, tx); err != nil {
			return result, err
		}
		if !control.enabled || control.supervisor == nil || control.bootID == "" {
			return result, apitypes.Fail(503, "MANAGEMENT_RESTART_UNAVAILABLE")
		}
		if err := runtimeops.RequireRestartAdmission(ctx, tx); err != nil {
			return result, err
		}
		now := r.now()
		id := repository.NewID()
		ref := &apitypes.Ref{Kind: "job", ID: id}
		if _, err := evidence.CreateJob(ctx, tx, evidence.Job{ID: id, Resource: ref, State: "queued", Reason: "restart-admitted", Created: now}); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO runtime_service_restart (singleton,job_id,target,owner_instance,boot_id,phase,updated_ms) VALUES(1,?,?,?,?,'prepared',?)", id, string(target), control.instance, control.bootID, now.UnixMilli()); err != nil {
			return result, err
		}
		result.Resource, result.Job = ref, ref
		if err := r.audit(ctx, tx, claims, op.ID, id, "restart-admitted", input.RequestID, ref, ref); err != nil {
			return result, err
		}
		if err := r.touch(ctx, tx, credential, claims); err != nil {
			return result, err
		}
		record = runtimeRestartRecord{job: id, owner: control.instance, phase: "prepared", target: target}
		return result, nil
	})
	return out, record, err
}

func (r *Repository) dispatchRuntimeRestart(ctx context.Context, credential string, op *apicontract.Operation, record runtimeRestartRecord) {
	control := r.runtimeRestart
	control.dispatch.Store(true)
	defer control.dispatch.Store(false)
	expected, err := control.supervisor.Inspect(ctx, record.target)
	if err != nil || !expected.Active || expected.JobPending || expected.InvocationID == "" || expected.MainPID <= 0 {
		_ = r.finishRuntimeRestart(ctx, record, "not-started", "restart-target-unproven")
		return
	}
	err = r.beginRuntimeRestartDispatch(ctx, credential, op, record, expected.InvocationID)
	if err != nil {
		return // The durable prepared record will be retired by maintenance.
	}
	record.phase, record.invocation = "dispatching", expected.InvocationID
	var job string
	if fenced, ok := control.supervisor.(runtimecontrol.FencedSupervisor); ok {
		job, err = fenced.TryRestartAfter(ctx, record.target, expected)
	} else {
		job, err = control.supervisor.TryRestart(ctx, record.target)
	}
	if err != nil {
		var failure *runtimecontrol.SystemdError
		if errors.As(err, &failure) && !failure.OutcomeUnknown {
			_ = r.finishRuntimeRestart(ctx, record, "not-started", "restart-rejected-before-queue")
		} else {
			_ = r.noteUncertainRestart(ctx, record.job)
		}
		return // Durable dispatching state retains protection on ambiguous outcomes.
	}
	if job != "" {
		_ = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE runtime_service_restart SET phase='submitted',systemd_job=?,updated_ms=? WHERE job_id=? AND phase='dispatching'", job, r.now().UnixMilli(), record.job)
			return err
		})
	}
}

func (r *Repository) beginRuntimeRestartDispatch(ctx context.Context, credential string, op *apicontract.Operation, record runtimeRestartRecord, invocation string) error {
	unlock, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := r.checkOperation(ctx, tx, credential, op, nil, true); err != nil {
			return err
		}
		now := r.now()
		result, err := tx.ExecContext(ctx, "UPDATE runtime_service_restart SET phase='dispatching',invocation_id=?,updated_ms=? WHERE job_id=? AND owner_instance=? AND phase='prepared'", invocation, now.UnixMilli(), record.job, record.owner)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return repository.ErrConflict
		}
		j, err := evidence.LoadJob(ctx, tx, record.job)
		if err != nil {
			return err
		}
		_, err = evidence.ChangeJob(ctx, tx, j.ID, j.Sequence, evidence.Transition{State: "running", Dispatch: "prepared", Reason: "restart-dispatch-recorded"}, now)
		return err
	})
}

// MaintainRuntimeRestarts only reconciles durable evidence. It never dispatches
// work, including after a daemon exits between the restart request and reply.
func (r *Repository) MaintainRuntimeRestarts(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			round, cancel := context.WithTimeout(ctx, 3*time.Second)
			r.reconcileRuntimeRestart(round)
			cancel()
		}
	}
}

func (r *Repository) reconcileRuntimeRestart(ctx context.Context) {
	control := r.runtimeRestart
	if control == nil || control.dispatch.Load() {
		return
	}
	var row runtimeRestartRecord
	err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT r.job_id,r.owner_instance,r.phase,r.invocation_id,r.systemd_job,r.boot_id,r.target,r.updated_ms,j.state FROM runtime_service_restart r JOIN jobs j ON j.id=r.job_id WHERE r.singleton=1").Scan(&row.job, &row.owner, &row.phase, &row.invocation, &row.systemdJob, &row.bootID, &row.target, &row.updated, &row.jobState)
	})
	if err != nil {
		return
	}
	if row.phase == "prepared" {
		if row.owner != control.instance || r.now().UnixMilli() >= row.updated+10000 {
			_ = r.finishRuntimeRestart(ctx, row, "not-started", "restart-interrupted-before-dispatch")
		}
		return
	}
	// A job queued in a previous systemd boot cannot run in this boot.
	// Retire only the exclusion and preserve the original unknown outcome.
	if control.bootID != "" && row.bootID != "" && row.bootID != control.bootID {
		_ = r.finishRuntimeRestart(ctx, row, "unknown", "restart-previous-boot")
		return
	}
	if row.phase == "dispatching" {
		if row.jobState != "needs-attention" {
			_ = r.noteUncertainRestart(ctx, row.job)
		}
		return // Without a known queue acknowledgement, do not clear the fence.
	}
	if row.phase != "submitted" || row.systemdJob == "" || control.supervisor == nil {
		return // Without a known queue acknowledgement, do not clear the fence.
	}
	current, err := control.supervisor.Inspect(ctx, row.target)
	if err != nil || !current.Active || current.JobPending || current.InvocationID == "" || current.MainPID <= 0 {
		return
	}
	reason := "restart-completion-unproven"
	if current.InvocationID != row.invocation {
		reason = "restart-new-invocation-observed"
	}
	_ = r.finishRuntimeRestart(ctx, row, "unknown", reason)
}

func (r *Repository) noteUncertainRestart(ctx context.Context, id string) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var active bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runtime_service_restart WHERE job_id=? AND phase='dispatching')", id).Scan(&active); err != nil || !active {
			return err
		}
		j, err := evidence.LoadJob(ctx, tx, id)
		if err != nil || j.State == "needs-attention" {
			return err
		}
		now := r.now()
		if _, err := evidence.ChangeJob(ctx, tx, id, j.Sequence, evidence.Transition{State: "needs-attention", Dispatch: "unknown", Business: "unknown", Reason: "restart-reply-unproven"}, now); err != nil {
			return err
		}
		_, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: restartOperation, Actor: j.Owner,
			Credential: j.Credential, Capability: j.Capability, Job: id, Object: j.Resource, Correlation: j.Correlation,
			RequestID: j.RequestID, RequestEpoch: j.RequestEpoch, RequestDomain: j.RequestDomain,
			Result: "unknown", Reason: "restart-reply-unproven", Critical: true, Created: now})
		return err
	})
}

func (r *Repository) finishRuntimeRestart(ctx context.Context, row runtimeRestartRecord, dispatch, reason string) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM runtime_service_restart WHERE singleton=1 AND job_id=? AND phase=?", row.job, row.phase)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return repository.ErrConflict
		}
		j, err := evidence.LoadJob(ctx, tx, row.job)
		if err != nil {
			return err
		}
		now := r.now()
		outcome := "unknown"
		if dispatch == "not-started" {
			outcome = "failure"
		}
		if _, err := evidence.ChangeJob(ctx, tx, j.ID, j.Sequence, evidence.Transition{State: "failed", Dispatch: dispatch, Business: outcome, Reason: reason}, now); err != nil {
			return err
		}
		_, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: restartOperation,
			Actor: j.Owner, Credential: j.Credential, Capability: j.Capability, Job: j.ID, Object: j.Resource,
			Correlation: j.Correlation, RequestID: j.RequestID, RequestEpoch: j.RequestEpoch, RequestDomain: j.RequestDomain,
			Result: outcome, Reason: reason, Created: now, Critical: true})
		return err
	})
}
