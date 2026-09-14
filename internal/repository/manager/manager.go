package manager

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
	if store.Kind() != repository.Manager {
		return nil, repository.ErrInvalid
	}
	return &Repository{store: store}, nil
}
func (r *Repository) PutPrincipal(ctx context.Context, p repository.Principal) error {
	if !repository.ValidID(p.ID) || !repository.ValidID(p.Name) || len(p.PasswordVerifier) > 4096 {
		return repository.ErrInvalid
	}
	verifier := p.PasswordVerifier
	if verifier == nil {
		verifier = []byte{}
	}
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO principals VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,disabled=excluded.disabled,password_verifier=excluded.password_verifier", p.ID, p.Name, p.Disabled, verifier)
		return err
	})
}
func (r *Repository) Principal(ctx context.Context, id string) (repository.Principal, error) {
	var p repository.Principal
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT id,name,disabled,password_verifier FROM principals WHERE id=?", id).Scan(&p.ID, &p.Name, &p.Disabled, &p.PasswordVerifier)
	})
	return p, err
}

const receiptColumns = "id,request_id,transaction_id,correlation_id,payload_hash,stage"

type scanner interface{ Scan(...any) error }

func scanReceipt(row scanner, r *repository.Receipt) error {
	return row.Scan(&r.ID, &r.RequestID, &r.TransactionID, &r.CorrelationID, &r.PayloadHash, &r.Stage)
}
func (r *Repository) Receipt(ctx context.Context, requestID string) (repository.Receipt, error) {
	var receipt repository.Receipt
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return scanReceipt(c.QueryRowContext(ctx, "SELECT "+receiptColumns+" FROM request_receipts WHERE request_id=?", requestID), &receipt)
	})
	return receipt, err
}

// Receive records immutable transport evidence in one manager transaction. It
// grants no execution authority; #34/#38/#39 perform authorization, validation
// and admission before any provider call. No IPC operation exposes this yet.
func (r *Repository) Receive(ctx context.Context, h repository.Handoff) (repository.Receipt, error) {
	for _, id := range []string{h.RequestID, h.TransactionID, h.CorrelationID, h.OwnerID, h.CandidateID, h.CandidateRevision} {
		if !repository.ValidID(id) {
			return repository.Receipt{}, repository.ErrInvalid
		}
	}
	if !repository.ValidDocument(h.Document) {
		return repository.Receipt{}, repository.ErrInvalid
	}
	var normalized bytes.Buffer
	if json.Compact(&normalized, h.Document) != nil {
		return repository.Receipt{}, repository.ErrInvalid
	}
	sum := sha256.Sum256(normalized.Bytes())
	if h.PayloadHash != hex.EncodeToString(sum[:]) {
		return repository.Receipt{}, repository.ErrInvalid
	}
	var result repository.Receipt
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var owner, candidate, revision string
		err := tx.QueryRowContext(ctx, "SELECT "+receiptColumns+",owner_id,candidate_id,candidate_revision FROM request_receipts WHERE request_id=?", h.RequestID).Scan(&result.ID, &result.RequestID, &result.TransactionID, &result.CorrelationID, &result.PayloadHash, &result.Stage, &owner, &candidate, &revision)
		if err == nil {
			if result.TransactionID != h.TransactionID || result.CorrelationID != h.CorrelationID || result.PayloadHash != h.PayloadHash || owner != h.OwnerID || candidate != h.CandidateID || revision != h.CandidateRevision {
				return repository.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result = repository.Receipt{ID: repository.NewID(), RequestID: h.RequestID, TransactionID: h.TransactionID, CorrelationID: h.CorrelationID, PayloadHash: h.PayloadHash, Stage: "received"}
		now := time.Now().Unix()
		if _, err = tx.ExecContext(ctx, "INSERT INTO request_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)", result.ID, h.RequestID, h.TransactionID, h.CorrelationID, h.OwnerID, h.CandidateID, h.CandidateRevision, h.PayloadHash, normalized.Bytes(), result.Stage, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO transaction_journal VALUES(?,1,'received',?)", h.TransactionID, []byte(`{"execution_authorized":false}`)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO jobs VALUES(?,?,'pending-validation',?)", repository.NewID(), h.TransactionID, []byte(`{}`)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO audit VALUES(?,?,'handoff-received',?)", repository.NewID(), h.RequestID, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO events VALUES(?,?,'handoff-received',?)", repository.NewID(), h.CorrelationID, now)
		return err
	})
	if err != nil {
		return repository.Receipt{}, err
	}
	return result, nil
}
