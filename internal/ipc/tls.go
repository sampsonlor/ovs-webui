package ipc

import (
	"context"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
	"net/http"
	"strings"
)

func (h *Handler) tlsOperation(w http.ResponseWriter, r *http.Request) {
	if h.tlsManager == nil {
		h.problem(w, r, 503, "TLS_AUTHORITY_UNAVAILABLE")
		return
	}
	class := Control
	if strings.HasSuffix(r.URL.Path, "tls.execute") {
		class = Heavy
	}
	free, err := h.budgets[class].acquire(r.Context())
	if err != nil {
		h.queueError(w, r, err)
		return
	}
	defer free()
	var out any
	if strings.HasSuffix(r.URL.Path, "tls.state") {
		var input struct{}
		if !h.decode(w, r, &input) {
			return
		}
		if len(r.Header.Values("Authorization")) != 0 {
			h.problem(w, r, 400, "AMBIGUOUS_AUTHENTICATION")
			return
		}
		out, err = h.tlsManager.TLSState(r.Context())
	} else {
		var input tlscontrol.Command
		if !h.decode(w, r, &input) {
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			h.problem(w, r, 401, "UNAUTHENTICATED")
			return
		}
		grant := strings.TrimPrefix(values[0], "Bearer ")
		if !authn.ValidSecret(grant, "ovsg_") {
			h.problem(w, r, 403, "BROWSER_SESSION_REQUIRED")
			return
		}
		out, err = h.tlsManager.ExecuteTLS(r.Context(), grant, input)
	}
	if err != nil {
		var p *apitypes.Problem
		if errors.As(err, &p) {
			h.problem(w, r, p.Status, p.Code)
		} else {
			h.problem(w, r, 503, "TLS_AUTHORITY_UNAVAILABLE")
		}
		return
	}
	h.write(w, 200, out)
}
func (c *Client) ExecuteTLS(ctx context.Context, grant string, in tlscontrol.Command) (out apitypes.Result, err error) {
	err = c.callAuth(ctx, "tls.execute", grant, in, &out)
	return
}
func (c *Client) TLSState(ctx context.Context) (out tlscontrol.State, err error) {
	err = c.callAuth(ctx, "tls.state", "", struct{}{}, &out)
	return
}
