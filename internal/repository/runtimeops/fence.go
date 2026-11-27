// Package runtimeops holds the durable mutual exclusion between product
// service control, Safe Apply and temporary TLS activation.
package runtimeops

import (
	"context"
	"database/sql"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

type queryRow interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func RequireNoRestart(ctx context.Context, q queryRow) error {
	var active bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runtime_service_restart WHERE singleton=1)").Scan(&active); err != nil {
		return err
	}
	if active {
		return apitypes.Fail(409, "MANAGEMENT_RESTART_IN_PROGRESS")
	}
	return nil
}

// RequireRestartAdmission runs within the writer transaction that inserts the
// restart fence. The Safe Apply and TLS writers check the reciprocal guard.
func RequireRestartAdmission(ctx context.Context, q queryRow) error {
	if err := RequireNoRestart(ctx, q); err != nil {
		return err
	}
	var safety, tls bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM safe_applies WHERE state NOT IN ('confirmed','rolled-back','not-committed')), EXISTS(SELECT 1 FROM tls_authority WHERE singleton=1 AND trial_id<>'')").Scan(&safety, &tls)
	if err != nil {
		return err
	}
	if safety {
		return apitypes.Fail(409, "MANAGEMENT_PATH_PROTECTED")
	}
	if tls {
		return apitypes.Fail(409, "TLS_RECOVERY_IN_PROGRESS")
	}
	return nil
}
