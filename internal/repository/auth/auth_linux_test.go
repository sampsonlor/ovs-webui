//go:build linux

package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

var testContext = context.Background()

const testPassword = "synthetic-test-password-34"

func fixture(t *testing.T) (*Repository, sqlite.Options) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	o := sqlite.Options{Path: filepath.Join(root, "manager.db"), Kind: repository.Manager, SoftwareVersion: "auth-test"}
	if err := sqlite.Initialize(testContext, o); err != nil {
		t.Fatal(err)
	}
	r := openFixture(t, o)
	if r.Ready(testContext) {
		t.Fatal("empty authority ready")
	}
	if err := r.Bootstrap(testContext, "admin", testPassword); err != nil {
		t.Fatal(err)
	}
	return r, o
}
func openFixture(t *testing.T, o sqlite.Options) *Repository {
	t.Helper()
	store, err := sqlite.Open(testContext, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	r, err := New(store, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func login(t *testing.T, r *Repository, name string, elevate bool) authn.LoginResult {
	t.Helper()
	c, err := r.Authenticate(testContext, authn.Login{Provider: "local", Username: name, Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if elevate {
		claims, err := r.Reauthenticate(testContext, c.Grant, authn.Reauthentication{Password: testPassword})
		if err != nil {
			t.Fatal(err)
		}
		c.Claims = claims
	}
	return c
}
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var p *apitypes.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func request(t *testing.T, r *Repository, method, path string, body map[string]any, etag string) authn.Command {
	t.Helper()
	id := apitypes.RequestID(time.Now())
	body["request_id"] = id
	raw, _ := json.Marshal(body)
	epoch, err := r.receipts.Epoch(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return authn.Command{Method: method, URI: "/api/v1" + path, Epoch: epoch, RequestID: id, Precondition: etag, Payload: raw}
}
func execute(t *testing.T, r *Repository, c authn.LoginResult, method, path string, body map[string]any, etag string) apitypes.Result {
	t.Helper()
	out, err := r.ExecuteAuth(testContext, c.Grant, request(t, r, method, path, body, etag))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func read(t *testing.T, r *Repository, c authn.LoginResult, path string) map[string]any {
	t.Helper()
	out, err := r.ReadAuth(testContext, c.Grant, authn.Query{Method: "GET", URI: "/api/v1" + path})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(out.Body, &value) != nil {
		t.Fatal("invalid read")
	}
	return value
}
func roleID(t *testing.T, r *Repository, c authn.LoginResult, name string) string {
	t.Helper()
	for _, item := range read(t, r, c, "/roles")["items"].([]any) {
		row := item.(map[string]any)
		if row["name"] == name {
			return row["id"].(string)
		}
	}
	t.Fatal("missing role")
	return ""
}
func createUser(t *testing.T, r *Repository, c authn.LoginResult, name, role string) string {
	t.Helper()
	out := execute(t, r, c, "POST", "/users", map[string]any{"username": name, "password": testPassword, "role_ids": []string{role}}, "")
	job := read(t, r, c, "/jobs/"+out.Receipt.Job.ID)
	if job["sequence"] != "1" || job["state"] != "succeeded" || job["correlation_id"] != out.Receipt.CorrelationID {
		t.Fatal("user creation did not expose its durable terminal job")
	}
	return out.Receipt.Resource.ID
}

func TestLocalBootstrapAndPasswordGrantBoundary(t *testing.T) {
	r, _ := fixture(t)
	if !r.Ready(testContext) {
		t.Fatal("bootstrap not ready")
	}
	wantCode(t, r.Bootstrap(testContext, "other", testPassword), "ALREADY_BOOTSTRAPPED")
	_, err := r.Authenticate(testContext, authn.Login{Provider: "local", Username: "absent", Password: testPassword})
	wantCode(t, err, "AUTHENTICATION_REJECTED")
	_, err = r.Authenticate(testContext, authn.Login{Provider: "tacacs", Username: "admin", Password: testPassword})
	wantCode(t, err, "AUTH_PROVIDER_UNAVAILABLE")
	c := login(t, r, "admin", false)
	if !authn.ValidSecret(c.Grant, "ovsg_") || !subset(allCapabilities(), c.Claims.Capabilities) || c.Claims.ElevatedUntil.After(time.Now()) {
		t.Fatal("invalid initial grant")
	}
	_, err = r.ExecuteAuth(testContext, c.Grant, request(t, r, "POST", "/roles", map[string]any{"name": "extra", "capabilities": []string{}}, ""))
	wantCode(t, err, "REAUTHENTICATION_REQUIRED")
	var verifier []byte
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT password_verifier FROM principals WHERE name='admin'").Scan(&verifier)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(verifier), passwordPrefix) || bytes.Contains(verifier, []byte(testPassword)) {
		t.Fatal("password not independently hashed")
	}
}
func TestLastLocalAdministratorAndStrongPreconditions(t *testing.T) {
	r, _ := fixture(t)
	c := login(t, r, "admin", true)
	id := c.Claims.PrincipalID
	u := read(t, r, c, "/users/"+id)
	tag := `"` + u["revision"].(string) + `"`
	for _, body := range []map[string]any{{"disabled": true, "role_ids": u["role_ids"]}, {"disabled": false, "role_ids": []string{}}} {
		_, err := r.ExecuteAuth(testContext, c.Grant, request(t, r, "PATCH", "/users/"+id, body, tag))
		wantCode(t, err, "LAST_LOCAL_ADMINISTRATOR")
	}
	_, err := r.ExecuteAuth(testContext, c.Grant, request(t, r, "POST", "/users/"+id+"/deletions", map[string]any{}, tag))
	wantCode(t, err, "LAST_LOCAL_ADMINISTRATOR")
	role := read(t, r, c, "/roles/"+roleID(t, r, c, "Administrator"))
	_, err = r.ExecuteAuth(testContext, c.Grant, request(t, r, "PATCH", "/roles/"+role["id"].(string), map[string]any{"name": "Administrator", "capabilities": []string{}}, `"`+role["revision"].(string)+`"`))
	wantCode(t, err, "LAST_LOCAL_ADMINISTRATOR")
	_, err = r.ExecuteAuth(testContext, c.Grant, request(t, r, "PATCH", "/users/"+id, map[string]any{"disabled": false, "role_ids": u["role_ids"]}, `"obsolete"`))
	wantCode(t, err, "PRECONDITION_FAILED")
	if !r.store.Status().Writable || read(t, r, c, "/users/"+id)["revision"] != u["revision"] {
		t.Fatal("rejection damaged authority or changed user")
	}
}
func TestCurrentPermissionsGrantCeilingAndRoleTemplates(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	readerRole := roleID(t, r, admin, "Reader")
	createUser(t, r, admin, "reader", readerRole)
	reader := login(t, r, "reader", false)
	if _, err := r.CheckAuth(testContext, reader.Grant, authn.Check{Capability: "user.manage"}); err == nil {
		t.Fatal("Reader became security admin")
	}
	role := read(t, r, admin, "/roles/"+readerRole)
	execute(t, r, admin, "PATCH", "/roles/"+readerRole, map[string]any{"name": "Reader", "capabilities": []string{"configuration.apply"}}, `"`+role["revision"].(string)+`"`)
	_, err := r.CheckAuth(testContext, reader.Grant, authn.Check{Capability: "state.read"})
	wantCode(t, err, "CAPABILITY_DENIED")
	_, err = r.CheckAuth(testContext, reader.Grant, authn.Check{Capability: "configuration.apply"})
	wantCode(t, err, "CAPABILITY_DENIED")
	updated := login(t, r, "reader", false)
	if _, err = r.CheckAuth(testContext, updated.Grant, authn.Check{Capability: "configuration.apply"}); err != nil {
		t.Fatal("fresh credentials missed current role", err)
	}
	templates := Templates()
	if slices.Contains(templates["NetworkAdmin"], "access.users.manage") || slices.Contains(templates["SecurityAdmin"], "configuration.apply") {
		t.Fatal("role templates conflate security and network permissions")
	}
}
func TestTokenScopeOneTimeSecretAndReceiptRecovery(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	input := request(t, r, "POST", "/tokens", map[string]any{"name": "synthetic-api", "scopes": []string{"state.read"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "")
	first, err := r.ExecuteAuth(testContext, admin.Grant, input)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	_ = json.Unmarshal(first.Body, &created)
	secret := created["secret"].(string)
	c, err := r.InspectAuth(testContext, secret)
	if err != nil || c.CredentialKind != "token" || !slices.Equal(c.Capabilities, []string{"state.read"}) {
		t.Fatal("bad scoped token", err)
	}
	replay, err := r.ExecuteAuth(testContext, admin.Grant, input)
	if err != nil || !replay.Replayed || bytes.Contains(replay.Body, []byte(secret)) || replay.Receipt.CorrelationID != first.Receipt.CorrelationID || replay.Receipt.RequestID != first.Receipt.RequestID {
		t.Fatal("secret replay or identity changed", err)
	}
	_, err = r.CheckAuth(testContext, secret, authn.Check{Capability: "user.manage"})
	wantCode(t, err, "CAPABILITY_DENIED")
	_, err = r.CheckAuth(testContext, secret, authn.Check{Capability: "token.read"})
	wantCode(t, err, "CAPABILITY_DENIED")
	_, err = r.Reauthenticate(testContext, secret, authn.Reauthentication{Password: testPassword})
	wantCode(t, err, "BROWSER_SESSION_REQUIRED")
	execute(t, r, admin, "POST", "/tokens/"+created["id"].(string)+"/revocations", map[string]any{}, "")
	_, err = r.InspectAuth(testContext, secret)
	wantCode(t, err, "CREDENTIAL_EXPIRED_OR_REVOKED")
	receipt := read(t, r, admin, "/requests/"+input.RequestID+"?domain=management&epoch="+input.Epoch)
	if receipt["request_id"] != input.RequestID {
		t.Fatal("lost receipt")
	}
	var stored []byte
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT response FROM api_receipts WHERE request_id=?", input.RequestID).Scan(&stored)
	}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(secret)) || bytes.Contains(stored, []byte(admin.Grant)) {
		t.Fatal("plaintext credential persisted")
	}
}
func TestTokenCannotExpandScopeOrReadOtherOwnersToken(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	createUser(t, r, admin, "reader", roleID(t, r, admin, "Reader"))
	reader := login(t, r, "reader", true)
	_, err := r.ExecuteAuth(testContext, reader.Grant, request(t, r, "POST", "/tokens", map[string]any{"name": "too-wide", "scopes": []string{"access.users.manage"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, ""))
	wantCode(t, err, "CAPABILITY_CEILING_EXCEEDED")
	result := execute(t, r, admin, "POST", "/tokens", map[string]any{"name": "admin-api", "scopes": []string{"state.read"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "")
	_, err = r.ReadAuth(testContext, reader.Grant, authn.Query{Method: "GET", URI: "/api/v1/tokens/" + result.Receipt.Resource.ID})
	wantCode(t, err, "RESOURCE_DENIED")
	if len(read(t, r, reader, "/tokens")["items"].([]any)) != 0 {
		t.Fatal("token list leaked another principal")
	}
}
func TestRestartIdleAbsoluteExpiryAndClockRollback(t *testing.T) {
	r, o := fixture(t)
	c := login(t, r, "admin", true)
	expiry := c.Claims.ExpiresAt
	if err := r.store.Close(); err != nil {
		t.Fatal(err)
	}
	r = openFixture(t, o)
	claims, err := r.InspectAuth(testContext, c.Grant)
	if err != nil || !claims.ExpiresAt.Equal(expiry) {
		t.Fatal("restart extended or lost grant", err)
	}
	future := time.Now().Add(authn.SessionIdle + time.Second)
	r.now = func() time.Time { return future }
	_, err = r.InspectAuth(testContext, c.Grant)
	wantCode(t, err, "CREDENTIAL_EXPIRED_OR_REVOKED")
	// Compare against the persisted boundary, not elapsed wall time while a
	// loaded race-test runner opens the database and evaluates expiry.
	var persisted int64
	if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT high_watermark_ms FROM auth_state WHERE singleton=1").Scan(&persisted)
	}); err != nil {
		t.Fatal(err)
	}
	rollback := time.UnixMilli(persisted - 1)
	r.now = func() time.Time { return rollback }
	_, err = r.InspectAuth(testContext, c.Grant)
	wantCode(t, err, "AUTH_CLOCK_UNSAFE")
	r.now = time.Now
	if err = r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE auth_grants SET last_used_at=?", expiry.Unix())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return expiry }
	_, err = r.InspectAuth(testContext, c.Grant)
	wantCode(t, err, "CREDENTIAL_EXPIRED_OR_REVOKED")
}
func TestElevationDeadlineAndAuthEpochInvalidation(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	result := execute(t, r, admin, "POST", "/tokens", map[string]any{"name": "epoch-test", "scopes": []string{"state.read"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "")
	var token map[string]any
	if err := json.Unmarshal(result.Body, &token); err != nil {
		t.Fatal(err)
	}
	_, err := r.Reauthenticate(testContext, admin.Grant, authn.Reauthentication{Password: "incorrect-synthetic-password"})
	wantCode(t, err, "AUTHENTICATION_REJECTED")
	future := admin.Claims.ElevatedUntil.Add(time.Second)
	r.now = func() time.Time { return future }
	if _, err = r.InspectAuth(testContext, admin.Grant); err != nil {
		t.Fatal("elevation expiry invalidated ordinary session", err)
	}
	_, err = r.ExecuteAuth(testContext, admin.Grant, request(t, r, "POST", "/tokens", map[string]any{"name": "expired-elevation", "scopes": []string{"state.read"}, "expires_at": future.Add(time.Hour).UTC().Format(time.RFC3339)}, ""))
	wantCode(t, err, "REAUTHENTICATION_REQUIRED")
	if err = r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE auth_state SET epoch=? WHERE singleton=1", repository.NewID())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{admin.Grant, token["secret"].(string)} {
		_, err = r.InspectAuth(testContext, credential)
		wantCode(t, err, "CREDENTIAL_EXPIRED_OR_REVOKED")
	}
}

func TestRevocationSerializesWithAdmittedSecurityMutation(t *testing.T) {
	r, _ := fixture(t)
	for i := 0; i < 4; i++ {
		c := login(t, r, "admin", true)
		input := request(t, r, "POST", "/tokens", map[string]any{"name": "race", "scopes": []string{"state.read"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "")
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var result apitypes.Result
		var executionErr, revokeErr error
		go func() { defer wg.Done(); <-start; result, executionErr = r.ExecuteAuth(testContext, c.Grant, input) }()
		go func() { defer wg.Done(); <-start; revokeErr = r.RevokeAuth(testContext, c.Grant) }()
		close(start)
		wg.Wait()
		if revokeErr != nil {
			t.Fatal(revokeErr)
		}
		var n int
		if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT count(*) FROM api_receipts WHERE request_id=?", input.RequestID).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		if executionErr == nil {
			if n != 1 || result.Receipt.RequestID != input.RequestID {
				t.Fatal("dispatched operation lost evidence")
			}
		} else {
			wantCode(t, executionErr, "CREDENTIAL_EXPIRED_OR_REVOKED")
			if n != 0 {
				t.Fatal("revoked request mutated authority")
			}
		}
	}
}
func TestPasswordChangeAndDisableRevokeAllCredentials(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	id := createUser(t, r, admin, "reader", roleID(t, r, admin, "Reader"))
	reader := login(t, r, "reader", true)
	token := execute(t, r, reader, "POST", "/tokens", map[string]any{"name": "test", "scopes": []string{"state.read"}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "")
	var created map[string]any
	_ = json.Unmarshal(token.Body, &created)
	u := read(t, r, admin, "/users/"+id)
	execute(t, r, admin, "POST", "/users/"+id+"/password", map[string]any{"password": "another-synthetic-password"}, `"`+u["revision"].(string)+`"`)
	for _, secret := range []string{reader.Grant, created["secret"].(string)} {
		_, err := r.InspectAuth(testContext, secret)
		wantCode(t, err, "CREDENTIAL_EXPIRED_OR_REVOKED")
	}
	u = read(t, r, admin, "/users/"+id)
	execute(t, r, admin, "PATCH", "/users/"+id, map[string]any{"disabled": true, "role_ids": u["role_ids"]}, `"`+u["revision"].(string)+`"`)
	_, err := r.Authenticate(testContext, authn.Login{Provider: "local", Username: "reader", Password: "another-synthetic-password"})
	wantCode(t, err, "AUTHENTICATION_REJECTED")
}
func TestForgedIdentityModeAndUnsupportedObjectAuthority(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	command := request(t, r, "POST", "/roles", map[string]any{"name": "fake", "capabilities": []string{}, "actor": "admin", "mode": "expert"}, "")
	if _, err := r.ExecuteAuth(testContext, admin.Grant, command); err == nil {
		t.Fatal("forged input fields accepted")
	}
	if _, err := r.CheckAuth(testContext, admin.Grant, authn.Check{Capability: "invented.superuser"}); err == nil {
		t.Fatal("unknown capability granted")
	}
	_, err := r.CheckAuth(testContext, admin.Grant, authn.Check{Capability: "state.read", Resources: []apitypes.Ref{{Kind: "interface", ID: repository.NewID()}}})
	wantCode(t, err, "OBJECT_AUTHORITY_UNAVAILABLE")
	_, err = r.ReadAuth(testContext, admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/aaa"})
	wantCode(t, err, "DOMAIN_SERVICE_UNAVAILABLE")
	if !r.store.Status().Writable {
		t.Fatal("rejected request damaged DB")
	}
}
func TestCursorIsBoundToPrincipalPolicyAndScope(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	first := read(t, r, admin, "/roles?limit=1")
	cursor := first["next_cursor"].(string)
	second := read(t, r, admin, "/roles?limit=1&cursor="+cursor)
	if first["snapshot_id"] != second["snapshot_id"] || first["items"].([]any)[0].(map[string]any)["id"] == second["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("unstable page")
	}
	_, err := r.ReadAuth(testContext, admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/roles?limit=2&cursor=" + cursor})
	wantCode(t, err, "CURSOR_EXPIRED")
	execute(t, r, admin, "POST", "/roles", map[string]any{"name": "new", "capabilities": []string{}}, "")
	_, err = r.ReadAuth(testContext, admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/roles?limit=1&cursor=" + cursor})
	wantCode(t, err, "CURSOR_EXPIRED")
}
func TestRateBudgetAndAuditRedaction(t *testing.T) {
	r, _ := fixture(t)
	for i := 0; i < 10; i++ {
		if err := r.attempt(testContext, "not-a-real-user"); err != nil {
			t.Fatal(err)
		}
	}
	wantCode(t, r.attempt(testContext, "not-a-real-user"), "AUTH_RATE_LIMITED")
	c := login(t, r, "admin", true)
	createUser(t, r, c, "evidence", roleID(t, r, c, "Reader"))
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		rows, err := q.QueryContext(ctx, "SELECT operation,result,coalesce(request_id,'') FROM auth_audit")
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var op, result, id string
			if err = rows.Scan(&op, &result, &id); err != nil {
				return err
			}
			if strings.Contains(op+result+id, testPassword) || strings.Contains(op+result+id, c.Grant) {
				t.Fatal("audit leaked credential")
			}
			count++
		}
		if count < 4 {
			t.Fatal("missing audit evidence")
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	free, err := r.passwordSlot(testContext)
	if err != nil {
		t.Fatal(err)
	}
	defer free()
	free2, err := r.passwordSlot(testContext)
	if err != nil {
		t.Fatal(err)
	}
	defer free2()
	_, err = r.passwordSlot(testContext)
	wantCode(t, err, "AUTH_BUDGET_EXCEEDED")
}
func TestArgon2Calibration(t *testing.T) {
	start := time.Now()
	verifier, err := hashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	hashElapsed := time.Since(start)
	start = time.Now()
	if !verifyPassword(testPassword, verifier) || verifyPassword("wrong", verifier) {
		t.Fatal("password verification")
	}
	t.Logf("argon2id architecture=%s cpus=%d memory_kib=65536 iterations=3 parallelism=1 active_limit=2 hash_ms=%d two_verifications_ms=%d", runtime.GOARCH, runtime.NumCPU(), hashElapsed.Milliseconds(), time.Since(start).Milliseconds())
}
