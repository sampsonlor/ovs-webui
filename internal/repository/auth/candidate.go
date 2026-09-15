package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

func (r *Repository) candidateClaims(ctx context.Context, q querier, credential, capability string) (authn.Claims, error) {
	c, err := r.claims(ctx, q, credential)
	if err != nil {
		return c, err
	}
	for _, cap := range []string{capability, "configuration.read", "inventory.read"} {
		if err = require(c, cap, false, r.now().Unix()); err != nil {
			return c, err
		}
	}
	return c, nil
}
func (r *Repository) candidateSnapshot(ctx context.Context, bindings []plan.Binding) (plan.Snapshot, error) {
	p, ok := r.inventory.(plan.Provider)
	if !ok {
		return plan.Snapshot{}, apitypes.Fail(503, "PROVIDER_UNAVAILABLE")
	}
	return p.CandidateSnapshot(ctx, bindings)
}
func (r *Repository) candidateCommand(input authn.Command, id string) (*apicontract.Operation, json.RawMessage, error) {
	op, path, query, err := r.operation(input.Method, input.URI)
	if err != nil {
		return nil, nil, err
	}
	if op.ID != id || len(query) != 0 {
		return nil, nil, apitypes.Fail(403, "OPERATION_DENIED")
	}
	headers := map[string][]string{"Idempotency-Key": {input.RequestID}, "X-OVS-Request-Epoch": {input.Epoch}, "If-Match": {input.Precondition}}
	if err = op.ValidateParameters(path, query, func(n string) []string { return headers[n] }); err != nil {
		return nil, nil, err
	}
	value, body, err := op.Decode(input.Payload)
	if err != nil {
		return nil, nil, err
	}
	if value["request_id"] != input.RequestID {
		return nil, nil, apitypes.Fail(409, "IDEMPOTENCY_KEY_MISMATCH")
	}
	return op, body, nil
}
func (r *Repository) PrepareCandidate(ctx context.Context, credential string, in plan.PrepareRequest) (plan.Envelope, error) {
	var out plan.Envelope
	_, body, err := r.candidateCommand(in.Command, "changeCandidate")
	if err != nil {
		return out, err
	}
	var cmd plan.Command
	// The frozen union includes future operations. Reject every operation not
	// implemented by this typed service, even if it passes the public schema.
	if json.Unmarshal(body, &cmd) != nil {
		return out, apitypes.Fail(422, "INVALID_INTENT")
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	c, err := r.CheckAuth(ctx, credential, authn.Check{Capability: "config.stage"})
	if err != nil {
		return out, err
	}
	if err = in.Envelope.Verify(r.key, c.PrincipalID); err != nil {
		return out, err
	}
	if !etagPattern.MatchString(in.Command.Precondition) || in.Command.Precondition != `"`+in.Envelope.Candidate.Revision+`"` {
		return out, apitypes.Fail(412, "ETAG_MISMATCH")
	}
	if in.Command.Epoch != in.Envelope.Epoch {
		return out, apitypes.Fail(410, "REQUEST_EPOCH_CHANGED")
	}
	var snapshot plan.Snapshot
	if cmd.Operation == "stage" || cmd.Operation == "rebase" {
		bindings := plan.Bindings(in.Envelope.Candidate)
		for _, i := range cmd.Intents {
			bindings = append(bindings, i.Object)
		}
		snapshot, err = r.candidateSnapshot(ctx, bindings)
		if err != nil {
			return out, err
		}
	}
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := r.candidateClaims(ctx, tx, credential, "config.stage"); err != nil {
			return err
		}
		if err := candidateFence(ctx, tx, in.Envelope, false); err != nil {
			return err
		}
		var err error
		out, err = plan.Prepare(in.Envelope, cmd, snapshot)
		if err != nil {
			return err
		}
		out.Sign(r.key)
		if err = plan.Budget(out); err != nil {
			return err
		}
		return r.touch(ctx, tx, credential, c)
	})
	return out, err
}

