package inventory

import (
	"context"
	"database/sql"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	domain "github.com/sampsonlor/ovs-webui/internal/inventory"
)

const CreationMarker = candidate.BridgeCreationMarker

// ReserveCreations shares the durable admission transaction. Neither the HTTP
// caller nor OVS external_ids assigns management identity. UUIDs cannot be reused
// after failure, rollback or deletion, even if the display name is unchanged.
func ReserveCreations(ctx context.Context, tx *sql.Tx, transaction, marker string, c candidate.Candidate) error {
	for _, i := range c.Intents {
		if i.Operation != candidate.BridgeCreate {
			continue
		}
		if i.Creation == nil || i.Creation.BeforePresent || !i.Creation.AfterPresent {
			return apitypes.Fail(422, "INVALID_CREATION_RESERVATION")
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM identities").Scan(&count); err != nil {
			return err
		}
		if count+3 > domain.MaxIdentities {
			return apitypes.Fail(503, "IDENTITY_REGISTRY_FULL")
		}
		for _, b := range candidate.CreationBindings(i) {
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM identities WHERE management_id=? OR (generation=? AND table_name=? AND ovs_uuid=?)", b.ManagementID, b.Generation, b.Table, b.OVSUUID).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return apitypes.Fail(409, "CREATION_IDENTITY_CONSUMED")
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO identities VALUES(?,?,?,?,'pending')", b.ManagementID, b.Generation, b.Table, b.OVSUUID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO identity_creations VALUES(?,?,?)", b.ManagementID, transaction, marker); err != nil {
				return err
			}
		}
	}
	return nil
}

func RetirePending(ctx context.Context, tx *sql.Tx, transaction string) error {
	_, err := tx.ExecContext(ctx, "UPDATE identities SET state='tombstone' WHERE state='pending' AND management_id IN (SELECT management_id FROM identity_creations WHERE transaction_id=?)", transaction)
	return err
}
