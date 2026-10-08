package executions

import (
	"context"
	"database/sql"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

func protectDeletion(ctx context.Context, tx *sql.Tx, id string, i candidate.StoredIntent) error {
	d := i.Deletion
	return protectGraph(ctx, tx, id, d.Source.Root, append(d.Source.Bindings(), d.Replacement.Bindings()...))
}

func protectGraph(ctx context.Context, tx *sql.Tx, id, root string, bindings []candidate.Binding) error {
	protect := func(resource, field string) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE resource_id=?", resource).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return apitypes.Fail(409, "FIELD_PROTECTED")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO operation_protections VALUES(?,?,?)", resource, field, id)
		return err
	}
	if err := protect(root, "root.bridge-creation"); err != nil {
		return err
	}
	for _, b := range bindings {
		if err := protect(b.ManagementID, "object.lifecycle"); err != nil {
			return err
		}
	}
	return nil
}

// Only a settled, proven compensation makes replacement identities usable.
// Reservations and same-name objects are not evidence of successful recovery.
func identityReplacements(r execution.Record, s safety.Record) []map[string]any {
	out := []map[string]any{}
	for _, i := range r.Plan.Envelope.Candidate.Intents {
		var previous, replacement []candidate.Binding
		if i.Topology != nil {
			for _, n := range i.Topology.Before {
				if b, ok := i.Topology.Replacements[n.Binding.OVSUUID]; ok {
					previous = append(previous, n.Binding)
					replacement = append(replacement, b)
				}
			}
		} else if i.Operation == candidate.BridgeDelete && i.Deletion != nil {
			previous, replacement = i.Deletion.Source.Bindings(), i.Deletion.Replacement.Bindings()
		} else if i.Operation == candidate.InternalPortDelete && i.PortDeletion != nil {
			previous, replacement = i.PortDeletion.Source.Bindings(), i.PortDeletion.Replacement.Bindings()
		} else {
			continue
		}
		state := "unverified"
		switch s.State {
		case "preparing", "awaiting-confirmation", "rollback-requested", "rolling-back":
			state = "reserved"
		case "confirmed", "not-committed":
			state = "not-used"
		case "rolled-back":
			if s.Rollback != nil && s.Outcome.Commit == "committed" && s.Outcome.Applied == "applied" && s.HealthyAt != nil {
				state = "restored"
			}
		}
		for k := range previous {
			out = append(out, map[string]any{"previous": previous[k], "replacement": replacement[k], "state": state})
		}
	}
	return out
}
