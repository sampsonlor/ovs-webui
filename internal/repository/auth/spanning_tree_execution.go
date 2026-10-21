package auth

import (
	"context"
	"database/sql"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const SpanningTreeExecutionCapability = "ovs.bridge.spanning-tree.write"

// SpanningTreeExecutionAuthorizer is a private preflight prerequisite, not an
// HTTP/IPC operation or an execution grant. A caller must invoke it before any
// provider access and again after I/O. The credential stays only in the closure;
// every invocation resolves current claims and the credential's original ceiling.
func (r *Repository) SpanningTreeExecutionAuthorizer(credential string) func(context.Context) error {
	return func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			c, err := r.claims(ctx, q, credential)
			if err != nil {
				return err
			}
			for _, cap := range []string{"inventory.read", "configuration.read", "workspace.write", "configuration.apply", SpanningTreeExecutionCapability} {
				if err = require(c, cap, false, r.now().Unix()); err != nil {
					return err
				}
			}
			if ctx.Err() != nil {
				return apitypes.Fail(503, "SPANNING_TREE_PREFLIGHT_CANCELLED")
			}
			return nil
		})
	}
}
