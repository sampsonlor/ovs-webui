package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
)

// SetIsolationExecutionRole is an offline root provisioning operation. There
// is no HTTP/IPC entrypoint. Existing credentials keep their original ceilings.
func (r *Repository) SetIsolationExecutionRole(ctx context.Context, id string, enabled bool) error {
	if !apitypes.ManagementID(id) {
		return apitypes.Fail(422, "INVALID_ROLE")
	}
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT r.capabilities FROM roles r JOIN auth_roles a ON a.role_id=r.id WHERE r.id=?", id).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return apitypes.Fail(404, "ROLE_NOT_FOUND")
		} else if err != nil {
			return err
		}
		var caps []string
		if json.Unmarshal(raw, &caps) != nil || validateCapabilities(caps) != nil {
			return repository.ErrUnavailable
		}
		if slices.Contains(caps, plan.PortIsolationCapability) == enabled {
			return nil
		}
		caps = slices.DeleteFunc(caps, func(cap string) bool { return cap == plan.PortIsolationCapability })
		result := "revoked"
		if enabled {
			caps = append(caps, plan.PortIsolationCapability)
			result = "granted"
		}
		data, _ := json.Marshal(unique(caps))
		if _, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id=?", data, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE auth_roles SET revision=? WHERE role_id=?", repository.NewID(), id); err != nil {
			return err
		}
		_, err := evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "port-isolation-execution-role", Result: result, Object: &apitypes.Ref{Kind: "role", ID: id}, Reason: "offline-root-policy", Created: r.now().UTC()})
		return err
	})
}
