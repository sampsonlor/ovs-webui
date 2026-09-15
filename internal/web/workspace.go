package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

type Workspace struct {
	store    *sqlite.Store
	receipts *requests.Repository
	auth     authn.Manager
	manager  candidate.Manager
}

func NewWorkspace(store *sqlite.Store, manager authn.Manager) (*Workspace, error) {
	domain, ok := manager.(candidate.Manager)
	if store.Kind() != repository.Web || !ok {
		return nil, repository.ErrInvalid
	}
	return &Workspace{store: store, receipts: requests.New(store), auth: manager, manager: domain}, nil
}
func loadWorkspace(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner string) (candidate.Envelope, error) {
	var e candidate.Envelope
	var b []byte
	if err := q.QueryRowContext(ctx, "SELECT envelope FROM candidate_workspaces WHERE owner_id=?", owner).Scan(&b); err != nil {
		return e, err
	}
	if json.Unmarshal(b, &e) != nil || e.Owner != owner {
		return e, repository.ErrUnavailable
	}
	return e, nil
}
func (w *Workspace) envelope(ctx context.Context, owner string) (candidate.Envelope, error) {
	var e candidate.Envelope
	if !apitypes.ManagementID(owner) {
		return e, apitypes.Fail(401, "UNAUTHENTICATED")
	}
	err := w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		e, err = loadWorkspace(ctx, tx, owner)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM candidate_workspaces").Scan(&count); err != nil {
			return err
		}
		if count >= 10000 {
			return apitypes.Fail(429, "WORKSPACE_CAPACITY_REACHED")
		}
		e = candidate.Envelope{Owner: owner, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), State: "empty", Intents: []candidate.StoredIntent{}}}
		if err = tx.QueryRowContext(ctx, "SELECT epoch FROM api_authority WHERE singleton=1").Scan(&e.Epoch); err != nil {
			return err
		}
		b, _ := json.Marshal(e)
		_, err = tx.ExecContext(ctx, "INSERT INTO candidate_workspaces VALUES(?,?,?,?,?)", owner, e.Candidate.ID, e.Candidate.Revision, e.Sequence, b)
		return err
	})
	return e, err
}
func (w *Workspace) authorize(ctx context.Context, s publicapi.Subject, cap string) error {
	for _, capability := range []string{cap, "configuration.read", "inventory.read"} {
		c, err := w.auth.CheckAuth(ctx, s.Credential, authn.Check{Capability: capability})
		if err != nil {
			return err
		}
		if c.PrincipalID != s.ID {
			return apitypes.Fail(401, "SESSION_INVALID")
		}
	}
	return nil
}
func (w *Workspace) Read(ctx context.Context, s publicapi.Subject, q publicapi.Query) (publicapi.Response, error) {
	var out publicapi.Response
	cap := "workspace.read"
	if q.Operation.ID == "readValidation" {
		cap = "config.validate"
	}
	if err := w.authorize(ctx, s, cap); err != nil {
		return out, err
	}
	if q.Operation.ID == "readRequestReceipt" {
		epoch := q.Values.Get("epoch")
		if epoch == "" {
			var err error
			epoch, err = w.receipts.Epoch(ctx)
			if err != nil {
				return out, err
			}
		}
		receipt, err := w.receipts.Find(ctx, s.ID, epoch, q.Path["request_id"], func(ctx context.Context, op string, ref *apitypes.Ref) error {
			if op != "changeCandidate" || ref == nil || ref.Kind != "candidate" {
				return apitypes.Fail(403, "RESOURCE_DENIED")
			}
			return w.authorize(ctx, s, "config.stage")
		})
		if errors.Is(err, sql.ErrNoRows) {
			err = apitypes.Fail(404, "NOT_FOUND")
		}
		if err != nil {
			return out, err
		}
		out.Status = 200
		out.Body, err = json.Marshal(receipt)
		return out, err
	}
	e, err := w.envelope(ctx, s.ID)
	if err != nil {
		return out, err
	}
	response, err := w.manager.ReadCandidate(ctx, s.Credential, candidate.ReadRequest{Envelope: e, ValidationID: q.Path["validation_id"]})
	if err != nil {
		return out, err
	}
	if q.Operation.ID == "readWorkspace" {
		claims, err := w.auth.InspectAuth(ctx, s.Credential)
		if err != nil {
			return out, err
		}
		var latest *apitypes.Ref
		err = w.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
			var b []byte
			err := c.QueryRowContext(ctx, "SELECT receipt FROM candidate_validation_outbox WHERE owner_id=? AND receipt IS NOT NULL ORDER BY created_at_ms DESC,request_id DESC LIMIT 1", s.ID).Scan(&b)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			var r apitypes.Receipt
			if json.Unmarshal(b, &r) != nil {
				return repository.ErrUnavailable
			}
			latest = r.Resource
			return nil
		})
		if err != nil {
			return out, err
		}
		response.Body, err = json.Marshal(map[string]any{"server_time": time.Now().UTC(), "request_epochs": map[string]string{"workspace": e.Epoch, "management": claims.RequestEpoch}, "candidate": response.Body, "latest_validation": latest, "latest_transaction": nil, "gates": []candidate.Gate{{Code: "APPLY_SERVICE_UNAVAILABLE", State: "blocked", Reason: "APPLY_SERVICE_UNAVAILABLE"}}})
		if err != nil {
			return out, err
		}
	}
	return publicapi.Response{Status: response.Status, Body: response.Body, ETag: response.ETag}, nil
}
func authCommand(c requests.Command) authn.Command {
	return authn.Command{Method: c.Method, URI: c.URI, Epoch: c.Epoch, RequestID: c.ID, Precondition: c.Precondition, Payload: c.Payload}
}
func (w *Workspace) Execute(ctx context.Context, s publicapi.Subject, q publicapi.Query, c requests.Command) (apitypes.Result, error) {
	var out apitypes.Result
	cap := "config.stage"
	if q.Operation.ID == "createValidation" {
		cap = "config.validate"
	}
	authorize := func(ctx context.Context) error { return w.authorize(ctx, s, cap) }
	if err := authorize(ctx); err != nil {
		return out, err
	}
	if q.Operation.ID == "createValidation" {
		return w.validate(ctx, s, c)
	}
	if q.Operation.ID != "changeCandidate" || c.Domain != "workspace" || c.Principal != s.ID {
		return out, apitypes.Fail(403, "OPERATION_DENIED")
	}
	if prior, found, err := w.receipts.Replay(ctx, c, authorize); err != nil || found {
		return prior, err
	}
	e, err := w.envelope(ctx, s.ID)
	if err != nil {
		return out, err
	}
	prepared, err := w.manager.PrepareCandidate(ctx, s.Credential, candidate.PrepareRequest{Envelope: e, Command: authCommand(c)})
	if err != nil {
		return out, err
	}
	if prepared.Owner != s.ID || prepared.Epoch != e.Epoch || prepared.Candidate.ID != e.Candidate.ID || prepared.Sequence != e.Sequence+1 || candidate.Budget(prepared) != nil {
		return out, apitypes.Fail(503, "CANDIDATE_RESPONSE_INVALID")
	}
	if err = authorize(ctx); err != nil {
		return out, err
	}
	// All IPC authorization/preparation completes before opening web.db's SQL
	// transaction. A draft is user intent; mgrd independently reauthorizes every
	// validation. No cross-database transaction or cached permission grants writes.
	out, err = w.receipts.Execute(ctx, c, func(context.Context) error { return nil }, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		current, err := loadWorkspace(ctx, tx, s.ID)
		if err != nil {
			return requests.Mutation{}, err
		}
		if current.Candidate.Revision != e.Candidate.Revision || c.Precondition != `"`+current.Candidate.Revision+`"` {
			return requests.Mutation{}, apitypes.Fail(412, "ETAG_MISMATCH")
		}
		blob, _ := json.Marshal(prepared)
		body, _ := json.Marshal(prepared.Candidate)
		if _, err = tx.ExecContext(ctx, "UPDATE candidate_workspaces SET revision=?,sequence=?,envelope=? WHERE owner_id=? AND revision=?", prepared.Candidate.Revision, prepared.Sequence, blob, s.ID, e.Candidate.Revision); err != nil {
			return requests.Mutation{}, err
		}
		return requests.Mutation{Status: 200, Body: body, Resource: &apitypes.Ref{Kind: "candidate", ID: e.Candidate.ID}, Terminal: true}, nil
	})
	if err == nil {
		_, _ = w.manager.ReadCandidate(ctx, s.Credential, candidate.ReadRequest{Envelope: prepared})
	}
	return out, err
}
func (w *Workspace) validate(ctx context.Context, s publicapi.Subject, c requests.Command) (apitypes.Result, error) {
	var out apitypes.Result
	if c.Domain != "management" || c.Principal != s.ID {
		return out, apitypes.Fail(403, "OPERATION_DENIED")
	}
	var in candidate.ValidateRequest
	command := authCommand(c)
	fingerprint := candidate.Digest(command)
	err := w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var prior string
		var envelope, blob []byte
		err := tx.QueryRowContext(ctx, "SELECT fingerprint,envelope,command FROM candidate_validation_outbox WHERE owner_id=? AND request_epoch=? AND request_id=?", s.ID, c.Epoch, c.ID).Scan(&prior, &envelope, &blob)
		if err == nil {
			if prior != fingerprint {
				return apitypes.Fail(409, "IDEMPOTENCY_MISMATCH")
			}
			if json.Unmarshal(envelope, &in.Envelope) != nil || json.Unmarshal(blob, &in.Command) != nil {
				return repository.ErrUnavailable
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		in.Envelope, err = loadWorkspace(ctx, tx, s.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return apitypes.Fail(409, "CANDIDATE_CHANGED")
		}
		if err != nil {
			return err
		}
		var request struct {
			ID       string `json:"candidate_id"`
			Revision string `json:"candidate_revision"`
		}
		if json.Unmarshal(c.Payload, &request) != nil || request.ID != in.Envelope.Candidate.ID || request.Revision != in.Envelope.Candidate.Revision {
			return apitypes.Fail(409, "CANDIDATE_CHANGED")
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM candidate_validation_outbox").Scan(&count); err != nil {
			return err
		}
		if count >= 10000 {
			return apitypes.Fail(429, "VALIDATION_OUTBOX_FULL")
		}
		in.Command = command
		envelope, _ = json.Marshal(in.Envelope)
		blob, _ = json.Marshal(command)
		_, err = tx.ExecContext(ctx, "INSERT INTO candidate_validation_outbox VALUES(?,?,?,?,?,?,NULL,?)", s.ID, c.Epoch, c.ID, fingerprint, envelope, blob, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		return out, err
	}
	// Advance mgrd's witness using the current draft even when recovering an
	// older outbox entry. Existing receipts replay; an unadmitted old draft fails.
	current, err := w.envelope(ctx, s.ID)
	if err != nil {
		return out, err
	}
	if _, err = w.manager.ReadCandidate(ctx, s.Credential, candidate.ReadRequest{Envelope: current}); err != nil {
		return out, err
	}
	out, err = w.manager.ValidateCandidate(ctx, s.Credential, in)
	if err != nil {
		return out, err
	}
	if out.Receipt.RequestID != c.ID || out.Receipt.Epoch != c.Epoch || out.Receipt.Domain != "management" || out.Receipt.Resource == nil || out.Receipt.Resource.Kind != "validation" {
		return apitypes.Result{}, apitypes.Fail(503, "VALIDATION_RESPONSE_INVALID")
	}
	// Acknowledgement is only a link to manager authority. A failed local ack
	// leaves the original outbox entry available for receipt recovery.
	err = w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		b, _ := json.Marshal(out.Receipt)
		_, err := tx.ExecContext(ctx, "UPDATE candidate_validation_outbox SET receipt=? WHERE owner_id=? AND request_epoch=? AND request_id=?", b, s.ID, c.Epoch, c.ID)
		return err
	})
	return out, err
}
