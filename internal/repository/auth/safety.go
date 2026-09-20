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
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

func (r *Repository) ConfigureSafety(options executions.SafetyOptions) error {
	if r.fields == nil {
		return apitypes.Fail(503, "EXECUTION_SERVICE_UNAVAILABLE")
	}
	options.Authority = r.safeAuthority
	return r.fields.ConfigureSafety(options)
}
func (r *Repository) SafetyAvailable() bool { return r.fields != nil && r.fields.SafetyAvailable() }

// Reload by non-secret credential ID. No credential bearer is kept in the
// journal, so recovery can revoke confirmation without retaining login secrets.
func (r *Repository) safeAuthority(ctx context.Context, q evidence.Query, a execution.Authorization) error {
	epoch, _, err := r.clock(ctx, q)
	if err != nil {
		return err
	}
	var credentialEpoch string
	var ceiling []byte
	var expires, issued, last int64
	var revoked sql.NullInt64
	var disabled bool
	var kind string
	err = q.QueryRowContext(ctx, `SELECT g.epoch,g.ceiling,g.expires_at,g.revoked_at,p.disabled,c.issued_at,c.last_used_at,'grant'
 FROM grants g JOIN auth_grants c ON c.grant_hash=g.grant_hash JOIN principals p ON p.id=g.principal_id
 WHERE c.id=? AND p.id=? UNION ALL
 SELECT t.epoch,t.scopes,t.expires_at,t.revoked_at,p.disabled,c.created_at,c.last_used_at,'token'
 FROM api_tokens t JOIN auth_tokens c ON c.token_hash=t.token_hash JOIN principals p ON p.id=t.principal_id
 WHERE c.id=? AND p.id=?`, a.Credential, a.Owner, a.Credential, a.Owner).Scan(&credentialEpoch, &ceiling, &expires, &revoked, &disabled, &issued, &last, &kind)
	if err != nil {
		return apitypes.Fail(403, "APPLY_AUTHORITY_REVOKED")
	}
	now := r.now().Unix()
	if credentialEpoch != epoch || disabled || revoked.Valid || now < issued || now >= expires || kind == "grant" && now >= last+int64(authn.SessionIdle.Seconds()) {
		return apitypes.Fail(403, "APPLY_AUTHORITY_REVOKED")
	}
	var currentEpoch string
	if err = q.QueryRowContext(ctx, "SELECT epoch FROM api_authority WHERE singleton=1").Scan(&currentEpoch); err != nil {
		return err
	}
	if currentEpoch != a.Epoch {
		return apitypes.Fail(403, "APPLY_AUTHORITY_REVOKED")
	}
	var caps []string
	if json.Unmarshal(ceiling, &caps) != nil {
		return repository.ErrUnavailable
	}
	current, err := capabilities(ctx, q, a.Owner)
	if err != nil {
		return err
	}
	for _, cap := range []string{"configuration.apply", "configuration.confirm", "ovs.port.vlan.write", "configuration.read", "inventory.read"} {
		if !slices.Contains(caps, cap) || !slices.Contains(current, cap) {
			return apitypes.Fail(403, "APPLY_AUTHORITY_REVOKED")
		}
	}
	return nil
}

type reservedLease struct {
	r  *Repository
	in safety.Request
}

func (l reservedLease) Check(ctx context.Context, req execution.Request) error {
	if !l.r.SafetyAvailable() || req.ID != l.in.Command.RequestID || plan.Digest(req.Envelope) != plan.Digest(l.in.Envelope) {
		return apitypes.Fail(409, "WORKSPACE_RESERVATION_REQUIRED")
	}
	// The credential-checked IPC peer is the sole writer of web.db. This closed
	// command represents its durable compare-and-freeze, never a browser claim.
	return l.r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var n int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM safe_resolutions WHERE owner_id=? AND request_epoch=? AND request_id=?", req.Envelope.Owner, l.in.Command.Epoch, req.ID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return apitypes.Fail(409, "SAFE_APPLY_ALREADY_RESOLVED")
		}
		return nil
	})
}
func (r *Repository) safeCommand(in safety.Request) (execution.Request, error) {
	var req execution.Request
	_, body, err := r.candidateCommand(in.Command, "createTransaction")
	if err != nil {
		return req, err
	}
	var command struct {
		Candidate  string `json:"candidate_id"`
		Revision   string `json:"candidate_revision"`
		Validation string `json:"validation_id"`
		Mode       string `json:"mode"`
	}
	if json.Unmarshal(body, &command) != nil || !apitypes.ManagementID(in.Reservation) || plan.Budget(in.Envelope) != nil {
		return req, apitypes.Fail(422, "INVALID_SAFE_APPLY")
	}
	if command.Mode != "safe-apply" {
		return req, apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
	}
	if command.Candidate != in.Envelope.Candidate.ID || command.Revision != in.Envelope.Candidate.Revision {
		return req, apitypes.Fail(409, "CANDIDATE_CHANGED")
	}
	return execution.Request{ID: in.Command.RequestID, ValidationID: command.Validation, Envelope: in.Envelope}, nil
}
func (r *Repository) AdmitSafeApply(ctx context.Context, credential string, in safety.Request) (apitypes.Result, error) {
	req, err := r.safeCommand(in)
	if err != nil {
		return apitypes.Result{}, err
	}
	c, err := r.CheckAuth(ctx, credential, authn.Check{Capability: "config.apply"})
	if err != nil {
		return apitypes.Result{}, err
	}
	if in.Command.Epoch != c.RequestEpoch {
		return apitypes.Result{}, apitypes.Fail(410, "REQUEST_EPOCH_CHANGED")
	}
	if r.fields == nil {
		return apitypes.Result{}, apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
	}
	payload, _ := json.Marshal(in)
	cmd := requests.Command{Principal: c.PrincipalID, Epoch: c.RequestEpoch, Domain: "management", ID: req.ID, Operation: "createTransaction", Method: in.Command.Method, URI: in.Command.URI, Payload: payload, Credential: c.CredentialID, Capability: "configuration.apply"}
	return r.fields.SubmitSafe(ctx, req, r.ExecutionAuthorizer(credential), reservedLease{r, in}, cmd, in.Reservation)
}
func (r *Repository) decideSafeApply(ctx context.Context, credential string, in authn.Command) (apitypes.Result, error) {
	op, body, err := r.candidateCommand(in, "decideTransaction")
	if err != nil {
		return apitypes.Result{}, err
	}
	_, path, _, err := r.operation(in.Method, in.URI)
	if err != nil {
		return apitypes.Result{}, err
	}
	var decision struct {
		Decision string `json:"decision"`
		Sequence string `json:"expected_sequence"`
	}
	if json.Unmarshal(body, &decision) != nil {
		return apitypes.Result{}, apitypes.Fail(422, "INVALID_DECISION")
	}
	cap := "configuration.confirm"
	if decision.Decision == "rollback" {
		cap = "configuration.rollback"
	}
	var c authn.Claims
	check := func(ctx context.Context, q evidence.Query) error {
		var err error
		c, err = r.claims(ctx, q, credential)
		if err != nil {
			return err
		}
		if err = require(c, cap, false, r.now().Unix()); err != nil {
			return err
		}
		return checkRefs(ctx, q, c, refsFor(op, path))
	}
	if err = r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { return check(ctx, q) }); err != nil {
		return apitypes.Result{}, err
	}
	if r.fields == nil {
		return apitypes.Result{}, apitypes.Fail(503, "SAFE_APPLY_UNAVAILABLE")
	}
	command := requests.Command{Principal: c.PrincipalID, Epoch: in.Epoch, Domain: "management", ID: in.RequestID, Operation: op.ID, Method: in.Method, URI: in.URI, Payload: body, Credential: c.CredentialID, Capability: cap}
	return r.fields.Decide(ctx, path["transaction_id"], decision.Sequence, decision.Decision, check, command)
}

