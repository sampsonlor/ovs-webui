// Package requests owns durable API idempotency within one existing authority
// database. Mutations are SQL-only and commit atomically with their receipt.
package requests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const MaxReceipts = 100000

type Repository struct {
	store  *sqlite.Store
	domain string
	now    func() time.Time
}

func New(store *sqlite.Store) *Repository {
	domain := "management"
	if store.Kind() == repository.Web {
		domain = "workspace"
	}
	return &Repository{store: store, domain: domain, now: time.Now}
}

type Command struct {
	Principal, Epoch, Domain, ID, Operation, Method, URI, Precondition string
	Credential, Capability                                             string
	Payload                                                            json.RawMessage
	Sensitive                                                          bool
	// Supplied by the authority's SecretStore; never accepted from HTTP or
	// persisted in a receipt. A missing key refuses secret-bearing commands.
	FingerprintKey []byte
	SecretResponse bool
}
type Mutation struct {
	Status   int
	Body     json.RawMessage
	Resource *apitypes.Ref
	Job      *apitypes.Ref
	Terminal bool
	// Responses with reusable/one-time secrets are never stored or replayed.
	SecretResponse bool
}

func (r *Repository) Epoch(ctx context.Context) (string, error) {
	var epoch string
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT epoch FROM api_authority WHERE singleton=1").Scan(&epoch)
	})
	return epoch, err
}

// Replay checks an original receipt before preparing new provider-dependent
// work. Authorization runs outside the database read (important for webd IPC).
// Execute still repeats this lookup atomically before admitting any mutation.
func (r *Repository) Replay(ctx context.Context, c Command, authorize func(context.Context) error) (apitypes.Result, bool, error) {
	var out apitypes.Result
	if authorize == nil {
		return out, false, apitypes.Fail(503, "AUTH_UNAVAILABLE")
	}
	if err := authorize(ctx); err != nil {
		return out, false, err
	}
	digest, err := fingerprint(c)
	if err != nil {
		return out, false, err
	}
	err = r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var prior string
		var body, receipt []byte
		if err := q.QueryRowContext(ctx, "SELECT fingerprint,status,response,receipt FROM api_receipts WHERE principal_id=? AND epoch=? AND domain=? AND request_id=?", c.Principal, c.Epoch, c.Domain, c.ID).Scan(&prior, &out.Status, &body, &receipt); err != nil {
			return err
		}
		if digest != prior {
			return apitypes.Fail(409, "IDEMPOTENCY_MISMATCH")
		}
		if json.Unmarshal(receipt, &out.Receipt) != nil {
			return repository.ErrUnavailable
		}
		out.Body = body
		out.Replayed = true
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrNotFound) {
		return apitypes.Result{}, false, nil
	}
	return out, err == nil, err
}
func fingerprint(c Command) (string, error) {
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(c.Payload))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return "", apitypes.Fail(422, "INVALID_REQUEST")
	}
	payload, err := canonicalValue(payload)
	if err != nil {
		return "", apitypes.Fail(422, "INVALID_REQUEST")
	}
	value := []any{c.Operation, c.Method, c.URI, c.Precondition, payload}
	data, err := json.Marshal(value)
	if err != nil {
		return "", apitypes.Fail(422, "INVALID_REQUEST")
	}
	if c.Sensitive {
		if len(c.FingerprintKey) < 32 {
			return "", apitypes.Fail(503, "SECRETSTORE_UNAVAILABLE")
		}
		h := hmac.New(sha256.New, c.FingerprintKey)
		_, _ = h.Write(data)
		return "hmac-sha256:" + hex.EncodeToString(h.Sum(nil)), nil
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Tagged values distinguish numbers from strings/objects while normalizing
// decimal spelling exactly (1, 1.0 and 1e0). Never round 64-bit IDs via float64.
func canonicalValue(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		text := string(v)
		negative := strings.HasPrefix(text, "-")
		text = strings.TrimPrefix(text, "-")
		var exponent int64
		if at := strings.IndexAny(text, "eE"); at >= 0 {
			var err error
			exponent, err = strconv.ParseInt(text[at+1:], 10, 32)
			if err != nil {
				return nil, err
			}
			text = text[:at]
		}
		if at := strings.IndexByte(text, '.'); at >= 0 {
			exponent -= int64(len(text) - at - 1)
			text = text[:at] + text[at+1:]
		}
		text = strings.TrimLeft(text, "0")
		if text == "" {
			text = "0"
			exponent = 0
			negative = false
		} else {
			trimmed := strings.TrimRight(text, "0")
			exponent += int64(len(text) - len(trimmed))
			text = trimmed
		}
		if negative {
			text = "-" + text
		}
		return []any{"number", text + "e" + strconv.FormatInt(exponent, 10)}, nil
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			next, err := canonicalValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = next
		}
		return []any{"object", out}, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			next, err := canonicalValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return []any{"array", out}, nil
	case string:
		return []any{"string", v}, nil
	case bool:
		return []any{"boolean", v}, nil
	case nil:
		return []any{"null"}, nil
	default:
		return nil, errors.New("INVALID_TYPED_PAYLOAD")
	}
}

