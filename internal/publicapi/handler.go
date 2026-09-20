// Package publicapi is the shared REST/WebSocket transport, not an OVS executor.
package publicapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

type Subject struct {
	ID, PermissionRevision, CredentialKind, CSRFToken string
	Credential                                        string `json:"-"`
	SessionCookie                                     string `json:"-"`
	TLSIdentity                                       string `json:"-"`
}
type Authorizer interface {
	Authenticate(context.Context, *http.Request) (Subject, error)
	// Revalidate returns the current policy revision, checking expiry/revocation.
	Revalidate(context.Context, Subject) (Subject, error)
	Allow(context.Context, Subject, string, []apitypes.Ref) error
}
type ClosedAuthority struct{}

func (ClosedAuthority) Authenticate(_ context.Context, r *http.Request) (Subject, error) {
	if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
		return Subject{}, apitypes.Fail(401, "UNAUTHENTICATED")
	}
	return Subject{}, apitypes.Fail(503, "AUTH_UNAVAILABLE")
}
func (ClosedAuthority) Revalidate(context.Context, Subject) (Subject, error) {
	return Subject{}, apitypes.Fail(503, "AUTH_UNAVAILABLE")
}
func (ClosedAuthority) Allow(context.Context, Subject, string, []apitypes.Ref) error {
	return apitypes.Fail(503, "AUTH_UNAVAILABLE")
}

type Query struct {
	Operation *apicontract.Operation
	Path      map[string]string
	Values    url.Values
}
type Response struct {
	Status int
	Body   json.RawMessage
	ETag   string
	Cookie *http.Cookie
}

// AuthorityGateway executes in the endpoint's owning process. Management
// implementations must use manager.db admission before any provider dispatch.
// A remote gateway is bound to a compiled typed IPC registry by #34/#37–#40.
type AuthorityGateway interface {
	Read(context.Context, Subject, Query) (Response, error)
	Execute(context.Context, Subject, Query, requests.Command) (apitypes.Result, error)
	Session(context.Context, Subject, Query, json.RawMessage) (Response, error)
}
type Options struct {
	Context      context.Context
	Authorizer   Authorizer
	Gateway      AuthorityGateway
	PublicOrigin string
	Runtime      func(context.Context) (any, error)
}
type Handler struct {
	contract                   *apicontract.Contract
	authorizer                 Authorizer
	gateway                    AuthorityGateway
	origin                     string
	runtime                    func(context.Context) (any, error)
	ctx                        context.Context
	hub                        *Hub
	read, control, heavy, auth chan struct{}
}

func New(o Options) (*Handler, error) {
	c, err := apicontract.New()
	if err != nil {
		return nil, err
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	if o.Authorizer == nil {
		o.Authorizer = ClosedAuthority{}
	}
	if o.PublicOrigin != "" {
		u, e := url.Parse(o.PublicOrigin)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, errors.New("INVALID_PUBLIC_ORIGIN")
		}
	}
	h := &Handler{contract: c, authorizer: o.Authorizer, gateway: o.Gateway, origin: o.PublicOrigin, runtime: o.Runtime, ctx: o.Context, read: make(chan struct{}, 8), control: make(chan struct{}, 4), heavy: make(chan struct{}, 2), auth: make(chan struct{}, 4)}
	h.hub = newHub(o.Context)
	return h, nil
}
func (h *Handler) Close()    { h.hub.Close() }
func (h *Handler) Hub() *Hub { return h.hub }

