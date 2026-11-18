package ovsdb

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func telemetryRootConfig(view inventory.ExecutionView) map[string]any {
	values := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root].Configuration
	labels, labelsKnown := values["external_ids"].(map[string]any)
	options, optionsKnown := values["other_config"].(map[string]any)
	if !labelsKnown || !optionsKnown {
		return nil
	}
	return map[string]any{"external_ids": labels, "other_config": options}
}

func telemetryImages(state *telemetryNativePlan, after bool) (map[string]any, string, map[string]any, []string) {
	bridge, uuid, row, detached := state.BridgeBefore, state.BeforeUUID, state.Before, state.AfterUUID
	if after {
		bridge, uuid, row, detached = state.BridgeAfter, state.AfterUUID, state.After, state.BeforeUUID
	}
	absent := []string{}
	for _, id := range []string{detached, state.RestoreUUID, state.RetiredUUID} {
		if id != "" && id != uuid && !slices.Contains(absent, id) {
			absent = append(absent, id)
		}
	}
	return bridge, uuid, row, absent
}

func telemetryImageMatches(view inventory.ExecutionView, state *telemetryNativePlan, after bool) bool {
	bridge, uuid, row, absent := telemetryImages(state, after)
	rows := view.Observation.Rows
	bridgeRow := rows["Bridge"][state.Bridge]
	root := rows["Open_vSwitch"][view.Observation.Evidence.Root]
	if !apitypes.UUID(state.Bridge) || bridgeRow.UUID != state.Bridge || !apitypes.UUID(root.UUID) ||
		!slices.Contains(nativeRefs(root.Values["bridges"]), state.Bridge) ||
		state.RootConfig == nil || inventory.Digest(telemetryRootConfig(view)) != inventory.Digest(state.RootConfig) ||
		bridge == nil || inventory.Digest(bridgeRow.Configuration) != inventory.Digest(bridge) {
		return false
	}
	refs, ok := bridge[state.Protocol].([]any)
	if !ok || uuid == "" && len(refs) != 0 || uuid != "" && (len(refs) != 1 || refs[0] != uuid) {
		return false
	}
	table := telemetryTables[state.Protocol]
	for _, id := range absent {
		if !apitypes.UUID(id) || rows[table][id].UUID != "" {
			return false
		}
	}
	if uuid == "" {
		return row == nil
	}
	if !apitypes.UUID(uuid) || row == nil || inventory.Digest(rows[table][uuid].Configuration) != inventory.Digest(row) {
		return false
	}
	for id, other := range rows["Bridge"] {
		if id != state.Bridge && slices.Contains(nativeRefs(other.Configuration[state.Protocol]), uuid) {
			return false
		}
	}
	if state.Protocol == "ipfix" {
		for _, collector := range rows[flowSampleCollectorTable] {
			refs, ok := collector.Configuration["ipfix"].([]any)
			if !ok || slices.Contains(nativeRefs(refs), uuid) {
				return false
			}
		}
	}
	return true
}

func telemetryGuards(d discovered, root string, state *telemetryNativePlan, after bool) ([]map[string]any, error) {
	bridge, uuid, row, absent := telemetryImages(state, after)
	names := []string{}
	for name, col := range d.native.Tables["Bridge"].Columns {
		if graphConfigurationColumn("Bridge", name, col) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) != len(bridge) {
		return nil, errors.New("TELEMETRY_BRIDGE_IMAGE_INVALID")
	}
	bridgeGuard, err := guard(d, "Bridge", inventory.Row{UUID: state.Bridge, Values: bridge}, names, []any{uuidCondition(state.Bridge)})
	if err != nil {
		return nil, err
	}
	rootWhere := []any{uuidCondition(root), []any{"bridges", "includes", uuidSet(state.Bridge)}}
	rootGuard, err := guard(d, "Open_vSwitch", inventory.Row{UUID: root, Values: state.RootConfig}, []string{"external_ids", "other_config"}, rootWhere)
	if err != nil {
		return nil, err
	}
	ops := []map[string]any{rootGuard, bridgeGuard}
	table := telemetryTables[state.Protocol]
	for _, id := range absent {
		ops = append(ops, waitRows(table, []any{uuidCondition(id)}, []string{"_uuid"}, []any{}))
	}
	if uuid == "" {
		return ops, nil
	}
	nativeRow, err := telemetryWireRow(d, table, row)
	if err != nil {
		return nil, err
	}
	columns := []string{"_uuid"}
	for name := range nativeRow {
		columns = append(columns, name)
	}
	slices.Sort(columns)
	nativeRow["_uuid"] = uuidValue(uuid)
	ops = append(ops,
		waitRows(table, []any{uuidCondition(uuid)}, columns, []any{nativeRow}),
		waitRows("Bridge", []any{[]any{state.Protocol, "includes", uuidSet(uuid)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(state.Bridge)}}),
	)
	if state.Protocol == "ipfix" {
		ops = append(ops, waitRows(flowSampleCollectorTable, []any{[]any{"ipfix", "includes", uuidSet(uuid)}}, []string{"_uuid"}, []any{}))
	}
	return ops, nil
}

