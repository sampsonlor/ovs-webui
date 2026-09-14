package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

const principal = "00000000-0000-4000-8000-000000000001"
const epoch = "00000000-0000-4000-8000-000000000002"
const resource = "00000000-0000-4000-8000-000000000003"

type testAuthority struct {
	revoked atomic.Bool
	cookie  bool
}

func (a *testAuthority) Authenticate(context.Context, *http.Request) (Subject, error) {
	s := Subject{ID: principal, PermissionRevision: "p1", CredentialKind: "bearer", CSRFToken: "synthetic-csrf-token"}
	if a.cookie {
		s.CredentialKind = "cookie"
	}
	return s, nil
}
func (a *testAuthority) Revalidate(ctx context.Context, s Subject) (Subject, error) {
	if a.revoked.Load() {
		return Subject{}, apitypes.Fail(403, "REVOKED")
	}
	return s, nil
}
func (a *testAuthority) Allow(context.Context, Subject, string, []apitypes.Ref) error {
	if a.revoked.Load() {
		return apitypes.Fail(403, "REVOKED")
	}
	return nil
}

type testGateway struct {
	execute func(context.Context, Subject, Query, requests.Command) (apitypes.Result, error)
	read    func(context.Context, Subject, Query) (Response, error)
}

func (g testGateway) Execute(ctx context.Context, s Subject, q Query, c requests.Command) (apitypes.Result, error) {
	if g.execute != nil {
		return g.execute(ctx, s, q, c)
	}
	return apitypes.Result{}, apitypes.Fail(503, "SERVICE_UNAVAILABLE")
}
func (g testGateway) Read(ctx context.Context, s Subject, q Query) (Response, error) {
	if g.read != nil {
		return g.read(ctx, s, q)
	}
	return Response{}, apitypes.Fail(503, "SERVICE_UNAVAILABLE")
}
func (g testGateway) Session(_ context.Context, _ Subject, q Query, _ json.RawMessage) (Response, error) {
	if q.Operation.ID == "deleteSession" {
		return Response{Status: 204}, nil
	}
	return Response{}, apitypes.Fail(503, "SERVICE_UNAVAILABLE")
}
func newTestHandler(t *testing.T, o Options) *Handler {
	t.Helper()
	h, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}
