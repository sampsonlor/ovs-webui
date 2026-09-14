package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

type Repository struct{ store *sqlite.Store }

func New(store *sqlite.Store) (*Repository, error) {
	if store.Kind() != repository.Web {
		return nil, repository.ErrInvalid
	}
	return &Repository{store: store}, nil
}
func (r *Repository) SaveCandidate(ctx context.Context, id, owner, expected string, document json.RawMessage) (repository.Candidate, error) {
	if !repository.ValidID(id) || !repository.ValidID(owner) || !repository.ValidDocument(document) {
		return repository.Candidate{}, repository.ErrInvalid
	}
	candidate := repository.Candidate{ID: id, OwnerID: owner, Revision: repository.NewID(), Document: append(json.RawMessage(nil), document...)}
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if expected == "" {
			_, err := tx.ExecContext(ctx, "INSERT INTO candidates VALUES(?,?,?,?)", id, owner, candidate.Revision, []byte(document))
			return err
		}
		result, err := tx.ExecContext(ctx, "UPDATE candidates SET revision=?,document=? WHERE id=? AND owner_id=? AND revision=?", candidate.Revision, []byte(document), id, owner, expected)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			return repository.ErrConflict
		}
		return err
	})
	if err != nil {
		return repository.Candidate{}, err
	}
	return candidate, nil
}
func (r *Repository) Candidate(ctx context.Context, id, owner string) (repository.Candidate, error) {
	var c repository.Candidate
	err := r.store.Read(ctx, func(ctx context.Context, db *sql.Conn) error {
		return db.QueryRowContext(ctx, "SELECT id,owner_id,revision,document FROM candidates WHERE id=? AND owner_id=?", id, owner).Scan(&c.ID, &c.OwnerID, &c.Revision, &c.Document)
	})
	return c, err
}
func (r *Repository) PutMetadata(ctx context.Context, m repository.Metadata) error {
	if (m.Kind != repository.Preference && m.Kind != repository.Label && m.Kind != repository.Profile && m.Kind != repository.Override) || !repository.ValidID(m.OwnerID) || !repository.ValidID(m.ID) || !repository.ValidDocument(m.Document) {
		return repository.ErrInvalid
	}
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES(?,?,?,?) ON CONFLICT(kind,owner_id,id) DO UPDATE SET document=excluded.document", m.Kind, m.OwnerID, m.ID, []byte(m.Document))
		return err
	})
}
func (r *Repository) Metadata(ctx context.Context, kind repository.MetadataKind, id, owner string) (repository.Metadata, error) {
	m := repository.Metadata{Kind: kind, ID: id, OwnerID: owner}
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT document FROM metadata WHERE kind=? AND id=? AND owner_id=?", kind, id, owner).Scan(&m.Document)
	})
	return m, err
}
func (r *Repository) SaveSession(ctx context.Context, m repository.SessionMapping) error {
	if m.SessionHash == [32]byte{} || !repository.ValidID(m.PrincipalID) || !repository.ValidID(m.GrantSecretRef) || !m.ExpiresAt.After(time.Now()) {
		return repository.ErrInvalid
	}
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO session_mappings VALUES(?,?,?,?) ON CONFLICT(session_hash) DO UPDATE SET principal_id=excluded.principal_id,grant_secret_ref=excluded.grant_secret_ref,expires_at=excluded.expires_at", m.SessionHash[:], m.PrincipalID, m.GrantSecretRef, m.ExpiresAt.Unix())
		return err
	})
}
func (r *Repository) Session(ctx context.Context, hash [32]byte) (repository.SessionMapping, error) {
	m := repository.SessionMapping{SessionHash: hash}
	var expiry int64
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT principal_id,grant_secret_ref,expires_at FROM session_mappings WHERE session_hash=? AND expires_at>?", hash[:], time.Now().Unix()).Scan(&m.PrincipalID, &m.GrantSecretRef, &expiry)
	})
	m.ExpiresAt = time.Unix(expiry, 0)
	return m, err
}

const handoffColumns = "request_id,transaction_id,correlation_id,owner_id,candidate_id,candidate_revision,payload_hash,document,coalesce(receipt_id,'')"

