package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sessions"
)

const sessionCookie = "__Host-ovs_session"

type Authentication struct {
	manager      authn.Manager
	sessions     *sessions.Repository
	certificates *ManagedTLS
	workspace    *Workspace
}

func NewAuthentication(manager authn.Manager, sessions *sessions.Repository) *Authentication {
	return &Authentication{manager: manager, sessions: sessions}
}
func (a *Authentication) WithWorkspace(w *Workspace) *Authentication { a.workspace = w; return a }
func (a *Authentication) WithCertificates(c *ManagedTLS) *Authentication {
	a.certificates = c
	return a
}
func authError(err error) error {
	if err == nil {
		return nil
	}
	var remote *ipc.RemoteError
	if errors.As(err, &remote) {
		return apitypes.Fail(remote.Status, remote.Code)
	}
	var p *apitypes.Problem
	if errors.As(err, &p) {
		return p
	}
	if errors.Is(err, repository.ErrNotFound) {
		return apitypes.Fail(401, "UNAUTHENTICATED")
	}
	return apitypes.Fail(503, "AUTH_UNAVAILABLE")
}
func (a *Authentication) Authenticate(ctx context.Context, r *http.Request) (publicapi.Subject, error) {
	var subject publicapi.Subject
	if r.Header.Get("Authorization") != "" {
		if len(r.Header.Values("Authorization")) != 1 || r.Header.Get("Cookie") != "" {
			return subject, apitypes.Fail(400, "AMBIGUOUS_AUTHENTICATION")
		}
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			return subject, apitypes.Fail(401, "UNAUTHENTICATED")
		}
		credential := strings.TrimPrefix(raw, "Bearer ")
		if !authn.ValidSecret(credential, "ovst_") {
			return subject, apitypes.Fail(401, "API_TOKEN_REQUIRED")
		}
		c, err := a.manager.InspectAuth(ctx, credential)
		if err != nil {
			return subject, authError(err)
		}
		if c.CredentialKind != "token" {
			return subject, apitypes.Fail(401, "API_TOKEN_REQUIRED")
		}
		return publicapi.Subject{ID: c.PrincipalID, PermissionRevision: c.Revision, CredentialKind: "bearer", Credential: credential}, nil
	}
	cookies := r.CookiesNamed(sessionCookie)
	if len(cookies) != 1 {
		return subject, apitypes.Fail(401, "UNAUTHENTICATED")
	}
	cookie := cookies[0].Value
	m, err := a.sessions.Load(ctx, cookie)
	if err != nil {
		return subject, authError(err)
	}
	c, err := a.manager.InspectAuth(ctx, m.Grant)
	if err != nil {
		return subject, authError(err)
	}
	if c.PrincipalID != m.PrincipalID || c.CredentialKind != "grant" {
		return subject, apitypes.Fail(401, "SESSION_INVALID")
	}
	return publicapi.Subject{ID: c.PrincipalID, PermissionRevision: c.Revision, CredentialKind: "cookie", CSRFToken: m.CSRF, Credential: m.Grant, SessionCookie: cookie, TLSIdentity: servedCertificate(r.Context())}, nil
}
func (a *Authentication) Revalidate(ctx context.Context, s publicapi.Subject) (publicapi.Subject, error) {
	// Re-read the sealed mapping as well, so deleted/replaced local sessions stop
	// WebSocket subscriptions. Manager expiry/ceiling/revocation remains authority.
	if s.CredentialKind == "cookie" {
		m, err := a.sessions.Load(ctx, s.SessionCookie)
		if err != nil {
			return s, authError(err)
		}
		if m.Grant != s.Credential || m.PrincipalID != s.ID || m.CSRF != s.CSRFToken {
			return s, apitypes.Fail(401, "SESSION_INVALID")
		}
	}
	c, err := a.manager.InspectAuth(ctx, s.Credential)
	if err != nil {
		return s, authError(err)
	}
	if c.PrincipalID != s.ID {
		return s, apitypes.Fail(401, "SESSION_INVALID")
	}
	s.PermissionRevision = c.Revision
	return s, nil
}
func (a *Authentication) Allow(ctx context.Context, s publicapi.Subject, capability string, refs []apitypes.Ref) error {
	c, err := a.manager.CheckAuth(ctx, s.Credential, authn.Check{Capability: capability, Resources: refs})
	if err != nil {
		return authError(err)
	}
	if c.PrincipalID != s.ID {
		return apitypes.Fail(401, "SESSION_INVALID")
	}
	return nil
}
func (a *Authentication) representation(ctx context.Context, c authn.Claims, csrf string) (json.RawMessage, error) {
	epoch, err := a.sessions.Epoch(ctx)
	if err != nil {
		return nil, err
	}
	// Auth and request epochs are deliberately separate. Fetch the management
	// request epoch from mgrd's session response extension, not web.db metadata.
	return json.Marshal(map[string]any{"principal_id": c.PrincipalID, "display_name": c.DisplayName, "effective_capabilities": c.Capabilities, "expires_at": c.ExpiresAt, "csrf_token": csrf, "request_epochs": map[string]string{"workspace": epoch, "management": c.RequestEpoch}, "server_time": time.Now().UTC(), "elevated_until": c.ElevatedUntil})
}
func (a *Authentication) Session(ctx context.Context, s publicapi.Subject, q publicapi.Query, body json.RawMessage) (publicapi.Response, error) {
	var out publicapi.Response
	switch q.Operation.ID {
	case "createSession":
		var in authn.Login
		if err := ipc.DecodeStrict(body, &in); err != nil {
			return out, apitypes.Fail(422, "INVALID_REQUEST")
		}
		result, err := a.manager.Authenticate(ctx, in)
		if err != nil {
			return out, authError(err)
		}
		cookie := authn.Secret("ovss_")
		csrf := authn.Secret("ovsc_")
		mapping := sessions.Mapping{Grant: result.Grant, PrincipalID: result.Claims.PrincipalID, CSRF: csrf, ExpiresAt: result.Claims.ExpiresAt}
		if err = a.sessions.Save(ctx, cookie, mapping); err != nil {
			_ = a.manager.RevokeAuth(ctx, result.Grant)
			return out, authError(err)
		}
		out.Body, err = a.representation(ctx, result.Claims, csrf)
		if err != nil {
			_ = a.manager.RevokeAuth(ctx, result.Grant)
			_ = a.sessions.Delete(ctx, cookie)
			return out, authError(err)
		}
		out.Status = 201
		out.Cookie = &http.Cookie{Name: sessionCookie, Value: cookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: mapping.ExpiresAt}
	case "reauthenticateSession":
		if s.CredentialKind != "cookie" {
			return out, apitypes.Fail(403, "BROWSER_SESSION_REQUIRED")
		}
		var in authn.Reauthentication
		if ipc.DecodeStrict(body, &in) != nil {
			return out, apitypes.Fail(422, "INVALID_REQUEST")
		}
		c, err := a.manager.Reauthenticate(ctx, s.Credential, in)
		if err != nil {
			return out, authError(err)
		}
		out.Status = 200
		out.Body, err = a.representation(ctx, c, s.CSRFToken)
		if err != nil {
			return out, authError(err)
		}
	case "deleteSession":
		if s.CredentialKind != "cookie" {
			return out, apitypes.Fail(403, "BROWSER_SESSION_REQUIRED")
		}
		if err := a.manager.RevokeAuth(ctx, s.Credential); err != nil {
			return out, authError(err)
		}
		if err := a.sessions.Delete(ctx, s.SessionCookie); err != nil {
			return out, authError(err)
		}
		out.Status = 204
		out.Cookie = &http.Cookie{Name: sessionCookie, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)}
	default:
		return out, apitypes.Fail(503, "SERVICE_UNAVAILABLE")
	}
	return out, nil
}
func (a *Authentication) Read(ctx context.Context, s publicapi.Subject, q publicapi.Query) (publicapi.Response, error) {
	if q.Operation.ID == "readCandidate" || q.Operation.ID == "readWorkspace" || q.Operation.ID == "readValidation" || q.Operation.ID == "readRequestReceipt" && q.Values.Get("domain") == "workspace" {
		if a.workspace == nil {
			return publicapi.Response{}, apitypes.Fail(503, "WORKSPACE_UNAVAILABLE")
		}
		out, err := a.workspace.Read(ctx, s, q)
		return out, authError(err)
	}
	uri := q.Operation.Path
	for key, id := range q.Path {
		uri = strings.ReplaceAll(uri, "{"+key+"}", id)
	}
	if len(q.Values) > 0 {
		uri += "?" + q.Values.Encode()
	}
	input := authn.Query{Method: q.Operation.Method, URI: uri}
	var result authn.Response
	var err error
	if reader, ok := a.manager.(interface {
		ReadInventory(context.Context, string, authn.Query) (authn.Response, error)
	}); ok && inventory.Operation(q.Operation.ID) {
		result, err = reader.ReadInventory(ctx, s.Credential, input)
	} else {
		result, err = a.manager.ReadAuth(ctx, s.Credential, input)
	}
	if err != nil {
		return publicapi.Response{}, authError(err)
	}
	if q.Operation.ID == "readSession" {
		var c authn.Claims
		if json.Unmarshal(result.Body, &c) != nil || c.PrincipalID != s.ID {
			return publicapi.Response{}, apitypes.Fail(503, "AUTH_RESPONSE_INVALID")
		}
		result.Body, err = a.representation(ctx, c, s.CSRFToken)
		if err != nil {
			return publicapi.Response{}, authError(err)
		}
	}
	return publicapi.Response{Status: result.Status, Body: result.Body, ETag: result.ETag}, nil
}
func (a *Authentication) Execute(ctx context.Context, s publicapi.Subject, q publicapi.Query, c requests.Command) (apitypes.Result, error) {
	if q.Operation.ID == "changeCandidate" || q.Operation.ID == "createValidation" || q.Operation.ID == "createTransaction" {
		if a.workspace == nil {
			return apitypes.Result{}, apitypes.Fail(503, "WORKSPACE_UNAVAILABLE")
		}
		out, err := a.workspace.Execute(ctx, s, q, c)
		return out, authError(err)
	}
	if q.Operation.ID == "createCertificate" || q.Operation.ID == "activateCertificate" || q.Operation.ID == "confirmCertificate" {
		if a.certificates == nil {
			return apitypes.Result{}, apitypes.Fail(503, "TLS_STORE_UNAVAILABLE")
		}
		return a.certificates.Execute(ctx, s, q, c)
	}
	result, err := a.manager.ExecuteAuth(ctx, s.Credential, authn.Command{Method: c.Method, URI: c.URI, Epoch: c.Epoch, RequestID: c.ID, Precondition: c.Precondition, Payload: c.Payload})
	return result, authError(err)
}
