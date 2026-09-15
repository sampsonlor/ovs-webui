package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

func (r *Repository) operation(method, uri string) (*apicontract.Operation, map[string]string, url.Values, error) {
	if len(uri) > 4096 || strings.Contains(uri, "//") {
		return nil, nil, nil, apitypes.Fail(422, "INVALID_REQUEST")
	}
	u, err := url.ParseRequestURI(uri)
	if err != nil || u.IsAbs() || u.RawPath != "" || u.Fragment != "" {
		return nil, nil, nil, apitypes.Fail(422, "INVALID_REQUEST")
	}
	op, path, _ := r.contract.Match(method, u.Path)
	if op == nil || op.Public {
		return nil, nil, nil, apitypes.Fail(403, "OPERATION_DENIED")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, nil, nil, apitypes.Fail(422, "INVALID_QUERY")
	}
	return op, path, query, nil
}
func refsFor(op *apicontract.Operation, path map[string]string) []apitypes.Ref {
	out := []apitypes.Ref{}
	for _, id := range path {
		out = append(out, apitypes.Ref{Kind: op.ResourceKind, ID: id})
	}
	return out
}
func (r *Repository) checkOperation(ctx context.Context, q querier, credential string, op *apicontract.Operation, refs []apitypes.Ref, elevated bool) (authn.Claims, error) {
	c, err := r.claims(ctx, q, credential)
	if err != nil {
		return c, err
	}
	if err = require(c, op.Capability, elevated, r.now().Unix()); err != nil {
		return c, err
	}
	// Receipt/job access is checked against ownership and its original operation.
	if op.ID != "readRequestReceipt" && !evidence.Operation(op.ID) {
		if err = checkRefs(ctx, q, c, refs); err != nil {
			return c, err
		}
	}
	return c, nil
}
func securityCommand(id string) bool {
	switch id {
	case "createUser", "changeUser", "changePassword", "deleteUser", "createRole", "changeRole", "createToken", "revokeToken":
		return true
	}
	return false
}