// Reauthorize must check the current principal, operation AND resource on every
// invocation, including receipt replay. It is not a saved capability snapshot.
func (r *Repository) Execute(ctx context.Context, c Command, reauthorize func(context.Context) error, mutate func(context.Context, *sql.Tx) (Mutation, error)) (apitypes.Result, error) {
	var out apitypes.Result
	keyTime, valid := apitypes.RequestTime(c.ID)
	if !valid || !apitypes.ManagementID(c.Principal) || !apitypes.ManagementID(c.Epoch) || c.Domain != r.domain || c.Operation == "" || len(c.URI) > 4096 || !repository.ValidDocument(c.Payload) {
		return out, apitypes.Fail(422, "INVALID_REQUEST")
	}
	if reauthorize == nil || mutate == nil {
		return out, apitypes.Fail(503, "AUTH_UNAVAILABLE")
	}
	if err := reauthorize(ctx); err != nil {
		return out, err
	}
	digest, err := fingerprint(c)
	if err != nil {
		return out, err
	}
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := reauthorize(ctx); err != nil {
			return err
		}
		var priorDigest string
		var savedReceipt, body []byte
		var status int
		err := tx.QueryRowContext(ctx, "SELECT fingerprint,status,response,receipt FROM api_receipts WHERE principal_id=? AND epoch=? AND domain=? AND request_id=?", c.Principal, c.Epoch, c.Domain, c.ID).Scan(&priorDigest, &status, &body, &savedReceipt)
		if err == nil {
			if priorDigest != digest {
				return apitypes.Fail(409, "IDEMPOTENCY_MISMATCH")
			}
			if json.Unmarshal(savedReceipt, &out.Receipt) != nil {
				return repository.ErrUnavailable
			}
			out.Status, out.Body, out.Replayed = status, append(json.RawMessage(nil), body...), true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var epoch string
		var high int64
		if err = tx.QueryRowContext(ctx, "SELECT epoch,high_watermark_ms FROM api_authority WHERE singleton=1").Scan(&epoch, &high); err != nil {
			return err
		}
		now := r.now()
		if epoch != c.Epoch {
			return apitypes.Fail(410, "REQUEST_EPOCH_CHANGED")
		}
		if now.UnixMilli()+120000 < high {
			return apitypes.Fail(503, "CLOCK_UNSAFE")
		}
		if now.Sub(keyTime) > 5*time.Minute || keyTime.Sub(now) > 2*time.Minute {
			return apitypes.Fail(410, "REQUEST_KEY_EXPIRED")
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM api_receipts").Scan(&count); err != nil {
			return err
		}
		limit := MaxReceipts
		if r.domain == "management" && !evidence.ControlOperation(c.Operation) {
			limit -= evidence.ReservedControlRecords
		}
		if count >= limit {
			return apitypes.Fail(429, "RESOURCE_BUDGET_EXCEEDED")
		}
		correlation := repository.NewID()
		if r.domain == "management" {
			ctx = evidence.WithRequest(ctx, evidence.Request{Principal: c.Principal, Credential: c.Credential, Capability: c.Capability, Operation: c.Operation, Domain: c.Domain, Epoch: c.Epoch, ID: c.ID, Correlation: correlation})
		}
		result, err := mutate(ctx, tx)
		if err != nil {
			return err
		}
		if result.Status < 200 || result.Status > 299 || result.Status == 204 || !json.Valid(result.Body) || len(result.Body) > 65536 {
			return apitypes.Fail(500, "INVALID_SERVICE_RESPONSE")
		}
		if result.Status == 202 && (result.Job == nil || result.Job.Kind != "job" || !apitypes.ManagementID(result.Job.ID) || result.Resource == nil || !apitypes.ManagementID(result.Resource.ID)) {
			return apitypes.Fail(500, "INVALID_SERVICE_RESPONSE")
		}
		if result.Status == 202 {
			if r.domain != "management" {
				return apitypes.Fail(500, "INVALID_SERVICE_RESPONSE")
			}
			var jobID string
			if err = tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=?", result.Job.ID).Scan(&jobID); err != nil {
				return apitypes.Fail(500, "JOB_NOT_DURABLE")
			}
		}
		receipt := apitypes.Receipt{RequestID: c.ID, Domain: c.Domain, Epoch: c.Epoch, State: "accepted", Effect: "unknown", Resource: result.Resource, Job: result.Job, CorrelationID: correlation}
		if result.Resource != nil {
			receipt.Effect = "linked-resource"
		}
		var completed any
		if result.Terminal {
			receipt.State = "completed"
			completed = now.UnixMilli()
		}
		receiptJSON, _ := json.Marshal(receipt)
		if result.Status == 202 {
			result.Body, _ = json.Marshal(apitypes.Accepted{RequestID: c.ID, Domain: c.Domain, Epoch: c.Epoch, JobID: result.Job.ID, Resource: result.Resource, CorrelationID: receipt.CorrelationID})
		}
		storedBody := result.Body
		storedStatus := result.Status
		if c.SecretResponse || result.SecretResponse {
			storedBody = receiptJSON
			storedStatus = 200
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO api_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", c.Principal, c.Epoch, c.Domain, c.ID, digest, result.Status, storedStatus, []byte(storedBody), receiptJSON, now.UnixMilli(), completed, c.Operation); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_authority SET high_watermark_ms=max(high_watermark_ms,?) WHERE singleton=1", now.UnixMilli()); err != nil {
			return err
		}
		out = apitypes.Result{Status: result.Status, Body: append(json.RawMessage(nil), result.Body...), Receipt: receipt}
		return nil
	})
	if err != nil {
		return apitypes.Result{}, err
	}
	return out, nil
}
func (r *Repository) Find(ctx context.Context, principal, epoch, id string, authorize func(context.Context, string, *apitypes.Ref) error) (apitypes.Receipt, error) {
	var receipt apitypes.Receipt
	if !apitypes.ManagementID(principal) || !apitypes.ManagementID(epoch) || !apitypes.UUID(id) {
		return receipt, apitypes.Fail(422, "INVALID_REQUEST")
	}
	if authorize == nil {
		return receipt, apitypes.Fail(503, "AUTH_UNAVAILABLE")
	}
	var operation string
	err := r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		var body []byte
		if err := c.QueryRowContext(ctx, "SELECT receipt,operation FROM api_receipts WHERE principal_id=? AND epoch=? AND domain=? AND request_id=?", principal, epoch, r.domain, id).Scan(&body, &operation); err != nil {
			return err
		}
		return json.Unmarshal(body, &receipt)
	})
	if err != nil {
		return apitypes.Receipt{}, err
	}
	if err = authorize(ctx, operation, receipt.Resource); err != nil {
		return apitypes.Receipt{}, err
	}
	return receipt, nil
}

// Only terminal receipts older than retention can be removed. Unresolved
// receipts are preserved even under pressure; new admission must fail instead.
func (r *Repository) Prune(ctx context.Context) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		query := "DELETE FROM api_receipts WHERE rowid IN (SELECT rowid FROM api_receipts WHERE completed_at_ms IS NOT NULL AND completed_at_ms<?"
		if r.domain == "management" {
			query += ` AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=json_extract(api_receipts.receipt,'$.job_ref.id'))
 AND NOT EXISTS(SELECT 1 FROM evidence_records e WHERE e.request_id=api_receipts.request_id)`
		}
		query += " ORDER BY completed_at_ms LIMIT 256)"
		_, err := tx.ExecContext(ctx, query, r.now().Add(-30*24*time.Hour).UnixMilli())
		return err
	})
}
