package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
)

type workspaceLease struct {
	w       *Workspace
	subject publicapi.Subject
	safety  execution.Lease
}

// ReserveExecution is an internal coordinator primitive, not a public route.
// Caller must acquire an independent safety guard first. Neither a validation
// nor a timeout authorizes a write or releases this persistent reservation.
func (w *Workspace) ReserveExecution(ctx context.Context, s publicapi.Subject, in execution.Request, safety execution.Lease) (execution.Lease, error) {
	if safety == nil {
		return nil, apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
	}
	if _, valid := apitypes.RequestTime(in.ID); !valid || !apitypes.ManagementID(in.ValidationID) || in.Envelope.Owner != s.ID {
		return nil, apitypes.Fail(422, "INVALID_EXECUTION")
	}
	if err := w.authorize(ctx, s, "config.apply"); err != nil {
		return nil, err
	}
	if err := safety.Check(ctx, in); err != nil {
		return nil, err
	}
	// Mgrd authenticates the envelope and advances the monotonic witness before
	// web.db's compare-and-freeze. A concurrent edit wins either this CAS or the
	// later exact witness check; it cannot silently replace execution intent.
	response, err := w.manager.ReadCandidate(ctx, s.Credential, candidate.ReadRequest{Envelope: in.Envelope, ValidationID: in.ValidationID})
	if err != nil {
		return nil, err
	}
	var validation candidate.Validation
	if json.Unmarshal(response.Body, &validation) != nil || validation.ID != in.ValidationID || validation.CandidateID != in.Envelope.Candidate.ID || validation.CandidateRevision != in.Envelope.Candidate.Revision {
		return nil, apitypes.Fail(503, "VALIDATION_RESPONSE_INVALID")
	}
	if !validation.Usable || validation.State != "passed" {
		return nil, apitypes.Fail(409, "VALIDATION_NOT_USABLE")
	}
	err = w.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		current, err := loadWorkspace(ctx, tx, s.ID)
		if err != nil {
			return err
		}
		if candidate.Digest(current) != candidate.Digest(in.Envelope) {
			return apitypes.Fail(409, "CANDIDATE_CHANGED")
		}
		var id, validation, digest string
		err = tx.QueryRowContext(ctx, "SELECT request_id,validation_id,envelope_hash FROM candidate_execution_reservations WHERE owner_id=?", s.ID).Scan(&id, &validation, &digest)
		if err == nil {
			if id != in.ID || validation != in.ValidationID || digest != candidate.Digest(in.Envelope) {
				return apitypes.Fail(409, "CANDIDATE_RESERVED")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO candidate_execution_reservations VALUES(?,?,?,?,?)", s.ID, in.ID, in.ValidationID, candidate.Digest(in.Envelope), time.Now().UnixMilli())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &workspaceLease{w: w, subject: s, safety: safety}, nil
}
func (l *workspaceLease) Check(ctx context.Context, in execution.Request) error {
	if in.Envelope.Owner != l.subject.ID {
		return apitypes.Fail(403, "RESOURCE_DENIED")
	}
	if err := l.w.authorize(ctx, l.subject, "config.apply"); err != nil {
		return err
	}
	if err := l.safety.Check(ctx, in); err != nil {
		return err
	}
	return l.w.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		current, err := loadWorkspace(ctx, q, l.subject.ID)
		if err != nil {
			return err
		}
		var id, validation, digest string
		if err = q.QueryRowContext(ctx, "SELECT request_id,validation_id,envelope_hash FROM candidate_execution_reservations WHERE owner_id=?", l.subject.ID).Scan(&id, &validation, &digest); err != nil {
			return apitypes.Fail(409, "WORKSPACE_RESERVATION_REQUIRED")
		}
		if id != in.ID || validation != in.ValidationID || digest != candidate.Digest(current) || digest != candidate.Digest(in.Envelope) {
			return apitypes.Fail(409, "WORKSPACE_RESERVATION_CHANGED")
		}
		return nil
	})
}
