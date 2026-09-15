package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

func (r *Repository) ConfigureExecution(p execution.Provider) (*executions.Engine, error) {
	e, err := executions.New(r.store, r.key, p)
	if err == nil {
		r.fields = e
	}
	return e, err
}

func (r *Repository) reconcileFields(ctx context.Context, credential string, in authn.Command) (apitypes.Result, error) {
	var out apitypes.Result
	op, body, err := r.candidateCommand(in, "reconcileTransaction")
	if err != nil {
		return out, err
	}
	_, path, _, err := r.operation(in.Method, in.URI)
	if err != nil {
		return out, err
	}
	id := path["transaction_id"]
	var c authn.Claims
	authorize := func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			var err error
			c, err = r.checkOperation(ctx, q, credential, op, refsFor(op, path), false)
			return err
		})
	}
	if err = authorize(ctx); err != nil {
		return out, err
	}
	cmd := requests.Command{Principal: c.PrincipalID, Epoch: in.Epoch, Domain: "management", ID: in.RequestID, Operation: op.ID, Method: in.Method, URI: in.URI, Payload: body, Credential: c.CredentialID, Capability: "configuration.read"}
	if prior, found, err := r.receipts.Replay(ctx, cmd, authorize); err != nil || found {
		return prior, err
	}
	if r.fields == nil {
		return out, apitypes.Fail(503, "EXECUTION_RECOVERY_UNAVAILABLE")
	}
	// Reconciliation can only observe and monotonically refine durable evidence.
	// Repeating after an interrupted receipt cannot dispatch or repeat a write.
	if err = r.fields.Reconcile(ctx, id); err != nil {
		return out, err
	}
	return r.receipts.Execute(ctx, cmd, authorize, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		if _, err := r.checkOperation(ctx, tx, credential, op, refsFor(op, path), false); err != nil {
			return requests.Mutation{}, err
		}
		ref := &apitypes.Ref{Kind: "transaction", ID: id}
		job, err := evidence.CreateJob(ctx, tx, evidence.Job{State: "succeeded", Resource: ref, Transaction: id, Dispatch: "completed", Business: "success", Commit: "not-applicable", Reason: "reconciliation-observed"})
		if err != nil {
			return requests.Mutation{}, err
		}
		if _, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "reconcile-fields", Result: "observed", Object: ref, Job: job.ID, Transaction: id}); err != nil {
			return requests.Mutation{}, err
		}
		return requests.Mutation{Status: 202, Body: json.RawMessage(`{}`), Resource: ref, Job: &apitypes.Ref{Kind: "job", ID: job.ID}, Terminal: true}, nil
	})
}

// ExecutionAuthorizer is SQL-local and does not retain a capability snapshot as
// permission. The bearer stays in this closure, never in a journal or receipt.
// Holding the auth gate across provider I/O would delay logout/revocation.
func (r *Repository) ExecutionAuthorizer(credential string) executions.Authorizer {
	return func(ctx context.Context, q evidence.Query, in execution.Request, replay bool) (execution.Authorization, error) {
		var out execution.Authorization
		c, err := r.candidateClaims(ctx, q, credential, "config.apply")
		if err != nil {
			return out, err
		}
		if c.PrincipalID != in.Envelope.Owner {
			return out, apitypes.Fail(403, "RESOURCE_DENIED")
		}
		if err = in.Envelope.Verify(r.key, c.PrincipalID); err != nil {
			return out, err
		}
		out = execution.Authorization{Owner: c.PrincipalID, Credential: c.CredentialID, Epoch: c.RequestEpoch}
		if replay {
			return out, nil
		}
		if !slices.Contains(c.Capabilities, "ovs.port.vlan.write") {
			return out, apitypes.Fail(403, "CAPABILITY_DENIED")
		}
		var b, original []byte
		if err = q.QueryRowContext(ctx, "SELECT document,envelope FROM candidate_validations WHERE id=? AND owner_id=?", in.ValidationID, c.PrincipalID).Scan(&b, &original); errors.Is(err, sql.ErrNoRows) {
			return out, apitypes.Fail(404, "NOT_FOUND")
		} else if err != nil {
			return out, err
		}
		var v validationRecord
		var envelope plan.Envelope
		if json.Unmarshal(b, &v) != nil || json.Unmarshal(original, &envelope) != nil {
			return out, repository.ErrUnavailable
		}
		if plan.Digest(envelope) != plan.Digest(in.Envelope) {
			return out, apitypes.Fail(409, "VALIDATION_SCOPE_CHANGED")
		}
		if v.State != "passed" || !v.Usable || !r.now().Before(v.Expires) {
			return out, apitypes.Fail(409, "VALIDATION_NOT_USABLE")
		}
		if c.CredentialID != v.Credential || c.Epoch != v.AuthEpoch || c.RequestEpoch != v.RequestEpoch || c.Revision != v.PolicyRevision || plan.Digest(c.Capabilities) != v.Capabilities || v.Validator != plan.ValidatorVersion {
			return out, apitypes.Fail(409, "VALIDATION_AUTHORITY_CHANGED")
		}
		// Unlike review, execution requires an exact already-synchronized witness.
		var id, epoch, revision, digest string
		var sequence int64
		if err = q.QueryRowContext(ctx, "SELECT candidate_id,workspace_epoch,sequence,revision,envelope_hash FROM candidate_witnesses WHERE owner_id=?", c.PrincipalID).Scan(&id, &epoch, &sequence, &revision, &digest); err != nil {
			return out, apitypes.Fail(409, "WORKSPACE_WITNESS_REQUIRED")
		}
		if id != envelope.Candidate.ID || epoch != envelope.Epoch || sequence != envelope.Sequence || revision != envelope.Candidate.Revision || digest != plan.Digest(envelope) {
			return out, apitypes.Fail(409, "WORKSPACE_WITNESS_CHANGED")
		}
		snapshot, err := r.candidateSnapshot(ctx, plan.Bindings(envelope.Candidate))
		if err != nil {
			return out, err
		}
		if snapshot.Policy != v.ProviderPolicy {
			return out, apitypes.Fail(409, "AUTHORITY_POLICY_CHANGED")
		}
		checks, _ := plan.Checks(envelope.Candidate, snapshot)
		if !plan.Passed(checks) {
			return out, apitypes.Fail(409, "EXECUTION_PREFLIGHT_CONFLICT")
		}
		out.Policy = v.PolicyRevision
		out.ProviderPolicy = v.ProviderPolicy
		out.ChangeSet = v.ChangeSetID
		out.Validation = in.ValidationID
		out.Scope = plan.Digest([]any{in.ValidationID, envelope, v.Risk, v.ProviderPolicy})
		out.Expires = v.Expires
		return out, nil
	}
}
