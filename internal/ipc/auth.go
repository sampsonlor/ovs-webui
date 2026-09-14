package ipc

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

func (h *Handler) authOperation(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		h.problem(w, r, 503, "AUTH_UNAVAILABLE")
		return
	}
	op := strings.TrimPrefix(r.URL.Path, "/ipc/v1/operations/")
	class := Read
	switch op {
	case "auth.authenticate", "auth.reauthenticate":
		class = Auth
	case "auth.revoke":
		class = Control
	case "security.execute":
		class = Heavy
	}
	free, err := h.budgets[class].acquire(r.Context())
	if err != nil {
		h.queueError(w, r, err)
		return
	}
	defer free()
	credential := ""
	if op == "auth.authenticate" {
		if len(r.Header.Values("Authorization")) != 0 {
			h.problem(w, r, 400, "AMBIGUOUS_AUTHENTICATION")
			return
		}
	} else {
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			h.problem(w, r, 401, "UNAUTHENTICATED")
			return
		}
		credential = strings.TrimPrefix(values[0], "Bearer ")
		if !authn.ValidSecret(credential, "ovsg_") && !authn.ValidSecret(credential, "ovst_") {
			h.problem(w, r, 401, "UNAUTHENTICATED")
			return
		}
	}
	var out any
	switch op {
	case "auth.authenticate":
		var in authn.Login
		if !h.decode(w, r, &in) {
			return
		}
		out, err = h.auth.Authenticate(r.Context(), in)
	case "auth.inspect":
		var in struct{}
		if !h.decode(w, r, &in) {
			return
		}
		out, err = h.auth.InspectAuth(r.Context(), credential)
	case "auth.check":
		var in authn.Check
		if !h.decode(w, r, &in) {
			return
		}
		out, err = h.auth.CheckAuth(r.Context(), credential, in)
	case "auth.reauthenticate":
		var in authn.Reauthentication
		if !h.decode(w, r, &in) {
			return
		}
		out, err = h.auth.Reauthenticate(r.Context(), credential, in)
	case "auth.revoke":
		var in struct{}
		if !h.decode(w, r, &in) {
			return
		}
		err = h.auth.RevokeAuth(r.Context(), credential)
		out = struct{}{}
	case "security.read":
		var in authn.Query
		if !h.decode(w, r, &in) {
			return
		}
		out, err = h.auth.ReadAuth(r.Context(), credential, in)
	case "security.execute":
		var in authn.Command
		if !h.decode(w, r, &in) {
			return
		}
		out, err = h.auth.ExecuteAuth(r.Context(), credential, in)
	}
	if err != nil {
		var p *apitypes.Problem
		if errors.As(err, &p) {
			h.problem(w, r, p.Status, p.Code)
		} else {
			h.problem(w, r, 503, "AUTH_UNAVAILABLE")
		}
		return
	}
	if r.Context().Err() != nil {
		h.problem(w, r, 503, "REQUEST_INTERRUPTED")
		return
	}
	h.write(w, 200, out)
}
func (c *Client) callAuth(ctx context.Context, op, credential string, input, output any) error {
	return c.withConnection(ctx, func(conn net.Conn, reader *bufio.Reader) error {
		return exchange(conn, reader, http.MethodPost, "/ipc/v1/operations/"+op, credential, input, output)
	})
}
func (c *Client) Authenticate(ctx context.Context, in authn.Login) (out authn.LoginResult, err error) {
	err = c.callAuth(ctx, "auth.authenticate", "", in, &out)
	return
}
func (c *Client) InspectAuth(ctx context.Context, g string) (out authn.Claims, err error) {
	err = c.callAuth(ctx, "auth.inspect", g, struct{}{}, &out)
	return
}
func (c *Client) CheckAuth(ctx context.Context, g string, in authn.Check) (out authn.Claims, err error) {
	err = c.callAuth(ctx, "auth.check", g, in, &out)
	return
}
func (c *Client) Reauthenticate(ctx context.Context, g string, in authn.Reauthentication) (out authn.Claims, err error) {
	err = c.callAuth(ctx, "auth.reauthenticate", g, in, &out)
	return
}
func (c *Client) RevokeAuth(ctx context.Context, g string) error {
	var out struct{}
	return c.callAuth(ctx, "auth.revoke", g, struct{}{}, &out)
}
func (c *Client) ReadAuth(ctx context.Context, g string, in authn.Query) (out authn.Response, err error) {
	err = c.callAuth(ctx, "security.read", g, in, &out)
	return
}
func (c *Client) ExecuteAuth(ctx context.Context, g string, in authn.Command) (out apitypes.Result, err error) {
	err = c.callAuth(ctx, "security.execute", g, in, &out)
	return
}
