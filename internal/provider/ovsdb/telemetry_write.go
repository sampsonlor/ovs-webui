package ovsdb

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// This checkpoint stays in the execution journal. The candidate contains a
// digest of the native image and exposes only the supported editor fields.
type telemetryNativePlan struct {
	Protocol     string         `json:"protocol"`
	Bridge       string         `json:"bridge"`
	BeforeUUID   string         `json:"before_uuid,omitempty"`
	AfterUUID    string         `json:"after_uuid,omitempty"`
	RestoreUUID  string         `json:"restore_uuid,omitempty"`
	RetiredUUID  string         `json:"retired_uuid,omitempty"`
	Before       map[string]any `json:"before,omitempty"`
	After        map[string]any `json:"after,omitempty"`
	BridgeBefore map[string]any `json:"bridge_before"`
	BridgeAfter  map[string]any `json:"bridge_after"`
	RootConfig   map[string]any `json:"root_config"`
	Rollback     bool           `json:"rollback,omitempty"`
}

func telemetryIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) != 1 {
		return candidate.StoredIntent{}, false
	}
	i := c.Intents[0]
	return i, candidate.IsTelemetryOperation(i.Operation) && i.Telemetry != nil
}

func telemetryDefaults(d discovered, table string) map[string]any {
	row := map[string]any{}
	for name, c := range d.native.Tables[table].Columns {
		switch c.Type {
		case native.TypeMap:
			row[name] = map[string]any{}
		case native.TypeSet:
			row[name] = []any{}
		case native.TypeInteger:
			row[name] = "0"
		case native.TypeBoolean:
			row[name] = false
		default:
			return nil
		}
	}
	return row
}

func telemetryAfter(d discovered, i candidate.StoredIntent, table string, before map[string]any) (map[string]any, error) {
	after := i.Telemetry.After
	if after == nil {
		if i.Operation != candidate.TelemetryClear || before == nil {
			return nil, errors.New("TELEMETRY_NATIVE_INTENT_INVALID")
		}
		return nil, nil
	}
	targets, ok := candidate.ValidTelemetryTargets(after.Targets)
	if i.Operation != candidate.TelemetrySet || !ok || len(targets) == 0 || !apitypes.UUID(after.UUID) {
		return nil, errors.New("TELEMETRY_NATIVE_INTENT_INVALID")
	}
	row := maps.Clone(before)
	if row == nil {
		row = telemetryDefaults(d, table)
	}
	if row == nil {
		return nil, errors.New("TELEMETRY_NATIVE_SCHEMA_UNSUPPORTED")
	}
	addresses := make([]any, len(targets))
	for index, endpoint := range targets {
		addresses[index] = endpoint
	}
	row["targets"] = addresses
	settingCount := 0
	for _, spec := range candidate.TelemetrySettings(i.Telemetry.Protocol) {
		value, present := after.Settings[spec.Key]
		if !present {
			continue
		}
		settingCount++
		switch {
		case value == nil && spec.Optional:
			row[spec.Key] = []any{}
		case value == nil || *value < spec.Min || *value > spec.Max:
			return nil, errors.New("TELEMETRY_NATIVE_INTENT_INVALID")
		case spec.Optional:
			row[spec.Key] = []any{strconv.FormatInt(*value, 10)}
		default:
			row[spec.Key] = strconv.FormatInt(*value, 10)
		}
	}
	if settingCount != len(after.Settings) {
		return nil, errors.New("TELEMETRY_NATIVE_INTENT_INVALID")
	}
	return row, nil
}

func telemetryBridgeAfter(before map[string]any, protocol, uuid, marker string) (map[string]any, error) {
	labels, ok := before["external_ids"].(map[string]any)
	if !ok || len(marker) != 64 || len(labels) >= 128 && labels[candidate.TelemetryMarker] == nil {
		return nil, errors.New("TELEMETRY_BRIDGE_IMAGE_INVALID")
	}
	after := maps.Clone(before)
	after["external_ids"] = maps.Clone(labels)
	after["external_ids"].(map[string]any)[candidate.TelemetryMarker] = marker
	refs := []any{}
	if uuid != "" {
		refs = append(refs, uuid)
	}
	after[protocol] = refs
	return after, nil
}

func compileTelemetryExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	i, ok := telemetryIntent(envelope.Candidate)
	if !ok || i.Object.Table != "Bridge" || !d.public.TelemetryWrite[i.Telemetry.Protocol] {
		return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_NATIVE_SCHEMA_UNSUPPORTED")
	}
	change := i.Telemetry
	table := telemetryTables[change.Protocol]
	state := &telemetryNativePlan{Protocol: change.Protocol, Bridge: i.Object.OVSUUID, RestoreUUID: change.RestoreUUID, RootConfig: telemetryRootConfig(view)}
	state.BridgeBefore = view.Observation.Rows["Bridge"][state.Bridge].Configuration
	if change.Before != nil {
		state.BeforeUUID = change.Before.UUID
		state.Before = view.Observation.Rows[table][state.BeforeUUID].Configuration
		if !apitypes.UUID(state.BeforeUUID) || state.Before == nil || change.Before.Digest != inventory.Digest(state.Before) {
			return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_NATIVE_CONFLICT")
		}
	}
	if change.After != nil {
		state.AfterUUID = change.After.UUID
		if state.BeforeUUID != "" && state.AfterUUID != state.BeforeUUID {
			return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_NATIVE_CONFLICT")
		}
	} else if !apitypes.UUID(state.RestoreUUID) || state.RestoreUUID == state.BeforeUUID || view.Observation.Rows[table][state.RestoreUUID].UUID != "" {
		return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_NATIVE_CONFLICT")
	}
	var err error
	state.After, err = telemetryAfter(d, i, table, state.Before)
	if err != nil {
		return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_NATIVE_CONFLICT")
	}
	return buildTelemetryExecution(id, marker, envelope, view, d, state)
}

func buildTelemetryExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered, state *telemetryNativePlan) (execution.Plan, error) {
	conflict := apitypes.Fail(409, "TELEMETRY_NATIVE_CONFLICT")
	if state.Rollback {
		conflict = apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	if !d.public.TelemetryWrite[state.Protocol] || !telemetryImageMatches(view, state, false) {
		return execution.Plan{}, conflict
	}
	var err error
	state.BridgeAfter, err = telemetryBridgeAfter(state.BridgeBefore, state.Protocol, state.AfterUUID, marker)
	if err != nil {
		return execution.Plan{}, conflict
	}
	if !telemetryCapacity(view, state) {
		return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_CHECKPOINT_CAPACITY")
	}
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	limit := int64(math.MaxInt64)
	if !state.Rollback {
		limit-- // Reserve one more acknowledgement counter for compensation.
	}
	if e1 != nil || e2 != nil || cur > next || next >= limit {
		return execution.Plan{}, apitypes.Fail(409, "TELEMETRY_COUNTER_UNAVAILABLE")
	}
	ops, err := telemetryGuards(d, root.UUID, state, false)
	if err != nil {
		return execution.Plan{}, conflict
	}
	// Pin the counter at the checked baseline. Every previous wait and every
	// write shares this transaction. This also fixes the only possible target
	// counter for the execution marker if the transaction reply is lost.
	where := []any{uuidCondition(root.UUID), []any{"next_cfg", "==", next}, []any{"cur_cfg", ">=", cur}, []any{"cur_cfg", "<=", next}}
	ops = append(ops, waitRows("Open_vSwitch", where, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(root.UUID)}}))
	n := nativePlan{Operations: ops, Evidence: view.Observation.Evidence, Telemetry: state}
	table := telemetryTables[state.Protocol]
	if state.AfterUUID != "" {
		row, err := telemetryWireRow(d, table, state.After)
		if err != nil {
			return execution.Plan{}, conflict
		}
		if state.BeforeUUID == "" {
			n.InsertUUIDs = map[int]string{len(n.Operations): state.AfterUUID}
			n.Operations = append(n.Operations, map[string]any{"op": "insert", "table": table, "row": row, "uuid": state.AfterUUID})
		} else {
			for name := range row {
				if inventory.Digest(state.Before[name]) == inventory.Digest(state.After[name]) {
					delete(row, name)
				}
			}
			if len(row) == 0 {
				return execution.Plan{}, conflict
			}
			n.CountIndexes = append(n.CountIndexes, len(n.Operations))
			n.Operations = append(n.Operations, map[string]any{"op": "update", "table": table, "where": []any{uuidCondition(state.AfterUUID)}, "row": row})
		}
	}
	// OVSDB collects the detached non-root exporter after this transaction.
	// No explicit delete can affect a different or multiply referenced row.
	bridge := map[string]any{}
	for _, name := range []string{state.Protocol, "external_ids"} {
		bridge[name], err = nativeValue(state.BridgeAfter[name], d.native.Tables["Bridge"].Columns[name])
		if err != nil {
			return execution.Plan{}, conflict
		}
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations), len(n.Operations)+1)
	n.Operations = append(n.Operations,
		map[string]any{"op": "update", "table": "Bridge", "where": []any{uuidCondition(state.Bridge)}, "row": bridge},
		map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}},
	)
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations,
		map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}},
		map[string]any{"op": "commit", "durable": true},
	)
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return execution.Plan{}, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data}, nil
}

