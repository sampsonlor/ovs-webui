package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

const (
	linkDiagnosticOperation = "runLinkLACPDiagnostic"
	linkDiagnosticTTL       = 15 * time.Minute
	maxLinkResultBytes      = 32 << 10
	maxLinkResults          = 128
)

type diagnosticObservation struct {
	state    string
	document func(authn.Claims) any
}

type boundedObservation struct {
	target     apitypes.Ref
	budget     time.Duration
	capability string
	authorize  func(authn.Claims) error
	admit      func(context.Context, authn.Claims) error
	collect    func(context.Context, authn.Claims) (diagnosticObservation, error)
}

type linkLACPInventory interface {
	diagnostics.LinkLACPSource
	CheckLinkLACPTarget(context.Context, diagnostics.LinkLACPRequest, authn.Claims) error
}

func (r *Repository) runLinkLACPDiagnostic(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	op, body, err := r.candidateCommand(input, linkDiagnosticOperation)
	if err != nil {
		return apitypes.Result{}, err
	}
	source, ok := r.inventory.(linkLACPInventory)
	if !ok || r.diagnosticsContext == nil {
		return apitypes.Result{}, apitypes.Fail(503, "DIAGNOSTICS_EXECUTOR_UNAVAILABLE")
	}
	var wire struct {
		Target        apitypes.Ref `json:"target_ref"`
		SampleSeconds int          `json:"sample_seconds"`
		PortLimit     int          `json:"port_limit"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return apitypes.Result{}, apitypes.Fail(422, "INVALID_DIAGNOSTIC_PARAMETERS")
	}
	req := diagnostics.LinkLACPRequest{Target: wire.Target, SampleSeconds: wire.SampleSeconds, PortLimit: wire.PortLimit}
	return r.startDiagnosticObservation(ctx, credential, input, op, body, boundedObservation{
		target: req.Target, budget: time.Duration(req.SampleSeconds) * time.Second, capability: diagnostics.LinkLACPCapability,
		authorize: req.Authorize,
		admit: func(ctx context.Context, claims authn.Claims) error {
			return source.CheckLinkLACPTarget(ctx, req, claims)
		},
		collect: func(ctx context.Context, claims authn.Claims) (diagnosticObservation, error) {
			result, err := diagnostics.RunLinkLACP(ctx, source, req, claims)
			return diagnosticObservation{state: result.State, document: func(final authn.Claims) any {
				withholdLinkConfiguration(final, &result)
				return result
			}}, err
		},
	})
}

func (r *Repository) startDiagnosticObservation(ctx context.Context, credential string, input authn.Command, op *apicontract.Operation, body []byte, observation boundedObservation) (apitypes.Result, error) {
	result, id, err := r.admitDiagnosticObservation(ctx, credential, input, op, body, observation)
	if err == nil && id != "" {
		go r.executeDiagnosticObservation(r.diagnosticsContext, credential, op, id, observation)
	}
	return result, err
}

func (r *Repository) admitDiagnosticObservation(ctx context.Context, credential string, input authn.Command, op *apicontract.Operation, body []byte, observation boundedObservation) (apitypes.Result, string, error) {
	unlock, err := r.lock(ctx)
	if err != nil {
		return apitypes.Result{}, "", err
	}
	defer unlock()

	claims, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return apitypes.Result{}, "", err
	}
	check := func(ctx context.Context, q querier) error {
		claims, err = r.checkOperation(ctx, q, credential, op, []apitypes.Ref{observation.target}, false)
		if err != nil {
			return err
		}
		return observation.authorize(claims)
	}
	command := requests.Command{Principal: claims.PrincipalID, Credential: claims.CredentialID, Capability: observation.capability,
		Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID,
		Method: input.Method, URI: input.URI, Payload: body}
	var admitted string
	result, err := r.receipts.Execute(ctx, command, func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { return check(ctx, q) })
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		out := requests.Mutation{Status: 202, Body: json.RawMessage(`{}`)}
		if err := check(ctx, tx); err != nil {
			return out, err
		}
		if err := observation.admit(ctx, claims); err != nil {
			return out, err
		}
		var inflight, owned, retained int
		err := tx.QueryRowContext(ctx, `SELECT
          COUNT(*),
          COUNT(CASE WHEN e.owner_id=? THEN 1 END),
          (SELECT COUNT(*) FROM diagnostic_observations WHERE expires_ms>?)
        FROM evidence_jobs e JOIN jobs j ON e.id=j.id
        WHERE e.operation IN (?,?) AND j.state IN ('queued','running','cancel-requested')`,
			claims.PrincipalID, r.now().UnixMilli(), linkDiagnosticOperation, interfaceCounterOperation).Scan(&inflight, &owned, &retained)
		if err != nil {
			return out, err
		}
		if inflight >= evidence.MaxRunning || owned != 0 || inflight+retained >= maxLinkResults {
			return out, apitypes.Fail(429, "DIAGNOSTIC_CAPACITY_REACHED")
		}
		job, err := evidence.CreateJob(ctx, tx, evidence.Job{
			State: "queued", Cancellable: true, Reason: "diagnostic-admitted",
			Resource: &observation.target, Created: r.now(),
		})
		if err != nil {
			return out, err
		}
		out.Resource, out.Job = &observation.target, &apitypes.Ref{Kind: "job", ID: job.ID}
		if err := r.audit(ctx, tx, claims, op.ID, observation.target.ID, "diagnostic-admitted", input.RequestID, &observation.target, out.Job); err != nil {
			return out, err
		}
		admitted = job.ID
		return out, r.touch(ctx, tx, credential, claims)
	})
	return result, admitted, err
}

func (r *Repository) executeDiagnosticObservation(serviceCtx context.Context, credential string, op *apicontract.Operation, id string, observation boundedObservation) {
	ctx, cancel := context.WithTimeout(serviceCtx, observation.budget)
	defer cancel()
	var claims authn.Claims
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		j, err := evidence.LoadJob(ctx, tx, id)
		if err != nil || j.State != "queued" {
			return err
		}
		claims, err = r.checkOperation(ctx, tx, credential, op, []apitypes.Ref{observation.target}, false)
		if err != nil {
			return err
		}
		if err := observation.authorize(claims); err != nil {
			return err
		}
		_, err = evidence.ChangeJob(ctx, tx, id, j.Sequence, evidence.Transition{
			State: "running", Dispatch: "sent", Reason: "diagnostic-collecting",
		}, r.now())
		return err
	})
	var result diagnosticObservation
	if err == nil && claims.PrincipalID != "" {
		go r.watchDiagnosticCancellation(ctx, cancel, id, credential, observation.capability)
		result, err = observation.collect(ctx, claims)
	}
	cancel()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(serviceCtx), 3*time.Second)
	defer finishCancel()
	_ = r.finishDiagnosticObservation(finishCtx, credential, op, id, observation, result, err)
}

func (r *Repository) watchDiagnosticCancellation(ctx context.Context, stop context.CancelFunc, id, credential, capability string) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
				j, err := evidence.LoadJob(ctx, q, id)
				if err != nil {
					return err
				}
				if j.CancelRequested || j.State != "running" {
					return context.Canceled
				}
				claims, err := r.claims(ctx, q, credential)
				if err != nil {
					return err
				}
				if !slices.Contains(claims.Capabilities, capability) ||
					!slices.Contains(claims.Capabilities, "inventory.read") {
					return apitypes.Fail(403, "CAPABILITY_DENIED")
				}
				return nil
			})
			if err != nil {
				stop()
				return
			}
		}
	}
}

func (r *Repository) finishDiagnosticObservation(ctx context.Context, credential string, op *apicontract.Operation, id string, observation boundedObservation, result diagnosticObservation, runErr error) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		j, err := evidence.LoadJob(ctx, tx, id)
		if err != nil || evidence.Terminal(j.State) {
			return err
		}
		now := r.now()
		transition := evidence.Transition{State: "failed", Business: "failure", Reason: "diagnostic-operation-failed"}
		if j.Dispatch == "sent" {
			transition.Dispatch = "completed"
		}
		claims, authErr := r.checkOperation(ctx, tx, credential, op, []apitypes.Ref{observation.target}, false)
		if authErr == nil {
			authErr = observation.authorize(claims)
		}
		switch {
		case j.CancelRequested:
			transition.State, transition.Business, transition.Reason = "cancelled", "cancelled", "diagnostic-cancelled"
		case authErr != nil:
			transition.Reason = "diagnostic-authority-unavailable"
		case errors.Is(runErr, context.DeadlineExceeded):
			transition.Reason = "diagnostic-timed-out"
		case runErr == nil && result.state != "" && result.state != "cancelled" && result.document != nil:
			if err := writeDiagnosticObservation(ctx, tx, id, now, result.document(claims)); err != nil {
				return err
			}
			transition.Reason = "diagnostic-" + result.state
			if slices.Contains([]string{"complete", "partial", "truncated", "no-data"}, result.state) {
				transition.State, transition.Business = "succeeded", "success"
			}
		}
		return finishDiagnosticJob(ctx, tx, j, transition, now)
	})
}

func writeDiagnosticObservation(ctx context.Context, tx *sql.Tx, id string, now time.Time, result any) error {
	document, err := json.Marshal(result)
	if err != nil || len(document) > maxLinkResultBytes {
		return apitypes.Fail(503, "DIAGNOSTIC_RESULT_UNAVAILABLE")
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO diagnostic_observations(job_id,expires_ms,document) VALUES(?,?,?)",
		id, now.Add(linkDiagnosticTTL).UnixMilli(), document)
	return err
}

func withholdLinkConfiguration(claims authn.Claims, result *diagnostics.LinkLACPResult) {
	if slices.Contains(claims.Capabilities, "configuration.read") || len(result.Ports) == 0 {
		return
	}
	for i := range result.Ports {
		port := &result.Ports[i]
		port.LACP = "withheld"
		for j := range port.Members {
			if port.Members[j].LACPCurrent == "not-applicable" {
				port.Members[j].LACPCurrent = "unknown"
			}
		}
	}
	result.Partial = true
	if result.State == "complete" {
		result.State, result.Reason = "partial", "DIAGNOSTIC_CONFIGURATION_WITHHELD"
	}
}

func finishDiagnosticJob(ctx context.Context, tx *sql.Tx, j evidence.Job, transition evidence.Transition, now time.Time) error {
	if _, err := evidence.ChangeJob(ctx, tx, j.ID, j.Sequence, transition, now); err != nil {
		return err
	}
	_, err := evidence.Append(ctx, tx, evidence.Record{
		Collection: "audit", Origin: "Manager", Operation: j.Operation, Actor: j.Owner, Credential: j.Credential,
		Capability: j.Capability, Job: j.ID, Object: j.Resource, Correlation: j.Correlation,
		RequestID: j.RequestID, RequestEpoch: j.RequestEpoch, RequestDomain: j.RequestDomain,
		Result: transition.Business, Reason: transition.Reason, Critical: true, Created: now,
	})
	return err
}

func (r *Repository) readLinkLACPResult(ctx context.Context, tx *sql.Tx, claims authn.Claims, id string) (any, error) {
	j, document, err := r.readDiagnosticObservation(ctx, tx, claims, id, linkDiagnosticOperation)
	if err != nil {
		return nil, err
	}
	var result diagnostics.LinkLACPResult
	if json.Unmarshal(document, &result) != nil || j.Resource == nil || result.Target != *j.Resource {
		return nil, apitypes.Fail(503, "DIAGNOSTIC_RESULT_UNAVAILABLE")
	}
	withholdLinkConfiguration(claims, &result)
	return result, nil
}

func (r *Repository) readDiagnosticObservation(ctx context.Context, tx *sql.Tx, claims authn.Claims, id, operation string) (evidence.Job, []byte, error) {
	if !slices.Contains(claims.Capabilities, "inventory.read") {
		return evidence.Job{}, nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	j, err := evidence.LoadJob(ctx, tx, id)
	if err != nil {
		return evidence.Job{}, nil, err
	}
	if j.Owner != claims.PrincipalID || j.Operation != operation {
		return evidence.Job{}, nil, apitypes.Fail(404, "NOT_FOUND")
	}
	if !evidence.Terminal(j.State) {
		return evidence.Job{}, nil, apitypes.Fail(409, "DIAGNOSTIC_RESULT_PENDING")
	}
	var document []byte
	if err := tx.QueryRowContext(ctx, "SELECT document FROM diagnostic_observations WHERE job_id=? AND expires_ms>?", id, r.now().UnixMilli()).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return evidence.Job{}, nil, apitypes.Fail(404, "DIAGNOSTIC_RESULT_UNAVAILABLE")
		}
		return evidence.Job{}, nil, err
	}
	if len(document) > maxLinkResultBytes {
		return evidence.Job{}, nil, apitypes.Fail(503, "DIAGNOSTIC_RESULT_UNAVAILABLE")
	}
	return j, document, nil
}

func (r *Repository) pruneDiagnosticObservations(ctx context.Context) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM diagnostic_observations WHERE job_id IN (SELECT job_id FROM diagnostic_observations WHERE expires_ms<=? ORDER BY expires_ms,job_id LIMIT 128)",
			r.now().UnixMilli())
		return err
	})
}

func (r *Repository) retireInterruptedDiagnostics(ctx context.Context, restarting bool) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT j.id FROM jobs j JOIN evidence_jobs e ON e.id=j.id
         WHERE e.operation IN (?,?,?) AND j.state IN ('queued','running','cancel-requested','needs-attention')
         AND (? OR e.updated_at_ms<=?)
         LIMIT ?`, linkDiagnosticOperation, interfaceCounterOperation, captureOperation, restarting, r.now().Add(-8*time.Minute).UnixMilli(), evidence.MaxQueued+evidence.MaxRunning+1)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(ids) == 0 {
			return err
		}
		if len(ids) > evidence.MaxQueued+evidence.MaxRunning {
			return repository.ErrUnavailable
		}
		reason := "diagnostic-worker-deadline-exceeded"
		if restarting {
			reason = "diagnostic-interrupted-by-manager-restart"
		}
		for _, id := range ids {
			j, err := evidence.LoadJob(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := finishDiagnosticJob(ctx, tx, j, evidence.Transition{
				State: "failed", Business: "failure", Reason: reason,
			}, r.now()); err != nil {
				return err
			}
		}
		return nil
	})
}