// Resolution both seals the authoritative successor draft and fences any late
// admission. It is safe even when the acceptance reply was lost: an admitted
// transaction stays frozen; a not-yet-admitted request is durably tombstoned.
func (r *Repository) ResolveSafeApply(ctx context.Context, credential string, in safety.Request) (safety.Resolution, error) {
	var out safety.Resolution
	if _, err := r.safeCommand(in); err != nil {
		return out, err
	}
	fingerprint := plan.Digest(in)
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		c, err := r.candidateClaims(ctx, tx, credential, "workspace.read")
		if err != nil {
			return err
		}
		if err = in.Envelope.Verify(r.key, c.PrincipalID); err != nil {
			return err
		}
		var prior string
		var blob []byte
		err = tx.QueryRowContext(ctx, "SELECT fingerprint,document FROM safe_resolutions WHERE owner_id=? AND request_epoch=? AND request_id=?", c.PrincipalID, in.Command.Epoch, in.Command.RequestID).Scan(&prior, &blob)
		if err == nil {
			if prior != fingerprint {
				return apitypes.Fail(409, "IDEMPOTENCY_MISMATCH")
			}
			if json.Unmarshal(blob, &out) != nil {
				return repository.ErrUnavailable
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var id string
		err = tx.QueryRowContext(ctx, "SELECT id FROM safe_applies WHERE owner_id=? AND request_epoch=? AND request_id=?", c.PrincipalID, in.Command.Epoch, in.Command.RequestID).Scan(&id)
		confirmed := false
		if err == nil {
			out.Transaction = &apitypes.Ref{Kind: "transaction", ID: id}
			s, err := r.fields.SafetyState(ctx, tx, id)
			if err != nil {
				return err
			}
			if s.Reservation != in.Reservation {
				return apitypes.Fail(409, "RESERVATION_MISMATCH")
			}
			if !safety.Terminal(s.State) {
				out.Pending = true
				return nil
			}
			confirmed = s.State == "confirmed"
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = candidateFence(ctx, tx, in.Envelope, false); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM safe_resolutions").Scan(&count); err != nil {
			return err
		}
		if count >= 10000 {
			return apitypes.Fail(429, "SAFE_RESOLUTION_CAPACITY")
		}
		next := in.Envelope
		next.Sequence++
		next.Candidate.Revision = repository.NewID()
		if confirmed {
			next.Candidate.Intents = []plan.StoredIntent{}
			next.Candidate.State = "empty"
			next.Candidate.Consumed = out.Transaction
		} else {
			next.Candidate.State = "staged"
		}
		next.Sign(r.key)
		if err = candidateFence(ctx, tx, next, true); err != nil {
			return err
		}
		out.Envelope = &next
		blob, _ = json.Marshal(out)
		if _, err = tx.ExecContext(ctx, "INSERT INTO safe_resolutions VALUES(?,?,?,?,?)", c.PrincipalID, in.Command.Epoch, in.Command.RequestID, fingerprint, blob); err != nil {
			return err
		}
		_, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Actor: c.PrincipalID, Credential: c.CredentialID, Capability: "workspace.read", Operation: "safe-workspace-resolved", Object: out.Transaction, Result: "resolved", Critical: true, Created: r.now()})
		return err
	})
	return out, err
}

var _ safety.Manager = (*Repository)(nil)
