package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

func (r *Repository) cancelJob(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	var out apitypes.Result
	op, path, query, err := r.operation(input.Method, input.URI)
	if err != nil {
		return out, err
	}
	if op.ID != "cancelJob" || op.Domain != "management" {
		return out, apitypes.Fail(403, "OPERATION_DENIED")
	}
	headers := map[string][]string{"Idempotency-Key": {input.RequestID}, "X-OVS-Request-Epoch": {input.Epoch}}
	if err = op.ValidateParameters(path, query, func(name string) []string { return headers[name] }); err != nil {
		return out, err
	}
	v, body, err := op.Decode(input.Payload)
	if err != nil {
		return out, err
	}
	if v["request_id"] != input.RequestID {
		return out, apitypes.Fail(409, "IDEMPOTENCY_KEY_MISMATCH")
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	c, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return out, err
	}
	check := func(ctx context.Context, q querier) error {
		var err error
		c, err = r.checkOperation(ctx, q, credential, op, nil, false)
		if err != nil {
			return err
		}
		j, err := evidence.LoadJob(ctx, q, path["job_id"])
		if err != nil {
			return err
		}
		if j.Owner != c.PrincipalID {
			return apitypes.Fail(404, "NOT_FOUND")
		}
		return require(c, j.Capability, false, r.now().Unix())
	}
	command := requests.Command{Principal: c.PrincipalID, Credential: c.CredentialID, Capability: "jobs.cancel", Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID, Method: input.Method, URI: input.URI, Payload: body}
	return r.receipts.Execute(ctx, command, func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { return check(ctx, q) })
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		result := requests.Mutation{Status: 202, Terminal: true, Body: json.RawMessage(`{}`)}
		if err := check(ctx, tx); err != nil {
			return result, err
		}
		job, err := evidence.LoadJob(ctx, tx, path["job_id"])
		if err != nil {
			return result, err
		}
		if Terminal := evidence.Terminal(job.State); Terminal || (!job.Cancellable && !job.CancelRequested) {
			return result, apitypes.Fail(409, "JOB_NOT_CANCELLABLE")
		}
		if !job.CancelRequested {
			transition := evidence.Transition{State: "cancel-requested", Reason: "cancellation-requested"}
			if job.State == "queued" && job.Dispatch == "not-started" {
				transition.State = "cancelled"
				transition.Business = "cancelled"
				transition.Commit = "not-sent"
				transition.Reason = "cancelled-before-dispatch"
			}
			if _, err = evidence.ChangeJob(ctx, tx, job.ID, job.Sequence, transition, r.now()); err != nil {
				return result, err
			}
		}
		result.Resource = &apitypes.Ref{Kind: "job", ID: job.ID}
		ack, err := evidence.CreateJob(ctx, tx, evidence.Job{Resource: result.Resource, State: "succeeded", Business: "success", Reason: "cancellation-request-recorded", Created: r.now()})
		if err != nil {
			return result, err
		}
		result.Job = &apitypes.Ref{Kind: "job", ID: ack.ID}
		if err = r.audit(ctx, tx, c, op.ID, job.ID, "cancel-requested", input.RequestID, result.Resource, result.Job); err != nil {
			return result, err
		}
		return result, r.touch(ctx, tx, credential, c)
	})
}

func (r *Repository) InitializeEvidence(ctx context.Context) error {
	resolve := func(operation string) string {
		for _, op := range r.contract.Operations {
			if op.ID == operation {
				if v := aliases[op.Capability]; v != "" {
					return v
				}
				return op.Capability
			}
		}
		return "legacy.unverified"
	}
	if err := evidence.ImportLegacy(ctx, r.store, resolve); err != nil {
		return err
	}
	return evidence.Recover(ctx, r.store, r.now())
}
func (r *Repository) MaintainEvidence(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Retention shares the manager writer and does not invoke providers.
			_ = evidence.Prune(ctx, r.store, r.now())
			_ = r.receipts.Prune(ctx)
		}
	}
}
