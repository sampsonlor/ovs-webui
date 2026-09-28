package ovsdb

import (
	"bufio"
	"context"
	"net"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func deletedPortSourceGuards(d *candidate.InternalPortDeletion) []map[string]any {
	ops := []map[string]any{}
	for _, b := range d.Source.Bindings() {
		ops = append(ops, waitRows(b.Table, []any{uuidCondition(b.OVSUUID)}, []string{"_uuid"}, []any{}))
	}
	return append(ops, waitRows("Bridge", []any{[]any{"ports", "includes", uuidSet(d.Source.Port.OVSUUID)}}, []string{"_uuid"}, []any{}))
}

func verifyPortDeletionBefore(ctx context.Context, conn net.Conn, reader *bufio.Reader, d discovered, c candidate.Candidate, view inventory.ExecutionView, marker string) error {
	i, ok := internalPortIntent(c)
	if !ok || c.Intents[0].PortDeletion == nil {
		return apitypes.Fail(422, "INVALID_INTERNAL_PORT_DELETION")
	}
	guards, err := graphGuards(d, i, marker, i.PortCreation.BeforePresent)
	if err != nil {
		return err
	}
	parents, err := internalParentGuards(d, i, view, i.PortCreation.BeforePresent)
	if err != nil {
		return err
	}
	guards = append(guards, parents...)
	if c.Intents[0].PortDeletion.Restoring {
		guards = append(guards, deletedPortSourceGuards(c.Intents[0].PortDeletion)...)
	}
	return verifyDeletionGuards(ctx, conn, reader, guards, "INTERNAL_PORT_GRAPH_CHANGED")
}