func (e *Executor) observeTelemetry(ctx context.Context, p execution.Plan, view inventory.ExecutionView, out execution.Outcome) execution.Outcome {
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil || n.Telemetry == nil {
		out.Applied, out.Reason = "unknown", "telemetry-checkpoint-unavailable"
		return out
	}
	state := n.Telemetry
	if !telemetryImageMatches(view, state, true) {
		out.Applied, out.Reason = "unknown", "telemetry-target-changed"
		return out
	}
	out.Commit = "committed"
	floor, e0 := strconv.ParseInt(p.NextFloor, 10, 64)
	expected := strconv.FormatInt(floor+1, 10)
	if e0 != nil || out.Target != nil && *out.Target != expected {
		out.Applied, out.Reason = "unknown", "applied-target-unavailable"
		return out
	}
	root := view.Observation.Rows["Open_vSwitch"][p.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	curFloor, e3 := strconv.ParseInt(p.CurFloor, 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || next <= floor || cur < curFloor || cur > next {
		out.Applied, out.Reason = "unknown", "applied-counter-discontinuity"
		return out
	}
	current := strconv.FormatInt(cur, 10)
	observed := view.Observation.Evidence.ObservedAt
	out.Target, out.Current, out.Observed = &expected, &current, &observed
	if cur <= floor {
		out.Applied, out.Reason = "pending", "awaiting-ovs-vswitchd"
		return out
	}
	if e.verifyApplied(ctx, p, view, &out) != nil {
		out.Applied, out.Reason = "unknown", "fresh-applied-evidence-unavailable"
		return out
	}
	out.Applied, out.Reason = "applied", "ovs-vswitchd-applied"
	return out
}

func telemetryAppliedProof(p execution.Plan, view inventory.ExecutionView, d discovered) ([]any, error) {
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil || n.Telemetry == nil || !d.public.TelemetryWrite[n.Telemetry.Protocol] || !telemetryImageMatches(view, n.Telemetry, true) {
		return nil, errors.New("TELEMETRY_AFTER_IMAGE_UNAVAILABLE")
	}
	waits, err := telemetryGuards(d, p.Root, n.Telemetry, true)
	if err != nil {
		return nil, err
	}
	ops := make([]any, 0, len(waits)+1)
	for _, wait := range waits {
		ops = append(ops, wait)
	}
	return append(ops, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(p.Root)}, "columns": []string{"_uuid", "next_cfg", "cur_cfg"}}), nil
}

func compileTelemetryRollback(original execution.Plan, marker string, checkpoint *telemetryNativePlan, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	if checkpoint == nil || checkpoint.Rollback || !telemetryImageMatches(view, checkpoint, true) {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	labels, _ := checkpoint.BridgeAfter["external_ids"].(map[string]any)
	if labels[candidate.TelemetryMarker] != original.Marker {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	state := &telemetryNativePlan{
		Protocol: checkpoint.Protocol, Bridge: checkpoint.Bridge, Rollback: true,
		BeforeUUID: checkpoint.AfterUUID, AfterUUID: checkpoint.BeforeUUID,
		Before: checkpoint.After, After: checkpoint.Before, BridgeBefore: checkpoint.BridgeAfter, RootConfig: checkpoint.RootConfig,
	}
	if state.BeforeUUID == "" && state.AfterUUID != "" {
		// The old UUID was garbage collected. Restore the exact complete raw
		// image as a fresh, pre-reserved row and leave the old UUID unoccupied.
		state.RetiredUUID, state.AfterUUID = state.AfterUUID, checkpoint.RestoreUUID
		if !apitypes.UUID(state.AfterUUID) || state.AfterUUID == state.RetiredUUID {
			return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
		}
	}
	return buildTelemetryExecution(original.ID, marker, original.Envelope, view, d, state)
}