type scanner interface{ Scan(...any) error }

func scanHandoff(row scanner, h *repository.Handoff) error {
	return row.Scan(&h.RequestID, &h.TransactionID, &h.CorrelationID, &h.OwnerID, &h.CandidateID, &h.CandidateRevision, &h.PayloadHash, &h.Document, &h.ReceiptID)
}

// Persist the immutable draft and retry identity before any IPC send. Repeating
// the same request recovers its original copy even after the live draft changes.
func (r *Repository) PrepareHandoff(ctx context.Context, requestID, id, owner, revision string) (repository.Handoff, error) {
	if !repository.ValidID(requestID) || !repository.ValidID(id) || !repository.ValidID(owner) || !repository.ValidID(revision) {
		return repository.Handoff{}, repository.ErrInvalid
	}
	var h repository.Handoff
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := scanHandoff(tx.QueryRowContext(ctx, "SELECT "+handoffColumns+" FROM handoff_outbox WHERE request_id=?", requestID), &h)
		if err == nil {
			if h.CandidateID != id || h.OwnerID != owner || h.CandidateRevision != revision {
				return repository.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var body []byte
		err = tx.QueryRowContext(ctx, "SELECT document FROM candidates WHERE id=? AND owner_id=? AND revision=?", id, owner, revision).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			return repository.ErrConflict
		}
		if err != nil {
			return err
		}
		var normalized bytes.Buffer
		if err = json.Compact(&normalized, body); err != nil {
			return err
		}
		sum := sha256.Sum256(normalized.Bytes())
		h = repository.Handoff{RequestID: requestID, TransactionID: repository.NewID(), CorrelationID: repository.NewID(), OwnerID: owner, CandidateID: id, CandidateRevision: revision, PayloadHash: hex.EncodeToString(sum[:]), Document: append(json.RawMessage(nil), normalized.Bytes()...)}
		_, err = tx.ExecContext(ctx, "INSERT INTO handoff_outbox(request_id,transaction_id,correlation_id,owner_id,candidate_id,candidate_revision,payload_hash,document,created_at) VALUES(?,?,?,?,?,?,?,?,?)", h.RequestID, h.TransactionID, h.CorrelationID, h.OwnerID, h.CandidateID, h.CandidateRevision, h.PayloadHash, []byte(h.Document), time.Now().Unix())
		return err
	})
	if err != nil {
		return repository.Handoff{}, err
	}
	return h, nil
}
func (r *Repository) Pending(ctx context.Context, limit int) ([]repository.Handoff, error) {
	if limit < 1 || limit > 8 {
		return nil, repository.ErrInvalid
	}
	result := []repository.Handoff{}
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		rows, err := c.QueryContext(ctx, "SELECT "+handoffColumns+" FROM handoff_outbox WHERE receipt_id IS NULL ORDER BY created_at,request_id LIMIT ?", limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h repository.Handoff
			if err = scanHandoff(rows, &h); err != nil {
				return err
			}
			result = append(result, h)
		}
		return rows.Err()
	})
	return result, err
}

// Only a receipt read from the authorized manager channel may be supplied here.
// A transport receipt does not delete drafts or imply Applied/Confirmed.
func (r *Repository) Acknowledge(ctx context.Context, receipt repository.Receipt) error {
	if receipt.Stage != "received" || !repository.ValidID(receipt.ID) {
		return repository.ErrInvalid
	}
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var h repository.Handoff
		if err := scanHandoff(tx.QueryRowContext(ctx, "SELECT "+handoffColumns+" FROM handoff_outbox WHERE request_id=?", receipt.RequestID), &h); err != nil {
			return err
		}
		if receipt.TransactionID != h.TransactionID || receipt.CorrelationID != h.CorrelationID || receipt.PayloadHash != h.PayloadHash || (h.ReceiptID != "" && h.ReceiptID != receipt.ID) {
			return repository.ErrConflict
		}
		if h.ReceiptID == receipt.ID {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_receipts VALUES(?,?,?)", h.RequestID, receipt.ID, time.Now().Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE handoff_outbox SET receipt_id=? WHERE request_id=?", receipt.ID, h.RequestID)
		return err
	})
}
