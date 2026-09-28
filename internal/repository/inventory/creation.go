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
		if i.Operation != candidate.BridgeCreate && i.Operation != candidate.InternalPortCreate {
			continue
		}
		valid := i.Operation == candidate.BridgeCreate && i.Creation != nil && !i.Creation.BeforePresent && i.Creation.AfterPresent || i.Operation == candidate.InternalPortCreate && i.PortCreation != nil && !i.PortCreation.BeforePresent && i.PortCreation.AfterPresent
		if !valid {
			return apitypes.Fail(422, "INVALID_CREATION_RESERVATION")
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM identities").Scan(&count); err != nil {
			return err
		}
		if count+len(candidate.CreationBindings(i)) > domain.MaxIdentities {
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

// Reserve compensation identities before deleting anything. The same durable
// admission either reserves all three bindings and the journal, or does neither.
// Normal deletion confirmation retires these unused reservations permanently.
func ReserveRestorations(ctx context.Context, tx *sql.Tx, transaction, marker string, c candidate.Candidate) error {
	for _, i := range c.Intents {
		if i.Operation != candidate.BridgeDelete {
			continue
		}
		if i.Deletion == nil || i.Deletion.Restoring || i.Deletion.Observed {
			return apitypes.Fail(422, "INVALID_RESTORATION_RESERVATION")
		}
		g := i.Deletion.Replacement
		creation := candidate.StoredIntent{Operation: candidate.BridgeCreate, Object: g.Bridge,
			Creation: &candidate.BridgeCreation{Name: g.Name, Root: g.Root, Port: g.Port, Interface: g.Interface, AfterPresent: true}}
		if err := ReserveCreations(ctx, tx, transaction, marker, candidate.Candidate{Intents: []candidate.StoredIntent{creation}}); err != nil {
			return err
		}
	}
	return nil
}
