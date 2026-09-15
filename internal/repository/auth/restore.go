package auth

import (
	"context"
	"database/sql"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

// PrepareRestore is an explicit offline reconciliation after verified backup
// installation. Normal startup never calls it or extends credential lifetimes.
func (r *Repository) PrepareRestore(ctx context.Context) error {
	unlock, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		state, err := tlsState(ctx, tx)
		if err != nil {
			return err
		}
		if state.TrialID != "" {
			if err = r.finishTLS(ctx, tx, state, false); err != nil {
				return err
			}
		}
		now := r.now()
		for _, table := range []string{"grants", "api_tokens"} {
			if _, err = tx.ExecContext(ctx, "UPDATE "+table+" SET revoked_at=coalesce(revoked_at,?)", now.Unix()); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE auth_grants SET elevated_until=0"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE auth_state SET epoch=?,revision=?,high_watermark_ms=? WHERE singleton=1", repository.NewID(), repository.NewID(), now.UnixMilli()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_authority SET epoch=?,high_watermark_ms=? WHERE singleton=1", repository.NewID(), now.UnixMilli()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE inventory_state SET state='reconciliation-required',reason='management-restore',restore_required=1,pending_digest='' WHERE singleton=1"); err != nil {
			return err
		}
		return r.audit(ctx, tx, authn.Claims{}, "restore-security", "", "completed", "")
	})
}
