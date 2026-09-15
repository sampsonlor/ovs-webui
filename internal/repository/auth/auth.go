package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type Repository struct {
	store           *sqlite.Store
	receipts        *requests.Repository
	contract        *apicontract.Contract
	key             []byte
	gate, passwords chan struct{}
	now             func() time.Time
	tlsNow          func() tlscontrol.Clock
	inventory       inventory.Reader
}

func (r *Repository) WithInventory(reader inventory.Reader) *Repository {
	r.inventory = reader
	return r
}

func New(store *sqlite.Store, key []byte) (*Repository, error) {
	if store.Kind() != repository.Manager || len(key) != 32 {
		return nil, repository.ErrInvalid
	}
	c, err := apicontract.New()
	if err != nil {
		return nil, err
	}
	return &Repository{store: store, receipts: requests.New(store), contract: c, key: append([]byte{}, key...), gate: make(chan struct{}, 1), passwords: make(chan struct{}, 2), now: time.Now, tlsNow: tlscontrol.Now}, nil
}
func (r *Repository) lock(ctx context.Context) (func(), error) {
	select {
	case r.gate <- struct{}{}:
		return func() { <-r.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,127}$`)

func (r *Repository) Ready(ctx context.Context) bool {
	if !r.store.Status().Writable {
		return false
	}
	var n int
	err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT count(*) FROM auth_principals a JOIN principals p ON p.id=a.principal_id WHERE p.disabled=0").Scan(&n)
	})
	return err == nil && n > 0
}

// Root-only local bootstrap caller; no IPC or HTTP bootstrap route exists.
func (r *Repository) Bootstrap(ctx context.Context, username, password string) error {
	if !usernamePattern.MatchString(username) {
		return apitypes.Fail(422, "INVALID_USERNAME")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM principals").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return apitypes.Fail(409, "ALREADY_BOOTSTRAPPED")
		}
		var adminRole string
		for name, caps := range Templates() {
			id := repository.NewID()
			blob, _ := json.Marshal(caps)
			if _, err := tx.ExecContext(ctx, "INSERT INTO roles VALUES(?,?)", id, blob); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO auth_roles VALUES(?,?,?)", id, name, repository.NewID()); err != nil {
				return err
			}
			if name == "Administrator" {
				adminRole = id
			}
		}
		id := repository.NewID()
		if _, err := tx.ExecContext(ctx, "INSERT INTO principals VALUES(?,?,0,?)", id, username, hash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO auth_principals VALUES(?,?,?)", id, repository.NewID(), r.now().Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO principal_roles VALUES(?,?)", id, adminRole); err != nil {
			return err
		}
		return r.audit(ctx, tx, authn.Claims{PrincipalID: id}, "bootstrap", id, "success", "")
	})
}
func (r *Repository) audit(ctx context.Context, tx *sql.Tx, c authn.Claims, op, target, result, requestID string, refs ...*apitypes.Ref) error {
	record := evidence.Record{Collection: "audit", Origin: "Manager", Critical: op == "tls-recovery" || evidence.ControlOperation(op), Actor: c.PrincipalID, Credential: c.CredentialID, Operation: op, Result: result, RequestID: requestID, Created: r.now()}
	if len(refs) > 0 {
		record.Object = refs[0]
	}
	if len(refs) > 1 && refs[1] != nil {
		record.Job = refs[1].ID
	}
	if record.Object == nil && apitypes.ManagementID(target) {
		record.Object = &apitypes.Ref{Kind: "security-resource", ID: target}
	}
	id, err := evidence.Append(ctx, tx, record)
	if err != nil {
		return err
	}
	// Compatibility projection for accepted recovery tools; the shared record
	// above is the authority and owns capacity, retention and public redaction.
	_, err = tx.ExecContext(ctx, "INSERT INTO auth_audit VALUES(?,?,?,?,?,?,?,?)", id, c.PrincipalID, c.CredentialID, op, target, result, requestID, r.now().Unix())
	return err
}
func (r *Repository) clock(ctx context.Context, q querier) (string, string, error) {
	var epoch, rev string
	var high int64
	err := q.QueryRowContext(ctx, "SELECT epoch,revision,high_watermark_ms FROM auth_state WHERE singleton=1").Scan(&epoch, &rev, &high)
	if err != nil {
		return "", "", err
	}
	if r.now().UnixMilli() < high {
		return "", "", apitypes.Fail(503, "AUTH_CLOCK_UNSAFE")
	}
	return epoch, rev, nil
}
func (r *Repository) watermark(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE auth_state SET high_watermark_ms=max(high_watermark_ms,?) WHERE singleton=1", r.now().UnixMilli())
	return err
}
func (r *Repository) claims(ctx context.Context, q querier, credential string) (authn.Claims, error) {
	var c authn.Claims
	epoch, revision, err := r.clock(ctx, q)
	if err != nil {
		return c, err
	}
	hash := authn.Hash(credential)
	var ceiling []byte
	var credentialEpoch string
	var issued, last, expiry, elevated int64
	var revoked sql.NullInt64
	var disabled bool
	if authn.ValidSecret(credential, "ovsg_") {
		c.CredentialKind = "grant"
		err = q.QueryRowContext(ctx, "SELECT p.id,p.name,p.disabled,g.epoch,g.ceiling,g.expires_at,g.revoked_at,a.id,a.issued_at,a.last_used_at,a.elevated_until FROM grants g JOIN auth_grants a ON a.grant_hash=g.grant_hash JOIN principals p ON p.id=g.principal_id JOIN auth_principals ap ON ap.principal_id=p.id WHERE g.grant_hash=?", hash[:]).Scan(&c.PrincipalID, &c.DisplayName, &disabled, &credentialEpoch, &ceiling, &expiry, &revoked, &c.CredentialID, &issued, &last, &elevated)
	} else if authn.ValidSecret(credential, "ovst_") {
		c.CredentialKind = "token"
		err = q.QueryRowContext(ctx, "SELECT p.id,p.name,p.disabled,t.epoch,t.scopes,t.expires_at,t.revoked_at,a.id,a.created_at,a.last_used_at FROM api_tokens t JOIN auth_tokens a ON a.token_hash=t.token_hash JOIN principals p ON p.id=t.principal_id JOIN auth_principals ap ON ap.principal_id=p.id WHERE t.token_hash=?", hash[:]).Scan(&c.PrincipalID, &c.DisplayName, &disabled, &credentialEpoch, &ceiling, &expiry, &revoked, &c.CredentialID, &issued, &last)
	} else {
		return c, apitypes.Fail(401, "UNAUTHENTICATED")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return c, apitypes.Fail(401, "UNAUTHENTICATED")
	}
	if err != nil {
		return c, err
	}
	now := r.now().Unix()
	if disabled || revoked.Valid || credentialEpoch != epoch || now >= expiry || now < issued || (c.CredentialKind == "grant" && (now >= last+int64(authn.SessionIdle.Seconds()) || expiry > issued+int64(authn.SessionAbsolute.Seconds()))) {
		return c, apitypes.Fail(401, "CREDENTIAL_EXPIRED_OR_REVOKED")
	}
	var caps []string
	if json.Unmarshal(ceiling, &caps) != nil || validateCapabilities(caps) != nil {
		return c, apitypes.Fail(503, "AUTH_POLICY_INVALID")
	}
	current, err := capabilities(ctx, q, c.PrincipalID)
	if err != nil {
		return c, err
	}
	c.Capabilities = intersection(current, caps)
	c.Revision = revision
	c.Epoch = epoch
	c.IssuedAt = time.Unix(issued, 0).UTC()
	c.ExpiresAt = time.Unix(expiry, 0).UTC()
	c.ElevatedUntil = time.Unix(elevated, 0).UTC()
	if err = q.QueryRowContext(ctx, "SELECT epoch FROM api_authority WHERE singleton=1").Scan(&c.RequestEpoch); err != nil {
		return c, err
	}
	return c, nil
}
func (r *Repository) InspectAuth(ctx context.Context, credential string) (authn.Claims, error) {
	var c authn.Claims
	if !r.store.Status().Writable {
		return c, apitypes.Fail(503, "AUTH_UNAVAILABLE")
	}
	err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var err error
		c, err = r.claims(ctx, q, credential)
		return err
	})
	return c, err
}
func (r *Repository) CheckAuth(ctx context.Context, credential string, check authn.Check) (authn.Claims, error) {
	var c authn.Claims
	if !r.store.Status().Writable {
		return c, apitypes.Fail(503, "AUTH_UNAVAILABLE")
	}
	err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var err error
		c, err = r.claims(ctx, q, credential)
		if err != nil {
			return err
		}
		if err = require(c, check.Capability, false, r.now().Unix()); err != nil {
			return err
		}
		return checkRefs(ctx, q, c, check.Resources)
	})
	return c, err
}
func (r *Repository) Authorize(ctx context.Context, credential string, op ipc.Operation) error {
	if op.Name != "runtime.inspect" {
		return ipc.ErrDenied
	}
	_, err := r.CheckAuth(ctx, credential, authn.Check{Capability: "state.read"})
	return err
}
func (r *Repository) touch(ctx context.Context, tx *sql.Tx, credential string, c authn.Claims) error {
	hash := authn.Hash(credential)
	table := "auth_grants"
	column := "grant_hash"
	if c.CredentialKind == "token" {
		table = "auth_tokens"
		column = "token_hash"
	}
	if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET last_used_at=? WHERE "+column+"=?", r.now().Unix(), hash[:]); err != nil {
		return err
	}
	return r.watermark(ctx, tx)
}
func (r *Repository) attempt(ctx context.Context, username string) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, _, err := r.clock(ctx, tx); err != nil {
			return err
		}
		now := r.now().Unix()
		hash := authn.Hash(strings.ToLower(username))
		if _, err := tx.ExecContext(ctx, "DELETE FROM auth_attempts WHERE window_start<?", now-60); err != nil {
			return err
		}
		var total int
		if err := tx.QueryRowContext(ctx, "SELECT coalesce(sum(attempts),0) FROM auth_attempts").Scan(&total); err != nil {
			return err
		}
		if total >= 60 {
			return apitypes.Fail(429, "AUTH_RATE_LIMITED")
		}
		var attempts int
		err := tx.QueryRowContext(ctx, "SELECT attempts FROM auth_attempts WHERE username_hash=?", hash[:]).Scan(&attempts)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if attempts >= 10 {
			return apitypes.Fail(429, "AUTH_RATE_LIMITED")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO auth_attempts VALUES(?,?,1) ON CONFLICT(username_hash) DO UPDATE SET attempts=attempts+1", hash[:], now); err != nil {
			return err
		}
		return r.watermark(ctx, tx)
	})
}
func (r *Repository) Authenticate(ctx context.Context, login authn.Login) (authn.LoginResult, error) {
	var out authn.LoginResult
	if login.Provider != "local" {
		return out, apitypes.Fail(503, "AUTH_PROVIDER_UNAVAILABLE")
	}
	if !usernamePattern.MatchString(login.Username) || len(login.Password) < 1 || len(login.Password) > 1024 {
		return out, apitypes.Fail(401, "AUTHENTICATION_REJECTED")
	}
	free, err := r.passwordSlot(ctx)
	if err != nil {
		return out, err
	}
	defer free()
	if err = r.attempt(ctx, login.Username); err != nil {
		return out, err
	}
	var id string
	var verifier []byte
	var disabled bool
	err = r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT p.id,p.password_verifier,p.disabled FROM principals p JOIN auth_principals a ON a.principal_id=p.id WHERE p.name=?", login.Username).Scan(&id, &verifier, &disabled)
	})
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return out, err
	}
	valid := verifyPassword(login.Password, verifier)
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	denied := !valid || disabled || id == ""
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if denied {
			return r.audit(ctx, tx, authn.Claims{}, "authenticate", "", "rejected", "")
		}
		var current []byte
		var off bool
		if err := tx.QueryRowContext(ctx, "SELECT password_verifier,disabled FROM principals WHERE id=?", id).Scan(&current, &off); err != nil {
			return err
		}
		if off || !bytes.Equal(current, verifier) {
			denied = true
			return r.audit(ctx, tx, authn.Claims{}, "authenticate", "", "rejected", "")
		}
		epoch, _, err := r.clock(ctx, tx)
		if err != nil {
			return err
		}
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM grants WHERE revoked_at IS NULL AND expires_at>?", r.now().Unix()).Scan(&n); err != nil {
			return err
		}
		if n >= authn.MaxGrants {
			return apitypes.Fail(429, "AUTH_GRANT_CAPACITY_REACHED")
		}
		caps, err := capabilities(ctx, tx, id)
		if err != nil {
			return err
		}
		ceiling, _ := json.Marshal(caps)
		out.Grant = authn.Secret("ovsg_")
		hash := authn.Hash(out.Grant)
		now := r.now().Unix()
		expiry := r.now().Add(authn.SessionAbsolute).Unix()
		if _, err = tx.ExecContext(ctx, "INSERT INTO grants VALUES(?,?,?,?,?,NULL)", hash[:], id, epoch, ceiling, expiry); err != nil {
			return err
		}
		credentialID := repository.NewID()
		if _, err = tx.ExecContext(ctx, "INSERT INTO auth_grants VALUES(?,?,?,?,0)", hash[:], credentialID, now, now); err != nil {
			return err
		}
		out.Claims, err = r.claims(ctx, tx, out.Grant)
		if err != nil {
			return err
		}
		if err = r.audit(ctx, tx, out.Claims, "authenticate", id, "success", ""); err != nil {
			return err
		}
		return r.watermark(ctx, tx)
	})
	if err != nil {
		return authn.LoginResult{}, err
	}
	if denied {
		return authn.LoginResult{}, apitypes.Fail(401, "AUTHENTICATION_REJECTED")
	}
	return out, nil
}
func (r *Repository) Reauthenticate(ctx context.Context, credential string, input authn.Reauthentication) (authn.Claims, error) {
	c, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return c, err
	}
	if c.CredentialKind != "grant" {
		return c, apitypes.Fail(403, "BROWSER_SESSION_REQUIRED")
	}
	if len(input.Password) < 1 || len(input.Password) > 1024 {
		return c, apitypes.Fail(401, "AUTHENTICATION_REJECTED")
	}
	free, err := r.passwordSlot(ctx)
	if err != nil {
		return c, err
	}
	defer free()
	if err = r.attempt(ctx, c.DisplayName); err != nil {
		return c, err
	}
	var verifier []byte
	err = r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT password_verifier FROM principals WHERE id=?", c.PrincipalID).Scan(&verifier)
	})
	if err != nil {
		return c, err
	}
	valid := verifyPassword(input.Password, verifier)
	unlock, err := r.lock(ctx)
	if err != nil {
		return c, err
	}
	defer unlock()
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		c, err = r.claims(ctx, tx, credential)
		if err != nil {
			return err
		}
		var current []byte
		if err = tx.QueryRowContext(ctx, "SELECT password_verifier FROM principals WHERE id=?", c.PrincipalID).Scan(&current); err != nil {
			return err
		}
		valid = valid && bytes.Equal(current, verifier)
		if !valid {
			return r.audit(ctx, tx, c, "reauthenticate", c.PrincipalID, "rejected", "")
		}
		until := r.now().Add(authn.Elevation)
		if until.After(c.ExpiresAt) {
			until = c.ExpiresAt
		}
		hash := authn.Hash(credential)
		if _, err = tx.ExecContext(ctx, "UPDATE auth_grants SET elevated_until=? WHERE grant_hash=?", until.Unix(), hash[:]); err != nil {
			return err
		}
		c.ElevatedUntil = until.UTC()
		if err = r.audit(ctx, tx, c, "reauthenticate", c.PrincipalID, "success", ""); err != nil {
			return err
		}
		return r.touch(ctx, tx, credential, c)
	})
	if err != nil {
		return authn.Claims{}, err
	}
	if !valid {
		return authn.Claims{}, apitypes.Fail(401, "AUTHENTICATION_REJECTED")
	}
	return c, nil
}
func (r *Repository) RevokeAuth(ctx context.Context, credential string) error {
	unlock, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		c, err := r.claims(ctx, tx, credential)
		if err != nil {
			return err
		}
		if c.CredentialKind != "grant" {
			return apitypes.Fail(403, "BROWSER_SESSION_REQUIRED")
		}
		hash := authn.Hash(credential)
		if _, err = tx.ExecContext(ctx, "UPDATE grants SET revoked_at=? WHERE grant_hash=?", r.now().Unix(), hash[:]); err != nil {
			return err
		}
		if err = r.audit(ctx, tx, c, "revoke-session", c.CredentialID, "success", ""); err != nil {
			return err
		}
		return r.watermark(ctx, tx)
	})
}