func candidateFence(ctx context.Context, tx *sql.Tx, e plan.Envelope, advance bool) error {
	var id, epoch, rev, hash string
	var seq int64
	err := tx.QueryRowContext(ctx, "SELECT candidate_id,workspace_epoch,sequence,revision,envelope_hash FROM candidate_witnesses WHERE owner_id=?", e.Owner).Scan(&id, &epoch, &seq, &rev, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if id != e.Candidate.ID || epoch != e.Epoch {
			return apitypes.Fail(409, "WORKSPACE_RECONCILIATION_REQUIRED")
		}
		if seq > e.Sequence || seq == e.Sequence && (rev != e.Candidate.Revision || hash != plan.Digest(e)) {
			return apitypes.Fail(409, "CANDIDATE_WITNESS_CHANGED")
		}
		if seq == e.Sequence || !advance {
			return nil
		}
	} else if !advance {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO candidate_witnesses VALUES(?,?,?,?,?,?) ON CONFLICT(owner_id) DO UPDATE SET sequence=excluded.sequence,revision=excluded.revision,envelope_hash=excluded.envelope_hash`, e.Owner, e.Candidate.ID, e.Epoch, e.Sequence, e.Candidate.Revision, plan.Digest(e))
	return err
}

type validationRecord struct {
	plan.Validation
	Credential     string `json:"credential_id"`
	AuthEpoch      string `json:"auth_epoch"`
	RequestEpoch   string `json:"request_epoch"`
	Capabilities   string `json:"capabilities_digest"`
	ProviderPolicy string `json:"provider_policy"`
	Validator      string `json:"validator"`
}

func candidateProblem(err error) plan.Gate {
	code := "PROVIDER_UNAVAILABLE"
	var p *apitypes.Problem
	if errors.As(err, &p) {
		code = p.Code
	}
	return plan.Gate{Code: code, State: "blocked", Reason: code}
}
func invalidation(v *plan.Validation, code string) {
	v.Usable = false
	v.Invalidations = append(v.Invalidations, plan.Gate{Code: code, State: "blocked", Reason: code})
}

func (r *Repository) ReadCandidate(ctx context.Context, credential string, in plan.ReadRequest) (authn.Response, error) {
	var out authn.Response
	cap := "workspace.read"
	if in.ValidationID != "" {
		cap = "config.validate"
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	c, err := r.CheckAuth(ctx, credential, authn.Check{Capability: cap})
	if err != nil {
		return out, err
	}
	if err = in.Envelope.Verify(r.key, c.PrincipalID); err != nil {
		return out, err
	}
	var record validationRecord
	var original plan.Envelope
	bindings := plan.Bindings(in.Envelope.Candidate)
	if in.ValidationID != "" {
		err = r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			var doc, blob []byte
			if err := q.QueryRowContext(ctx, "SELECT document,envelope FROM candidate_validations WHERE id=? AND owner_id=?", in.ValidationID, c.PrincipalID).Scan(&doc, &blob); errors.Is(err, sql.ErrNoRows) {
				return apitypes.Fail(404, "NOT_FOUND")
			} else if err != nil {
				return err
			}
			if json.Unmarshal(doc, &record) != nil || json.Unmarshal(blob, &original) != nil {
				return repository.ErrUnavailable
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		bindings = plan.Bindings(original.Candidate)
	}
	snapshot, snapshotErr := r.candidateSnapshot(ctx, bindings)
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		c, err := r.candidateClaims(ctx, tx, credential, cap)
		if err != nil {
			return err
		}
		if err = candidateFence(ctx, tx, in.Envelope, true); err != nil {
			return err
		}
		var value any
		if in.ValidationID == "" {
			v := plan.Compare(in.Envelope.Candidate, snapshot)
			if snapshotErr != nil {
				v = plan.View{Candidate: in.Envelope.Candidate, Diff: []plan.Diff{}, Checks: []plan.Gate{candidateProblem(snapshotErr)}}
				if len(v.Intents) > 0 {
					v.State = "stale"
				}
			}
			value = v
		} else {
			v := record.Validation
			if in.Envelope.Candidate.ID != v.CandidateID || in.Envelope.Candidate.Revision != v.CandidateRevision {
				invalidation(&v, "CANDIDATE_CHANGED")
			}
			if !r.now().Before(v.Expires) {
				invalidation(&v, "VALIDATION_EXPIRED")
			}
			if c.Revision != v.PolicyRevision || c.Epoch != record.AuthEpoch || c.RequestEpoch != record.RequestEpoch || plan.Digest(c.Capabilities) != record.Capabilities || record.Validator != plan.ValidatorVersion {
				invalidation(&v, "POLICY_CHANGED")
			}
			// A result remains inspectable by its owner after re-login, but only
			// the exact current credential that validated it may continue using it.
			if c.CredentialID != record.Credential {
				invalidation(&v, "VALIDATION_CREDENTIAL_CHANGED")
			}
			if !slices.Contains(c.Capabilities, "ovs.port.vlan.write") {
				invalidation(&v, "CAPABILITY_DENIED")
			}
			current, e := snapshot, snapshotErr
			if e != nil {
				invalidation(&v, candidateProblem(e).Code)
			} else {
				if current.Policy != record.ProviderPolicy {
					invalidation(&v, "AUTHORITY_POLICY_CHANGED")
				}
				checks, _ := plan.Checks(original.Candidate, current)
				for _, g := range checks {
					if g.State != "allowed" {
						v.Usable = false
						v.Invalidations = append(v.Invalidations, g)
					}
				}
			}
			value = v
		}
		out.Status = 200
		out.Body, err = json.Marshal(value)
		if err != nil {
			return err
		}
		if len(out.Body) > 48<<10 {
			return apitypes.Fail(429, "RESPONSE_BUDGET_EXCEEDED")
		}
		return r.touch(ctx, tx, credential, c)
	})
	return out, err
}

func (r *Repository) ValidateCandidate(ctx context.Context, credential string, in plan.ValidateRequest) (apitypes.Result, error) {
	var out apitypes.Result
	op, body, err := r.candidateCommand(in.Command, "createValidation")
	if err != nil {
		return out, err
	}
	var request struct {
		RequestID   string `json:"request_id"`
		CandidateID string `json:"candidate_id"`
		Revision    string `json:"candidate_revision"`
	}
	if json.Unmarshal(body, &request) != nil {
		return out, apitypes.Fail(422, "INVALID_REQUEST")
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	var c authn.Claims
	authorize := func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			var err error
			c, err = r.candidateClaims(ctx, q, credential, op.Capability)
			return err
		})
	}
	if err = authorize(ctx); err != nil {
		return out, err
	}
	command := requests.Command{Principal: c.PrincipalID, Epoch: in.Command.Epoch, Domain: "management", ID: in.Command.RequestID, Operation: op.ID, Method: in.Command.Method, URI: in.Command.URI, Payload: body, Credential: c.CredentialID, Capability: "configuration.validate"}
	if prior, found, err := r.receipts.Replay(ctx, command, authorize); err != nil || found {
		return prior, err
	}
	if err = in.Envelope.Verify(r.key, c.PrincipalID); err != nil {
		return out, err
	}
	if request.CandidateID != in.Envelope.Candidate.ID || request.Revision != in.Envelope.Candidate.Revision {
		return out, apitypes.Fail(409, "CANDIDATE_CHANGED")
	}
	snapshot, snapshotErr := r.candidateSnapshot(ctx, plan.Bindings(in.Envelope.Candidate))
	return r.receipts.Execute(ctx, command, authorize, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		var result requests.Mutation
		c, err := r.candidateClaims(ctx, tx, credential, op.Capability)
		if err != nil {
			return result, err
		}
		if err = candidateFence(ctx, tx, in.Envelope, true); err != nil {
			return result, err
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM candidate_validations").Scan(&count); err != nil {
			return result, err
		}
		if count >= 10000 {
			return result, apitypes.Fail(429, "VALIDATION_CAPACITY_REACHED")
		}
		checks, diff := plan.Checks(in.Envelope.Candidate, snapshot)
		state := "passed"
		if !plan.Passed(checks) {
			state = "blocked"
		}
		if snapshotErr != nil {
			checks = []plan.Gate{candidateProblem(snapshotErr)}
			diff = []plan.Diff{}
			state = "failed"
		}
		if !slices.Contains(c.Capabilities, "ovs.port.vlan.write") {
			checks = append(checks, plan.Gate{Code: "CAPABILITY_DENIED", State: "blocked", Reason: "CAPABILITY_DENIED"})
			state = "blocked"
		}
		generation := snapshot.Generation
		if generation == "" {
			if in.Envelope.Candidate.Generation != nil {
				generation = *in.Envelope.Candidate.Generation
			} else {
				return result, apitypes.Fail(503, "PROVIDER_UNAVAILABLE")
			}
		}
		revision := snapshot.Revision
		if revision == "" {
			revision = "unavailable"
		}
		id, changeset, jobID := repository.NewID(), repository.NewID(), repository.NewID()
		v := plan.Validation{ID: id, CandidateID: request.CandidateID, CandidateRevision: request.Revision, Generation: generation, ConfigRevision: revision, PolicyRevision: c.Revision, Expires: r.now().Add(plan.ValidFor).UTC(), Job: &apitypes.Ref{Kind: "job", ID: jobID}, State: state, Checks: checks, Diff: diff, ChangeSetID: changeset, Usable: state == "passed", Invalidations: []plan.Gate{}, Risk: "connectivity-unknown-safe-apply-required", ExecutionReady: false}
		if c.ExpiresAt.Before(v.Expires) {
			v.Expires = c.ExpiresAt
		}
		record := validationRecord{Validation: v, Credential: c.CredentialID, AuthEpoch: c.Epoch, RequestEpoch: c.RequestEpoch, Capabilities: plan.Digest(c.Capabilities), ProviderPolicy: snapshot.Policy, Validator: plan.ValidatorVersion}
		doc, _ := json.Marshal(record)
		blob, _ := json.Marshal(in.Envelope)
		if len(doc) > 48<<10 || len(blob) > plan.MaxDocument {
			return result, apitypes.Fail(429, "CANDIDATE_BUDGET_EXCEEDED")
		}
		resource := &apitypes.Ref{Kind: "validation", ID: id}
		jobState, business := "succeeded", "success"
		if state == "failed" {
			jobState = "failed"
			business = "failure"
		}
		// Evaluation is bounded in-memory work. It commits its completed Job,
		// immutable result and receipt together; no worker or provider write can
		// be orphaned by a lost HTTP/IPC response or a daemon restart.
		_, err = evidence.CreateJob(ctx, tx, evidence.Job{ID: jobID, State: jobState, Resource: resource, ChangeSet: changeset, Created: r.now().UTC(), Dispatch: "completed", Business: business, Commit: "not-applicable", Reason: "validation-" + state})
		if err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO candidate_validations VALUES(?,?,?,?,?,?,?,?,?)", id, c.PrincipalID, request.CandidateID, request.Revision, jobID, changeset, r.now().UnixMilli(), blob, doc); err != nil {
			return result, err
		}
		if _, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "validate-candidate", Result: state, Reason: "validation-" + state, Object: resource, Job: jobID, ChangeSet: changeset, Created: r.now().UTC()}); err != nil {
			return result, err
		}
		if err = r.touch(ctx, tx, credential, c); err != nil {
			return result, err
		}
		return requests.Mutation{Status: 202, Body: json.RawMessage(`{}`), Resource: resource, Job: v.Job, Terminal: true}, nil
	})
}

var _ plan.Manager = (*Repository)(nil)