func telemetryCapacity(view inventory.ExecutionView, state *telemetryNativePlan) bool {
	rows := view.Observation.Rows
	bridge := rows["Bridge"][state.Bridge]
	bridge.Configuration = state.BridgeAfter
	bridge.Values = maps.Clone(bridge.Values)
	bridge.Values["external_ids"] = sanitize("Bridge", "external_ids", state.BridgeAfter["external_ids"])
	bridge.Values[state.Protocol] = state.BridgeAfter[state.Protocol]
	body, err := json.Marshal(bridge)
	if err != nil || len(body) > inventory.MaxRowBytes {
		return false
	}
	// Preserve enough monitor capacity to retain either image after a clear
	// or update. A successful transaction must keep rollback observable.
	count := 0
	for _, table := range rows {
		count += len(table)
	}
	if state.BeforeUUID == "" && state.AfterUUID != "" && count >= inventory.MaxRows {
		return false
	}
	encoded, err := json.Marshal(view.Observation)
	if err != nil {
		return false
	}
	previousBridge, _ := json.Marshal(rows["Bridge"][state.Bridge])
	growth := max(0, len(body)-len(previousBridge))
	if state.AfterUUID != "" {
		table := telemetryTables[state.Protocol]
		before, _ := json.Marshal(rows[table][state.BeforeUUID])
		after := inventory.Row{UUID: state.AfterUUID, Configuration: state.After, Values: map[string]any{}}
		for name, value := range state.After {
			if name != "external_ids" {
				after.Values[name] = sanitize(table, name, value)
			}
		}
		body, err = json.Marshal(after)
		if err != nil || len(body) > inventory.MaxRowBytes {
			return false
		}
		if state.BeforeUUID != "" {
			growth += max(0, len(body)-len(before))
		} else {
			growth += len(body) + 128 // Table/key delimiters and row UUID.
		}
	}
	return len(encoded)+growth <= inventory.MaxSnapshotBytes
}

func telemetryWireRow(d discovered, table string, image map[string]any) (map[string]any, error) {
	columns := d.native.Tables[table].Columns
	if len(image) != len(columns) || len(columns) == 0 {
		return nil, errors.New("TELEMETRY_EXPORTER_IMAGE_INVALID")
	}
	row := map[string]any{}
	for name, col := range columns {
		if image[name] == nil {
			return nil, errors.New("TELEMETRY_EXPORTER_IMAGE_INVALID")
		}
		value, err := nativeValue(image[name], col)
		if err != nil {
			return nil, err
		}
		row[name] = value
	}
	return row, nil
}