func (r *Repository) ExecuteAuth(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	var out apitypes.Result
	op, path, query, err := r.operation(input.Method, input.URI)
	if err != nil {
		return out, err
	}
	if op.ID == "cancelJob" {
		return r.cancelJob(ctx, credential, input)
	}
	if op.ID == "reconcileTransaction" {
		return r.reconcileFields(ctx, credential, input)
	}
	if op.ID == "createTransaction" {
		if _, _, err = r.candidateCommand(input, "createTransaction"); err != nil {
			return out, err
		}
		if _, err = r.CheckAuth(ctx, credential, authn.Check{Capability: op.Capability}); err != nil {
			return out, err
		}
		return out, apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
	}
	if !securityCommand(op.ID) || op.Domain != "management" {
		return out, apitypes.Fail(503, "DOMAIN_SERVICE_UNAVAILABLE")
	}
	headers := map[string][]string{"Idempotency-Key": {input.RequestID}, "X-OVS-Request-Epoch": {input.Epoch}}
	if input.Precondition != "" {
		headers["If-Match"] = []string{input.Precondition}
	}
	if err = op.ValidateParameters(path, query, func(name string) []string { return headers[name] }); err != nil {
		return out, err
	}
	if input.Precondition != "" && !etagPattern.MatchString(input.Precondition) {
		return out, apitypes.Fail(428, "STRONG_PRECONDITION_REQUIRED")
	}
	value, body, err := op.Decode(input.Payload)
	if err != nil {
		return out, err
	}
	if value["request_id"] != input.RequestID {
		return out, apitypes.Fail(409, "IDEMPOTENCY_KEY_MISMATCH")
	}
	// Password work happens before the dispatch gate and DB writer. Re-authorize
	// afterwards, so a concurrent logout/role change wins over this prepared work.
	var verifier []byte
	if op.ID == "createUser" || op.ID == "changePassword" {
		if _, err = r.CheckAuth(ctx, credential, authn.Check{Capability: op.Capability, Resources: refsFor(op, path)}); err != nil {
			return out, err
		}
		free, e := r.passwordSlot(ctx)
		if e != nil {
			return out, e
		}
		verifier, err = hashPassword(value["password"].(string))
		free()
		if err != nil {
			return out, err
		}
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	c, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return out, err
	}
	command := requests.Command{Principal: c.PrincipalID, Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID, Method: input.Method, URI: input.URI, Precondition: input.Precondition, Payload: body, Sensitive: op.Sensitive, FingerprintKey: r.key, SecretResponse: op.SecretResponse}
	command.Credential, command.Capability = c.CredentialID, aliases[op.Capability]
	// Gate is held through authorization, durable receipt and security mutation.
	// A queued revoke wins before execution; completed effects remain evidenced.
	authorize := func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			var err error
			c, err = r.checkOperation(ctx, q, credential, op, refsFor(op, path), true)
			return err
		})
	}
	return r.receipts.Execute(ctx, command, authorize, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		var err error
		c, err = r.checkOperation(ctx, tx, credential, op, refsFor(op, path), true)
		if err != nil {
			return requests.Mutation{}, err
		}
		result, err := r.mutate(ctx, tx, c, op, path, input.Precondition, value, verifier, input.RequestID)
		if err != nil {
			return result, err
		}
		if err = r.lastAdministrator(ctx, tx); err != nil {
			return result, err
		}
		if err = r.touch(ctx, tx, credential, c); err != nil {
			return result, err
		}
		return result, nil
	})
}
func revision(ctx context.Context, tx *sql.Tx, table, column, id, expected string) error {
	var current string
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM "+table+" WHERE "+column+"=?", id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apitypes.Fail(404, "NOT_FOUND")
		}
		return err
	}
	if expected != `"`+current+`"` {
		return apitypes.Fail(412, "PRECONDITION_FAILED")
	}
	return nil
}
func stringValues(value any) []string {
	out := []string{}
	for _, v := range value.([]any) {
		out = append(out, v.(string))
	}
	return out
}
func roleAssignment(ctx context.Context, tx *sql.Tx, c authn.Claims, roles []string) error {
	if len(roles) > 64 || len(unique(roles)) != len(roles) {
		return apitypes.Fail(422, "INVALID_ROLE_ASSIGNMENT")
	}
	for _, role := range roles {
		var blob []byte
		var caps []string
		err := tx.QueryRowContext(ctx, "SELECT r.capabilities FROM roles r JOIN auth_roles a ON a.role_id=r.id WHERE r.id=?", role).Scan(&blob)
		if errors.Is(err, sql.ErrNoRows) {
			return apitypes.Fail(422, "ROLE_NOT_FOUND")
		}
		if err != nil {
			return err
		}
		if json.Unmarshal(blob, &caps) != nil || validateCapabilities(caps) != nil {
			return apitypes.Fail(503, "AUTH_POLICY_INVALID")
		}
		if !subset(caps, c.Capabilities) {
			return apitypes.Fail(403, "CAPABILITY_CEILING_EXCEEDED")
		}
	}
	return nil
}
func replaceRoles(ctx context.Context, tx *sql.Tx, id string, roles []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM principal_roles WHERE principal_id=?", id); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := tx.ExecContext(ctx, "INSERT INTO principal_roles VALUES(?,?)", id, role); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repository) invalidatePrincipal(ctx context.Context, tx *sql.Tx, id string) error {
	for _, table := range []string{"grants", "api_tokens"} {
		if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET revoked_at=? WHERE principal_id=? AND revoked_at IS NULL", r.now().Unix(), id); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repository) lastAdministrator(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT p.id,p.password_verifier FROM principals p JOIN auth_principals a ON a.principal_id=p.id WHERE p.disabled=0")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		var v []byte
		if err = rows.Scan(&id, &v); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(string(v), passwordPrefix) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		caps, err := capabilities(ctx, tx, id)
		if err != nil {
			return err
		}
		if subset(allCapabilities(), caps) {
			return nil
		}
	}
	return apitypes.Fail(409, "LAST_LOCAL_ADMINISTRATOR")
}
func (r *Repository) mutate(ctx context.Context, tx *sql.Tx, c authn.Claims, op *apicontract.Operation, path map[string]string, expected string, v map[string]any, verifier []byte, requestID string) (requests.Mutation, error) {
	result := requests.Mutation{Status: 202, Terminal: true, Body: json.RawMessage(`{}`)}
	id := ""
	kind := ""
	now := r.now().Unix()
	switch op.ID {
	case "createUser":
		name := v["username"].(string)
		if !usernamePattern.MatchString(name) {
			return result, apitypes.Fail(422, "INVALID_USERNAME")
		}
		roles := stringValues(v["role_ids"])
		if err := roleAssignment(ctx, tx, c, roles); err != nil {
			return result, err
		}
		var count, existing int
		if err := tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(name=?),0) FROM principals", name).Scan(&count, &existing); err != nil {
			return result, err
		}
		if existing > 0 {
			return result, apitypes.Fail(409, "USERNAME_EXISTS")
		}
		if count >= authn.MaxPrincipals {
			return result, apitypes.Fail(429, "PRINCIPAL_CAPACITY_REACHED")
		}
		id = repository.NewID()
		kind = "user"
		if _, err := tx.ExecContext(ctx, "INSERT INTO principals VALUES(?,?,0,?)", id, name, verifier); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO auth_principals VALUES(?,?,?)", id, repository.NewID(), now); err != nil {
			return result, err
		}
		if err := replaceRoles(ctx, tx, id, roles); err != nil {
			return result, err
		}
	case "changeUser", "changePassword", "deleteUser":
		id = path["user_id"]
		kind = "user"
		if err := revision(ctx, tx, "auth_principals", "principal_id", id, expected); err != nil {
			return result, err
		}
		switch op.ID {
		case "changeUser":
			roles := stringValues(v["role_ids"])
			if err := roleAssignment(ctx, tx, c, roles); err != nil {
				return result, err
			}
			if err := replaceRoles(ctx, tx, id, roles); err != nil {
				return result, err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE principals SET disabled=? WHERE id=?", v["disabled"], id); err != nil {
				return result, err
			}
			if v["disabled"].(bool) {
				if err := r.invalidatePrincipal(ctx, tx, id); err != nil {
					return result, err
				}
			}
		case "changePassword":
			if _, err := tx.ExecContext(ctx, "UPDATE principals SET password_verifier=? WHERE id=?", verifier, id); err != nil {
				return result, err
			}
			if err := r.invalidatePrincipal(ctx, tx, id); err != nil {
				return result, err
			}
		case "deleteUser":
			// Preserve durable receipt/audit references to a deleted identity.
			if _, err := tx.ExecContext(ctx, "UPDATE principals SET disabled=1,password_verifier=x'' WHERE id=?", id); err != nil {
				return result, err
			}
			if err := replaceRoles(ctx, tx, id, nil); err != nil {
				return result, err
			}
			if err := r.invalidatePrincipal(ctx, tx, id); err != nil {
				return result, err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE auth_principals SET revision=? WHERE principal_id=?", repository.NewID(), id); err != nil {
			return result, err
		}
	case "createRole", "changeRole":
		name := v["name"].(string)
		caps := stringValues(v["capabilities"])
		if err := validateCapabilities(caps); err != nil {
			return result, err
		}
		if !subset(caps, c.Capabilities) {
			return result, apitypes.Fail(403, "CAPABILITY_CEILING_EXCEEDED")
		}
		blob, _ := json.Marshal(unique(caps))
		id = path["role_id"]
		kind = "role"
		var count, existing int
		if err := tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(name=? AND role_id!=?),0) FROM auth_roles", name, id).Scan(&count, &existing); err != nil {
			return result, err
		}
		if existing > 0 {
			return result, apitypes.Fail(409, "ROLE_NAME_EXISTS")
		}
		if op.ID == "createRole" {
			if count >= authn.MaxRoles {
				return result, apitypes.Fail(429, "ROLE_CAPACITY_REACHED")
			}
			id = repository.NewID()
			if _, err := tx.ExecContext(ctx, "INSERT INTO roles VALUES(?,?)", id, blob); err != nil {
				return result, err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO auth_roles VALUES(?,?,?)", id, name, repository.NewID()); err != nil {
				return result, err
			}
		} else {
			if err := revision(ctx, tx, "auth_roles", "role_id", id, expected); err != nil {
				return result, err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id=?", blob, id); err != nil {
				return result, err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE auth_roles SET name=?,revision=? WHERE role_id=?", name, repository.NewID(), id); err != nil {
				return result, err
			}
		}
	case "createToken":
		scopes := stringValues(v["scopes"])
		if err := validateCapabilities(scopes); err != nil {
			return result, err
		}
		if !subset(scopes, c.Capabilities) {
			return result, apitypes.Fail(403, "CAPABILITY_CEILING_EXCEEDED")
		}
		expiry, err := time.Parse(time.RFC3339, v["expires_at"].(string))
		if err != nil || !expiry.After(r.now()) || expiry.After(r.now().Add(365*24*time.Hour)) {
			return result, apitypes.Fail(422, "INVALID_TOKEN_EXPIRY")
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM api_tokens").Scan(&count); err != nil {
			return result, err
		}
		if count >= authn.MaxTokens {
			return result, apitypes.Fail(429, "TOKEN_CAPACITY_REACHED")
		}
		id = repository.NewID()
		kind = "token"
		secret := authn.Secret("ovst_")
		hash := authn.Hash(secret)
		blob, _ := json.Marshal(unique(scopes))
		name := v["name"].(string)
		if _, err = tx.ExecContext(ctx, "INSERT INTO api_tokens VALUES(?,?,?,?,?,NULL)", hash[:], c.PrincipalID, c.Epoch, blob, expiry.Unix()); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO auth_tokens VALUES(?,?,?,?,?)", hash[:], id, name, now, 0); err != nil {
			return result, err
		}
		result.Status = 201
		result.SecretResponse = true
		result.Body, _ = json.Marshal(map[string]any{"id": id, "name": name, "scopes": unique(scopes), "expires_at": expiry.UTC(), "revoked": false, "secret": secret, "secret_available": true})
	case "revokeToken":
		id = path["token_id"]
		kind = "token"
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT t.principal_id FROM api_tokens t JOIN auth_tokens a ON a.token_hash=t.token_hash WHERE a.id=?", id).Scan(&owner); err != nil {
			return result, err
		}
		if owner != c.PrincipalID && !slices.Contains(c.Capabilities, "access.tokens.manage") {
			return result, apitypes.Fail(403, "RESOURCE_DENIED")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked_at=coalesce(revoked_at,?) WHERE token_hash=(SELECT token_hash FROM auth_tokens WHERE id=?)", now, id); err != nil {
			return result, err
		}
	default:
		return result, apitypes.Fail(503, "DOMAIN_SERVICE_UNAVAILABLE")
	}
	result.Resource = &apitypes.Ref{Kind: kind, ID: id}
	if result.Status == 202 {
		jobID := repository.NewID()
		result.Job = &apitypes.Ref{Kind: "job", ID: jobID}
		if _, err := evidence.CreateJob(ctx, tx, evidence.Job{ID: jobID, Resource: result.Resource, State: "succeeded", Business: "success", Reason: "security-operation-completed", Created: r.now()}); err != nil {
			return result, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE auth_state SET revision=? WHERE singleton=1", repository.NewID()); err != nil {
		return result, err
	}
	if err := r.audit(ctx, tx, c, op.ID, id, "success", requestID, result.Resource, result.Job); err != nil {
		return result, err
	}
	return result, nil
}
