package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

var etagPattern = regexp.MustCompile(`^"[A-Za-z0-9_-]{1,128}"$`)

type pageCursor struct {
	After     string `json:"after"`
	Principal string `json:"principal"`
	Revision  string `json:"revision"`
	Operation string `json:"operation"`
	Filter    string `json:"filter"`
	Limit     int    `json:"limit"`
	Snapshot  string `json:"snapshot"`
	Expires   int64  `json:"expires"`
}

func (r *Repository) ReadAuth(ctx context.Context, credential string, input authn.Query) (authn.Response, error) {
	var out authn.Response
	op, path, query, err := r.operation(input.Method, input.URI)
	if err != nil {
		return out, err
	}
	if input.Method != "GET" || op.Domain != "" {
		return out, apitypes.Fail(403, "OPERATION_DENIED")
	}
	if err = op.ValidateParameters(path, query, func(string) []string { return nil }); err != nil {
		return out, err
	}
	if op.ID == "listCertificates" || op.ID == "readCertificate" {
		if _, err = r.TLSState(ctx); err != nil {
			return out, err
		}
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		c, err := r.checkOperation(ctx, tx, credential, op, refsFor(op, path), false)
		if err != nil {
			return err
		}
		var value any
		switch op.ID {
		case "readSession":
			value = c // Private claims; webd adds its CSRF and workspace epoch.
		case "listUsers", "listRoles", "listTokens", "listCertificates":
			value, err = r.list(ctx, tx, c, op, query)
		case "readUser":
			value, err = r.user(ctx, tx, path["user_id"])
		case "readRole":
			value, err = r.role(ctx, tx, path["role_id"])
		case "readToken":
			value, err = r.token(ctx, tx, path["token_id"])
		case "readCertificate":
			value, err = r.certificate(ctx, tx, path["certificate_id"])
		case "readJob":
			value, err = r.job(ctx, tx, c, path["job_id"])
		case "readRequestReceipt":
			value, err = r.receipt(ctx, tx, c, path["request_id"], query)
		default:
			return apitypes.Fail(503, "DOMAIN_SERVICE_UNAVAILABLE")
		}
		if errors.Is(err, sql.ErrNoRows) {
			return apitypes.Fail(404, "NOT_FOUND")
		}
		if err != nil {
			return err
		}
		out.Status = 200
		out.Body, err = json.Marshal(value)
		if err != nil {
			return err
		}
		if len(out.Body) > 48<<10 {
			return apitypes.Fail(429, "RESPONSE_BUDGET_EXCEEDED")
		}
		if op.ID != "readSession" {
			if err = op.ValidateResponse(out.Status, out.Body); err != nil {
				return err
			}
		}
		return r.touch(ctx, tx, credential, c)
	})
	if err != nil {
		return authn.Response{}, err
	}
	return out, nil
}
func (r *Repository) user(ctx context.Context, q querier, id string) (map[string]any, error) {
	var name, revision string
	var disabled bool
	if err := q.QueryRowContext(ctx, "SELECT p.name,p.disabled,a.revision FROM principals p JOIN auth_principals a ON a.principal_id=p.id WHERE p.id=?", id).Scan(&name, &disabled, &revision); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=? ORDER BY role_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []string{}
	for rows.Next() {
		var role string
		if err = rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "username": name, "disabled": disabled, "revision": revision, "role_ids": roles}, nil
}
func (r *Repository) role(ctx context.Context, q querier, id string) (map[string]any, error) {
	var name, revision string
	var blob []byte
	var caps []string
	err := q.QueryRowContext(ctx, "SELECT a.name,a.revision,r.capabilities FROM roles r JOIN auth_roles a ON a.role_id=r.id WHERE r.id=?", id).Scan(&name, &revision, &blob)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(blob, &caps) != nil || validateCapabilities(caps) != nil {
		return nil, apitypes.Fail(503, "AUTH_POLICY_INVALID")
	}
	return map[string]any{"id": id, "name": name, "revision": revision, "capabilities": caps}, nil
}
func (r *Repository) token(ctx context.Context, q querier, id string) (map[string]any, error) {
	var name, owner string
	var blob []byte
	var scopes []string
	var expires, last int64
	var revoked sql.NullInt64
	err := q.QueryRowContext(ctx, "SELECT a.name,t.scopes,t.expires_at,t.revoked_at,a.last_used_at,t.principal_id FROM api_tokens t JOIN auth_tokens a ON a.token_hash=t.token_hash WHERE a.id=?", id).Scan(&name, &blob, &expires, &revoked, &last, &owner)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(blob, &scopes) != nil {
		return nil, apitypes.Fail(503, "AUTH_POLICY_INVALID")
	}
	var lastUsed any
	if last != 0 {
		lastUsed = time.Unix(last, 0).UTC()
	}
	return map[string]any{"id": id, "name": name, "scopes": scopes, "expires_at": time.Unix(expires, 0).UTC(), "revoked": revoked.Valid, "principal_id": owner, "last_used_at": lastUsed}, nil
}
func (r *Repository) list(ctx context.Context, tx *sql.Tx, c authn.Claims, op *apicontract.Operation, query url.Values) (map[string]any, error) {
	limit := 100
	if query.Get("limit") != "" {
		limit, _ = strconv.Atoi(query.Get("limit"))
	}
	filter := query.Get("filter")
	cur := pageCursor{Principal: c.PrincipalID, Revision: c.Revision, Operation: op.ID, Filter: filter, Limit: limit, Snapshot: repository.NewID(), Expires: r.now().Add(30 * time.Second).Unix()}
	if value := query.Get("cursor"); value != "" {
		envelope, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return nil, apitypes.Fail(410, "CURSOR_INVALID")
		}
		plain, err := authn.Unseal(r.key, envelope, []byte("security-list-v1"))
		if err != nil || json.Unmarshal(plain, &cur) != nil {
			return nil, apitypes.Fail(410, "CURSOR_INVALID")
		}
		if cur.Principal != c.PrincipalID || cur.Revision != c.Revision || cur.Operation != op.ID || cur.Filter != filter || cur.Limit != limit || cur.Expires <= r.now().Unix() {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
	}
	var rows *sql.Rows
	var err error
	switch op.ID {
	case "listUsers":
		rows, err = tx.QueryContext(ctx, "SELECT p.id FROM principals p JOIN auth_principals a ON a.principal_id=p.id WHERE p.id>? AND instr(lower(p.name),lower(?))>0 ORDER BY p.id LIMIT ?", cur.After, filter, limit+1)
	case "listRoles":
		rows, err = tx.QueryContext(ctx, "SELECT role_id FROM auth_roles WHERE role_id>? AND instr(lower(name),lower(?))>0 ORDER BY role_id LIMIT ?", cur.After, filter, limit+1)
	case "listCertificates":
		rows, err = tx.QueryContext(ctx, "SELECT id FROM tls_certificates WHERE id>? AND instr(lower(descriptor),lower(?))>0 ORDER BY id LIMIT ?", cur.After, filter, limit+1)
	case "listTokens":
		all := slices.Contains(c.Capabilities, "access.tokens.manage")
		rows, err = tx.QueryContext(ctx, "SELECT a.id FROM auth_tokens a JOIN api_tokens t ON t.token_hash=a.token_hash WHERE a.id>? AND instr(lower(a.name),lower(?))>0 AND (? OR t.principal_id=?) ORDER BY a.id LIMIT ?", cur.After, filter, all, c.PrincipalID, limit+1)
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	size := 0
	for _, id := range ids {
		if len(items) >= limit {
			break
		}
		var item map[string]any
		switch op.ID {
		case "listUsers":
			item, err = r.user(ctx, tx, id)
		case "listRoles":
			item, err = r.role(ctx, tx, id)
		case "listCertificates":
			item, err = r.certificate(ctx, tx, id)
		case "listTokens":
			item, err = r.token(ctx, tx, id)
		}
		if err != nil {
			return nil, err
		}
		blob, _ := json.Marshal(item)
		if size+len(blob) > 40<<10 {
			break
		}
		size += len(blob)
		items = append(items, item)
		cur.After = id
	}
	var next any
	truncated := len(items) < len(ids)
	if truncated {
		plain, _ := json.Marshal(cur)
		sealed, err := authn.Seal(r.key, plain, []byte("security-list-v1"))
		if err != nil {
			return nil, err
		}
		next = base64.RawURLEncoding.EncodeToString(sealed)
	}
	return map[string]any{"snapshot_id": cur.Snapshot, "instance_generation": nil, "items": items, "next_cursor": next, "truncated": truncated}, nil
}
func (r *Repository) receipt(ctx context.Context, q querier, c authn.Claims, id string, values url.Values) (apitypes.Receipt, error) {
	var receipt apitypes.Receipt
	if values.Get("domain") != "management" {
		return receipt, apitypes.Fail(503, "WORKSPACE_SERVICE_UNAVAILABLE")
	}
	var blob []byte
	var operation string
	err := q.QueryRowContext(ctx, "SELECT receipt,operation FROM api_receipts WHERE principal_id=? AND epoch=? AND domain='management' AND request_id=?", c.PrincipalID, values.Get("epoch"), id).Scan(&blob, &operation)
	if err != nil {
		return receipt, err
	}
	if err = json.Unmarshal(blob, &receipt); err != nil {
		return receipt, err
	}
	return receipt, r.allowPrior(ctx, q, c, operation, receipt.Resource)
}
func (r *Repository) allowPrior(ctx context.Context, q querier, c authn.Claims, operation string, ref *apitypes.Ref) error {
	for _, op := range r.contract.Operations {
		if op.ID == operation {
			if err := require(c, op.Capability, false, r.now().Unix()); err != nil {
				return err
			}
			if ref != nil {
				return checkRefs(ctx, q, c, []apitypes.Ref{*ref})
			}
			return nil
		}
	}
	return apitypes.Fail(403, "OPERATION_DENIED")
}
func (r *Repository) job(ctx context.Context, q querier, c authn.Claims, id string) (map[string]any, error) {
	var blob, receiptJSON []byte
	var state string
	err := q.QueryRowContext(ctx, "SELECT j.document,j.state,r.receipt FROM jobs j JOIN api_receipts r ON json_extract(r.receipt,'$.job_ref.id')=j.id WHERE j.id=? AND r.principal_id=? AND j.transaction_id IS NULL", id, c.PrincipalID).Scan(&blob, &state, &receiptJSON)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Operation string        `json:"operation"`
		OwnerID   string        `json:"owner_id"`
		Resource  *apitypes.Ref `json:"resource_ref"`
		Sequence  string        `json:"sequence"`
	}
	var receipt apitypes.Receipt
	if json.Unmarshal(blob, &doc) != nil || json.Unmarshal(receiptJSON, &receipt) != nil || doc.OwnerID != c.PrincipalID {
		return nil, apitypes.Fail(503, "AUTH_EVIDENCE_INVALID")
	}
	if err = r.allowPrior(ctx, q, c, doc.Operation, doc.Resource); err != nil {
		return nil, err
	}
	if doc.Sequence == "" {
		doc.Sequence = "1"
	}
	return map[string]any{"id": id, "sequence": doc.Sequence, "state": state, "operation": doc.Operation, "owner_id": doc.OwnerID, "resource_ref": doc.Resource, "cancellable": false, "correlation_id": receipt.CorrelationID}, nil
}

// Authentication errors cross IPC as stable codes only, never SQL or secrets.
func ErrorCode(err error) string {
	var p *apitypes.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return "AUTH_UNAVAILABLE"
}