var strongETag = regexp.MustCompile(`^"[A-Za-z0-9_-]{1,128}"$`)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlation := repository.NewID()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	w.Header().Set("X-Correlation-ID", correlation)
	var commandID, domain *string
	fail := func(err error) {
		p := problem(err)
		p.CorrelationID = correlation
		p.RequestID = commandID
		p.RequestDomain = domain
		respond(w, p.Status, p, "application/problem+json")
	}
	if r.URL.RawPath != "" || len(r.URL.RawQuery) > 4096 || strings.Contains(r.URL.Path, "//") {
		fail(apitypes.Fail(400, "INVALID_REQUEST"))
		return
	}
	op, path, allowed := h.contract.Match(r.Method, r.URL.Path)
	if op == nil {
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			fail(apitypes.Fail(405, "METHOD_NOT_ALLOWED"))
		} else {
			fail(apitypes.Fail(404, "NOT_FOUND"))
		}
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(apitypes.Fail(400, "INVALID_QUERY"))
		return
	}
	for _, header := range []string{"Authorization", "Origin", "Content-Type", "Idempotency-Key", "X-OVS-Request-Epoch", "If-Match", "X-OVS-CSRF-Token"} {
		if len(r.Header.Values(header)) > 1 {
			fail(apitypes.Fail(400, "AMBIGUOUS_REQUEST"))
			return
		}
	}
	if r.Header.Get("Authorization") != "" && r.Header.Get("Cookie") != "" {
		fail(apitypes.Fail(400, "AMBIGUOUS_AUTHENTICATION"))
		return
	}
	if err = op.ValidateParameters(path, query, r.Header.Values); err != nil {
		fail(err)
		return
	}
	if tag := r.Header.Get("If-Match"); tag != "" && !strongETag.MatchString(tag) {
		fail(apitypes.Fail(428, "STRONG_PRECONDITION_REQUIRED"))
		return
	}
	if r.Method == http.MethodGet && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
		r.Close = true
		fail(apitypes.Fail(400, "UNEXPECTED_BODY"))
		return
	}
	if r.Method != http.MethodGet || op.ID == "subscribeResourceChanges" {
		if origin := r.Header.Get("Origin"); origin != "" && (h.origin == "" || origin != h.origin) {
			fail(apitypes.Fail(403, "ORIGIN_REJECTED"))
			return
		}
	}
	if op.ID == "readOpenAPI" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(apicontract.Document)
		return
	}
	if op.ID == "readContract" {
		respond(w, 200, map[string]any{"major": 1, "version": "1.7.0", "openapi_url": "/api/v1/openapi.json", "service_state": "availability-reported-by-runtime-and-domain-services", "request_domains": []string{"workspace", "management"}}, "application/json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	slots := h.read
	if op.Domain != "" {
		slots = h.heavy
	}
	if op.ID == "decideTransaction" || op.ID == "reconcileTransaction" {
		slots = h.control
	}
	if op.ID == "createSession" || op.ID == "deleteSession" || op.ID == "reauthenticateSession" {
		slots = h.auth
	}
	var release sync.Once
	free := func() { release.Do(func() { <-slots }) }
	select {
	case slots <- struct{}{}:
		defer free()
	default:
		fail(apitypes.Fail(429, "RESOURCE_BUDGET_EXCEEDED"))
		return
	}
	if op.ID == "readRuntime" {
		if h.runtime == nil {
			fail(apitypes.Fail(503, "MANAGEMENT_DEGRADED"))
			return
		}
		value, e := h.runtime(ctx)
		if e != nil {
			fail(e)
			return
		}
		respond(w, 200, value, "application/json")
		return
	}
	var subject Subject
	if op.ID == "createSession" && (h.origin == "" || r.Header.Get("Origin") != h.origin) {
		fail(apitypes.Fail(403, "ORIGIN_REJECTED"))
		return
	}
	if !op.Public {
		subject, err = h.authorizer.Authenticate(ctx, r)
		if err != nil {
			fail(err)
			return
		}
		if !apitypes.ManagementID(subject.ID) || subject.PermissionRevision == "" {
			fail(apitypes.Fail(503, "AUTH_UNAVAILABLE"))
			return
		}
		refs := []apitypes.Ref{}
		for _, id := range path {
			// The selected receipt domain owns its principal/resource check. A
			// workspace receipt does not have a duplicate row in manager.db.
			if op.ID != "readRequestReceipt" {
				refs = append(refs, apitypes.Ref{Kind: op.ResourceKind, ID: id})
			}
		}
		if err = h.authorizer.Allow(ctx, subject, op.Capability, refs); err != nil {
			fail(err)
			return
		}
		if subject.CredentialKind == "cookie" {
			if (r.Method != http.MethodGet || op.ID == "subscribeResourceChanges") && (r.Header.Get("Origin") == "" || h.origin == "") {
				fail(apitypes.Fail(403, "ORIGIN_REJECTED"))
				return
			}
			if r.Method != http.MethodGet && (len(subject.CSRFToken) < 16 || subtle.ConstantTimeCompare([]byte(subject.CSRFToken), []byte(r.Header.Get("X-OVS-CSRF-Token"))) != 1) {
				fail(apitypes.Fail(403, "CSRF_REJECTED"))
				return
			}
		}
	}
	if op.ID == "subscribeResourceChanges" {
		free()
		h.stream(w, r, subject)
		return
	}
	q := Query{Operation: op, Path: path, Values: query}
	if op.ID == "activateCertificate" {
		w.Header().Set("Connection", "close")
	}
	var body []byte
	var value map[string]any
	if op.Body != nil {
		media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
			fail(apitypes.Fail(415, "JSON_REQUIRED"))
			return
		}
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, ipc.MaxBodyBytes))
		if err != nil {
			r.Close = true
			fail(apitypes.Fail(413, "REQUEST_TOO_LARGE"))
			return
		}
		value, body, err = op.Decode(body)
		if err != nil {
			fail(err)
			return
		}
	} else if r.Method != http.MethodGet && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
		fail(apitypes.Fail(400, "UNEXPECTED_BODY"))
		return
	}
	if h.gateway == nil {
		fail(apitypes.Fail(503, "SERVICE_UNAVAILABLE"))
		return
	}
	if op.ID == "createSession" || op.ID == "deleteSession" || op.ID == "reauthenticateSession" {
		response, e := h.gateway.Session(ctx, subject, q, body)
		if e != nil {
			fail(e)
			return
		}
		if e = op.ValidateResponse(response.Status, response.Body); e != nil {
			fail(e)
			return
		}
		sendResponse(w, response)
		return
	}
	if op.Domain == "" {
		response, e := h.gateway.Read(ctx, subject, q)
		if e != nil {
			fail(e)
			return
		}
		if e = op.ValidateResponse(response.Status, response.Body); e != nil {
			fail(e)
			return
		}
		sendResponse(w, response)
		return
	}
	id, ok := value["request_id"].(string)
	if !ok || id != r.Header.Get("Idempotency-Key") {
		fail(apitypes.Fail(409, "IDEMPOTENCY_KEY_MISMATCH"))
		return
	}
	commandID = &id
	domain = &op.Domain
	c := requests.Command{Principal: subject.ID, Epoch: r.Header.Get("X-OVS-Request-Epoch"), Domain: op.Domain, ID: id, Operation: op.ID, Method: r.Method, URI: r.URL.Path, Precondition: r.Header.Get("If-Match"), Payload: body, Sensitive: op.Sensitive, SecretResponse: op.SecretResponse}
	if len(query) > 0 {
		c.URI += "?" + query.Encode()
	}
	result, e := h.gateway.Execute(ctx, subject, q, c)
	if e != nil {
		fail(e)
		return
	}
	if result.Receipt.RequestID != id || result.Receipt.Domain != op.Domain || result.Receipt.Epoch != c.Epoch || !apitypes.ManagementID(result.Receipt.CorrelationID) {
		fail(apitypes.Fail(500, "INVALID_SERVICE_RESPONSE"))
		return
	}
	if result.Status == 202 {
		var accepted apitypes.Accepted
		if json.Unmarshal(result.Body, &accepted) != nil || accepted.RequestID != id || accepted.Epoch != c.Epoch || accepted.Domain != op.Domain || accepted.CorrelationID != result.Receipt.CorrelationID || result.Receipt.Job == nil || accepted.JobID != result.Receipt.Job.ID || !sameRef(accepted.Resource, result.Receipt.Resource) {
			fail(apitypes.Fail(500, "INVALID_SERVICE_RESPONSE"))
			return
		}
	}
	if result.Replayed && op.SecretResponse {
		var receipt apitypes.Receipt
		if e = h.contract.Schemas["RequestReceipt"].Validate(jsonValue(result.Body)); e != nil || result.Status != 200 || json.Unmarshal(result.Body, &receipt) != nil || !sameReceipt(receipt, result.Receipt) {
			fail(apitypes.Fail(500, "INVALID_SERVICE_RESPONSE"))
			return
		}
	} else if e = op.ValidateResponse(result.Status, result.Body); e != nil {
		fail(e)
		return
	}
	w.Header().Set("X-Correlation-ID", result.Receipt.CorrelationID)
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	sendResponse(w, Response{Status: result.Status, Body: result.Body})
}
func jsonValue(data []byte) any { var v any; _ = json.Unmarshal(data, &v); return v }
func sameRef(a, b *apitypes.Ref) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func sameReceipt(a, b apitypes.Receipt) bool {
	return a.RequestID == b.RequestID && a.Domain == b.Domain && a.Epoch == b.Epoch && a.State == b.State && a.Effect == b.Effect && a.CorrelationID == b.CorrelationID && sameRef(a.Resource, b.Resource) && sameRef(a.Job, b.Job)
}
func sendResponse(w http.ResponseWriter, r Response) {
	if r.Cookie != nil {
		http.SetCookie(w, r.Cookie)
	}
	if r.ETag == "" && len(r.Body) > 0 {
		var value struct {
			Revision string `json:"revision"`
		}
		if json.Unmarshal(r.Body, &value) == nil && value.Revision != "" {
			r.ETag = `"` + value.Revision + `"`
		}
	}
	if r.ETag != "" && strongETag.MatchString(r.ETag) {
		w.Header().Set("ETag", r.ETag)
	}
	if r.Status == 204 {
		w.WriteHeader(204)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.Status)
	_, _ = w.Write(r.Body)
}
func respond(w http.ResponseWriter, status int, value any, media string) {
	w.Header().Set("Content-Type", media)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(err error) *apitypes.Problem {
	var p *apitypes.Problem
	if errors.As(err, &p) {
		copy := *p
		return &copy
	}
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return apitypes.Fail(404, "NOT_FOUND")
	case errors.Is(err, repository.ErrBusy):
		return apitypes.Fail(429, "RESOURCE_BUDGET_EXCEEDED")
	case errors.Is(err, repository.ErrCommitUnknown):
		return apitypes.Fail(503, "RESULT_UNKNOWN")
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, repository.ErrCanceled):
		return apitypes.Fail(503, "REQUEST_INTERRUPTED")
	default:
		return apitypes.Fail(503, "SERVICE_UNAVAILABLE")
	}
}
