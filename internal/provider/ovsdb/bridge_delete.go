package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func deletedSourceGuards(d *candidate.BridgeDeletion) []map[string]any {
	ops := []map[string]any{}
	for _, b := range d.Source.Bindings() {
		ops = append(ops, waitRows(b.Table, []any{uuidCondition(b.OVSUUID)}, []string{"_uuid"}, []any{}))
	}
	return append(ops, waitRows("Open_vSwitch", []any{uuidCondition(d.Source.Root),
		[]any{"bridges", "includes", uuidSet(d.Source.Bridge.OVSUUID)}}, []string{"_uuid"}, []any{}))
}

// Before admission, reject unsupported native configuration or dependencies
// outside the monitor. These read-only waits are repeated atomically with the
// actual write, so a writer racing this preflight cannot bypass them.
func verifyDeletionBefore(ctx context.Context, conn net.Conn, reader *bufio.Reader, d discovered, c candidate.Candidate, marker string) error {
	i, ok := bridgeIntent(c)
	if !ok || c.Intents[0].Deletion == nil {
		return apitypes.Fail(422, "INVALID_ISOLATED_BRIDGE_DELETION")
	}
	guards, err := graphGuards(d, i, marker, i.Creation.BeforePresent)
	if err != nil {
		return err
	}
	if c.Intents[0].Deletion.Restoring {
		guards = append(guards, deletedSourceGuards(c.Intents[0].Deletion)...)
	}
	return verifyDeletionGuards(ctx, conn, reader, guards, "ISOLATED_BRIDGE_GRAPH_CHANGED")
}

func verifyDeletionGuards(ctx context.Context, conn net.Conn, reader *bufio.Reader, guards []map[string]any, conflict string) error {
	params := []any{"Open_vSwitch"}
	for _, guard := range guards {
		if guard["op"] != "wait" {
			return apitypes.Fail(503, "INVALID_DELETION_PROOF")
		}
		params = append(params, guard)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	// The discovery call helper deliberately denies transact. Keep that boundary:
	// this private path sends only the locally compiled, wait-only proof above.
	if err := send(conn, map[string]any{"method": "transact", "params": params, "id": 4}); err != nil {
		return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
	}
	for count := 0; count < 32; count++ {
		m, err := readMessage(reader)
		if err != nil {
			return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
		}
		if m.Method == "echo" {
			if err = replyEcho(conn, m); err != nil {
				return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
			}
			continue
		}
		if m.Method != "" || string(m.ID) != "4" || len(m.Error) != 0 && string(m.Error) != "null" {
			return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
		}
		var results []map[string]json.RawMessage
		if json.Unmarshal(m.Result, &results) != nil || len(results) != len(guards) {
			return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
		}
		for _, result := range results {
			if result == nil || len(result["error"]) != 0 {
				return apitypes.Fail(409, conflict)
			}
		}
		return ctx.Err()
	}
	return apitypes.Fail(503, "BRIDGE_PREFLIGHT_UNAVAILABLE")
}
