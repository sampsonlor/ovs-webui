package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

func (w *Workspace) applySafely(ctx context.Context, s publicapi.Subject, c requests.Command) (apitypes.Result, error) {
	var out apitypes.Result
	manager, ok := w.manager.(safety.Manager)
	if !ok {
		return out, apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
	}
	if c.Domain != "management" || c.Principal != s.ID {
		return out, apitypes.Fail(403, "OPERATION_DENIED")
	}
	if err := w.authorize(ctx, s, "config.apply"); err != nil {
		return out, err
	}
	command := authCommand(c)
	fingerprint := candidate.Digest(command)
	var in safety.Request
	err := w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var prior string
		var blob []byte
		err := tx.QueryRowContext(ctx, "SELECT fingerprint,document FROM safe_apply_outbox WHERE owner_id=? AND request_epoch=? AND request_id=?", s.ID, c.Epoch, c.ID).Scan(&prior, &blob)
		if err == nil {
			if prior != fingerprint {
				return apitypes.Fail(409, "IDEMPOTENCY_MISMATCH")
			}
			if json.Unmarshal(blob, &in) != nil {
				return repository.ErrUnavailable
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		envelope, err := loadWorkspace(ctx, tx, s.ID)
		if err != nil {
			return err
		}
		var body struct {
			Candidate  string `json:"candidate_id"`
			Revision   string `json:"candidate_revision"`
			Validation string `json:"validation_id"`
			Mode       string `json:"mode"`
		}
		if json.Unmarshal(c.Payload, &body) != nil || body.Candidate != envelope.Candidate.ID || body.Revision != envelope.Candidate.Revision {
			return apitypes.Fail(409, "CANDIDATE_CHANGED")
		}
		if body.Mode != "safe-apply" {
			return apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM candidate_execution_reservations WHERE owner_id=?", s.ID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return apitypes.Fail(409, "CANDIDATE_RESERVED")
		}
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM safe_apply_outbox").Scan(&count); err != nil {
			return err
		}
		if count >= 10000 {
			return apitypes.Fail(429, "SAFE_APPLY_OUTBOX_FULL")
		}
		in = safety.Request{Envelope: envelope, Command: command, Reservation: repository.NewID()}
		blob, _ = json.Marshal(in)
		if _, err = tx.ExecContext(ctx, "INSERT INTO candidate_execution_reservations VALUES(?,?,?,?,?)", s.ID, c.ID, body.Validation, candidate.Digest(envelope), time.Now().UnixMilli()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO safe_apply_outbox VALUES(?,?,?,?,?,?,NULL,0,?)", s.ID, c.Epoch, c.ID, fingerprint, in.Reservation, blob, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		return out, err
	}
	out, err = manager.AdmitSafeApply(ctx, s.Credential, in)
	if err != nil {
		return out, err
	} // Recovery resolves the ORIGINAL outbox; never silently resends.
	if out.Receipt.RequestID != c.ID || out.Receipt.Epoch != c.Epoch || out.Receipt.Resource == nil || out.Receipt.Resource.Kind != "transaction" {
		return apitypes.Result{}, apitypes.Fail(503, "TRANSACTION_RESPONSE_INVALID")
	}
	err = w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		b, _ := json.Marshal(out.Receipt)
		_, err := tx.ExecContext(ctx, "UPDATE safe_apply_outbox SET receipt=? WHERE owner_id=? AND request_epoch=? AND request_id=?", b, s.ID, c.Epoch, c.ID)
		return err
	})
	return out, err
}

// Reads recover only manager authority, not an execution retry. A signed
// successor is persisted by mgrd before web.db CAS, making either crash safe.
func (w *Workspace) recoverSafeWorkspace(ctx context.Context, s publicapi.Subject) error {
	manager, ok := w.manager.(safety.Manager)
	if !ok {
		return nil
	}
	var in safety.Request
	waiting := false
	err := w.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var b []byte
		var receipt []byte
		var created int64
		if err := q.QueryRowContext(ctx, "SELECT document,receipt,created_at_ms FROM safe_apply_outbox WHERE owner_id=? AND resolved=0", s.ID).Scan(&b, &receipt, &created); err != nil {
			return err
		}
		waiting = len(receipt) == 0 && time.Now().UnixMilli()-created < 10000
		if json.Unmarshal(b, &in) != nil {
			return repository.ErrUnavailable
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if waiting {
		return nil
	}
	out, err := manager.ResolveSafeApply(ctx, s.Credential, in)
	if err != nil || out.Pending {
		return err
	}
	next := out.Envelope
	if next == nil || next.Owner != s.ID || next.Sequence != in.Envelope.Sequence+1 || next.Epoch != in.Envelope.Epoch || next.Seal == "" || candidate.Budget(*next) != nil {
		return apitypes.Fail(503, "WORKSPACE_RESOLUTION_INVALID")
	}
	if out.Confirmed {
		if out.Transaction == nil || !apitypes.ManagementID(next.Candidate.ID) || next.Candidate.ID == in.Envelope.Candidate.ID || next.Candidate.Consumed != nil || len(next.Candidate.Intents) != 0 || next.Candidate.State != "empty" {
			return apitypes.Fail(503, "WORKSPACE_RESOLUTION_INVALID")
		}
	} else if next.Candidate.ID != in.Envelope.Candidate.ID {
		return apitypes.Fail(503, "WORKSPACE_RESOLUTION_INVALID")
	}
	return w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		current, err := loadWorkspace(ctx, tx, s.ID)
		if err != nil {
			return err
		}
		if candidate.Digest(current) != candidate.Digest(in.Envelope) {
			if candidate.Digest(current) == candidate.Digest(*next) {
				return nil
			}
			return apitypes.Fail(409, "WORKSPACE_RESERVATION_CHANGED")
		}
		b, _ := json.Marshal(next)
		if _, err = tx.ExecContext(ctx, "UPDATE candidate_workspaces SET candidate_id=?,revision=?,sequence=?,envelope=? WHERE owner_id=?", next.Candidate.ID, next.Candidate.Revision, next.Sequence, b, s.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM candidate_execution_reservations WHERE owner_id=? AND request_id=?", s.ID, in.Command.RequestID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE safe_apply_outbox SET resolved=1 WHERE owner_id=? AND request_epoch=? AND request_id=?", s.ID, in.Command.Epoch, in.Command.RequestID)
		return err
	})
}