func call(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func commandHeaders(id string) map[string]string {
	return map[string]string{"Content-Type": "application/json", "Idempotency-Key": id, "X-OVS-Request-Epoch": epoch, "If-Match": "\"r1\""}
}
func assertProblem(t *testing.T, h *Handler, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var p apitypes.Problem
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Code != code || p.CommandEffect != "unknown" || !apitypes.UUID(p.CorrelationID) {
		t.Fatalf("problem: %s", w.Body)
	}
	if w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatal("wrong Problem media")
	}
	if err := h.contract.Schemas["Problem"].Validate(jsonValue(w.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
}
func TestProductionDefaultsAndContract(t *testing.T) {
	h := newTestHandler(t, Options{})
	for _, path := range []string{"/api/v1/contract", "/api/v1/openapi.json"} {
		if w := call(h, "GET", path, "", nil); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	assertProblem(t, h, call(h, "GET", "/api/v1/transactions", "", nil), 401, "UNAUTHENTICATED")
	assertProblem(t, h, call(h, "GET", "/api/v1/ports", "", map[string]string{"Authorization": "Bearer synthetic"}), 503, "AUTH_UNAVAILABLE")
	assertProblem(t, h, call(h, "GET", "/api/v2/ports", "", nil), 404, "NOT_FOUND")
	assertProblem(t, h, call(h, "PATCH", "/api/v1/bridges", "{}", nil), 405, "METHOD_NOT_ALLOWED")
}
func TestStrictAdmissionAndStrongPreconditions(t *testing.T) {
	h := newTestHandler(t, Options{Authorizer: &testAuthority{}, Gateway: testGateway{}, PublicOrigin: "https://console.example"})
	id := apitypes.RequestID(time.Now())
	body := fmt.Sprintf(`{"request_id":%q,"operation":"discard"}`, id)
	cases := []struct {
		name, body string
		edit       func(map[string]string)
		status     int
		code       string
	}{
		{"missing-tag", body, func(v map[string]string) { delete(v, "If-Match") }, 428, "PRECONDITION_REQUIRED"},
		{"weak-tag", body, func(v map[string]string) { v["If-Match"] = `W/"r1"` }, 428, "STRONG_PRECONDITION_REQUIRED"},
		{"wildcard", body, func(v map[string]string) { v["If-Match"] = `*` }, 428, "STRONG_PRECONDITION_REQUIRED"},
		{"duplicate", strings.TrimSuffix(body, "}") + `,"operation":"stage"}`, nil, 400, "INVALID_JSON"},
		{"unknown", strings.TrimSuffix(body, "}") + `,"injected":true}`, nil, 422, "INVALID_REQUEST"},
		{"malformed", `{"request_id":"\ud800"}`, nil, 400, "INVALID_JSON"},
		{"wrong-media", body, func(v map[string]string) { v["Content-Type"] = "text/plain" }, 415, "JSON_REQUIRED"},
		{"key-mismatch", body, func(v map[string]string) { v["Idempotency-Key"] = apitypes.RequestID(time.Now()) }, 409, "IDEMPOTENCY_KEY_MISMATCH"},
		{"origin", body, func(v map[string]string) { v["Origin"] = "https://hostile.example" }, 403, "ORIGIN_REJECTED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			headers := commandHeaders(id)
			if c.edit != nil {
				c.edit(headers)
			}
			assertProblem(t, h, call(h, "PATCH", "/api/v1/candidate", c.body, headers), c.status, c.code)
		})
	}
	assertProblem(t, h, call(h, "GET", "/api/v1/bridges?limit=501", "", nil), 422, "INVALID_PARAMETER")
	assertProblem(t, h, call(h, "GET", "/api/v1/bridges?limit=1&limit=2", "", nil), 400, "AMBIGUOUS_REQUEST")
	assertProblem(t, h, call(h, "GET", "/api/v1/bridges?offset=5", "", nil), 400, "UNKNOWN_PARAMETER")
	assertProblem(t, h, call(h, "GET", "/api/v1/%62ridges", "", nil), 400, "INVALID_REQUEST")
	assertProblem(t, h, call(h, "GET", "/api/v1/bridges", "{}", nil), 400, "UNEXPECTED_BODY")
	assertProblem(t, h, call(h, "PATCH", "/api/v1/candidate", strings.Repeat(" ", 1<<20)+body, commandHeaders(id)), 413, "REQUEST_TOO_LARGE")
}
func TestCookieCSRFAndLogout(t *testing.T) {
	h := newTestHandler(t, Options{Authorizer: &testAuthority{cookie: true}, Gateway: testGateway{}, PublicOrigin: "https://console.example"})
	assertProblem(t, h, call(h, "DELETE", "/api/v1/session", "", nil), 403, "ORIGIN_REJECTED")
	headers := map[string]string{"Origin": "https://console.example"}
	assertProblem(t, h, call(h, "DELETE", "/api/v1/session", "", headers), 403, "CSRF_REJECTED")
	headers["X-OVS-CSRF-Token"] = "synthetic-csrf-token"
	if w := call(h, "DELETE", "/api/v1/session", "", headers); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestGatewayResultsAndUncertainOutcomeNeverRetry(t *testing.T) {
	count := 0
	gateway := testGateway{execute: func(context.Context, Subject, Query, requests.Command) (apitypes.Result, error) {
		count++
		return apitypes.Result{}, repository.ErrCommitUnknown
	}}
	h := newTestHandler(t, Options{Authorizer: &testAuthority{}, Gateway: gateway})
	id := apitypes.RequestID(time.Now())
	body := fmt.Sprintf(`{"request_id":%q,"operation":"discard"}`, id)
	assertProblem(t, h, call(h, "PATCH", "/api/v1/candidate", body, commandHeaders(id)), 503, "RESULT_UNKNOWN")
	if count != 1 {
		t.Fatal("automatic retry")
	}
	h.gateway = testGateway{read: func(context.Context, Subject, Query) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"secret":"must-not-be-exposed"}`)}, nil
	}}
	w := call(h, "GET", "/api/v1/bridges", "", nil)
	assertProblem(t, h, w, 500, "INVALID_SERVICE_RESPONSE")
	if strings.Contains(w.Body.String(), "must-not-be-exposed") {
		t.Fatal("invalid service data leaked")
	}
}
func TestIndependentAdmissionBudgets(t *testing.T) {
	h := newTestHandler(t, Options{Authorizer: &testAuthority{}, Gateway: testGateway{}})
	for i := 0; i < cap(h.heavy); i++ {
		h.heavy <- struct{}{}
	}
	id := apitypes.RequestID(time.Now())
	assertProblem(t, h, call(h, "PATCH", "/api/v1/candidate", fmt.Sprintf(`{"request_id":%q,"operation":"discard"}`, id), commandHeaders(id)), 429, "RESOURCE_BUDGET_EXCEEDED")
	assertProblem(t, h, call(h, "GET", "/api/v1/bridges", "", nil), 503, "SERVICE_UNAVAILABLE")
}
func TestCursorScopeTamperAndExpiry(t *testing.T) {
	c := NewCursors()
	now := time.Now()
	c.now = func() time.Time { return now }
	scope := PageScope{Principal: principal, PermissionRevision: "p1", Operation: "listPorts", Filter: "bridge=x", Order: "id", Generation: epoch, Snapshot: resource}
	token, err := c.Issue(scope, "last-key", now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if key, err := c.Read(token, scope); err != nil || key != "last-key" {
		t.Fatal(key, err)
	}
	changed := []PageScope{scope, scope, scope, scope, scope, scope, scope}
	changed[0].Principal = resource
	changed[1].PermissionRevision = "p2"
	changed[2].Operation = "listBridges"
	changed[3].Filter = "bridge=y"
	changed[4].Order = "name"
	changed[5].Generation = resource
	changed[6].Snapshot = epoch
	for _, s := range changed {
		if _, err := c.Read(token, s); err == nil {
			t.Fatal("cross scope admitted")
		}
	}
	if _, err := c.Read(token+"x", scope); err == nil {
		t.Fatal("tamper accepted")
	}
	if _, err := NewCursors().Read(token, scope); err == nil {
		t.Fatal("restart accepted old cursor")
	}
	now = now.Add(30 * time.Second)
	if _, err := c.Read(token, scope); err == nil {
		t.Fatal("expired cursor")
	}
	if _, err := c.Issue(scope, "last", now.Add(31*time.Second)); err == nil {
		t.Fatal("excessive TTL")
	}
}
func TestProblemSanitization(t *testing.T) {
	p := problem(errors.New("synthetic secret"))
	if strings.Contains(p.Detail, "secret") {
		t.Fatal("internal error leaked")
	}
}

func TestAcceptedAndSecretReplayCannotMisrouteReceipt(t *testing.T) {
	id := apitypes.RequestID(time.Now())
	job := apitypes.Ref{Kind: "job", ID: resource}
	receipt := apitypes.Receipt{RequestID: id, Domain: "management", Epoch: epoch, State: "accepted", Effect: "linked-resource", Resource: &job, Job: &job, CorrelationID: repository.NewID()}
	accepted := apitypes.Accepted{RequestID: id, Domain: "management", Epoch: epoch, JobID: resource, Resource: &job, CorrelationID: receipt.CorrelationID}
	result := apitypes.Result{Status: 202, Receipt: receipt}
	result.Body, _ = json.Marshal(accepted)
	h := newTestHandler(t, Options{Authorizer: &testAuthority{}, Gateway: testGateway{execute: func(context.Context, Subject, Query, requests.Command) (apitypes.Result, error) { return result, nil }}})
	body := fmt.Sprintf(`{"request_id":%q}`, id)
	if w := call(h, "POST", "/api/v1/jobs/"+resource+"/cancellations", body, commandHeaders(id)); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	accepted.JobID = principal
	result.Body, _ = json.Marshal(accepted)
	assertProblem(t, h, call(h, "POST", "/api/v1/jobs/"+resource+"/cancellations", body, commandHeaders(id)), 500, "INVALID_SERVICE_RESPONSE")
	result.Status = 200
	result.Replayed = true
	result.Body, _ = json.Marshal(receipt)
	body = fmt.Sprintf(`{"request_id":%q,"name":"synthetic","scopes":["state.read"],"expires_at":%q}`, id, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if w := call(h, "POST", "/api/v1/tokens", body, commandHeaders(id)); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	receipt.Epoch = resource
	result.Body, _ = json.Marshal(receipt)
	assertProblem(t, h, call(h, "POST", "/api/v1/tokens", body, commandHeaders(id)), 500, "INVALID_SERVICE_RESPONSE")
}
