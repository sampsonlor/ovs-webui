package ovsdb

import (
	"encoding/json"
	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

func multicastMapConstraint(c *native.ColumnSchema, raw any) bool {
	if c == nil || !bondConstraint("other_config", c) {
		return false
	}
	// Inspect the original wire type as well: the typed schema codec does not
	// preserve every string constraint (notably minLength). No new native map
	// writer may qualify a constrained or unknown type through that lossy view.
	value, ok := raw.(map[string]any)
	if !ok || len(value) != 4 || value["min"] != json.Number("0") || value["max"] != "unlimited" {
		return false
	}
	free := func(spec any) bool {
		if spec == "string" {
			return true
		}
		base, ok := spec.(map[string]any)
		return ok && len(base) == 1 && base["type"] == "string"
	}
	return free(value["key"]) && free(value["value"])
}

func multicastRawColumnType(raw map[string]any, table, name string) any {
	tables, _ := raw["tables"].(map[string]any)
	definition, _ := tables[table].(map[string]any)
	columns, _ := definition["columns"].(map[string]any)
	column, _ := columns[name].(map[string]any)
	return column["type"]
}

func compileMulticastExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	if len(envelope.Candidate.Intents) != 1 || envelope.Candidate.Intents[0].Operation != candidate.BridgeMulticastSet || envelope.Candidate.Intents[0].Multicast == nil {
		return out, apitypes.Fail(422, "INVALID_MULTICAST_REQUEST")
	}
	i := envelope.Candidate.Intents[0]
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return out, apitypes.Fail(409, "MULTICAST_EXECUTION_CONFLICT")
	}
	if !isolationConstraint(d.native.Tables["Bridge"].Columns["mcast_snooping_enable"]) || !d.multicastMaps["other_config"] {
		return out, apitypes.Fail(409, "MULTICAST_SCHEMA_UNSUPPORTED")
	}
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	if e1 != nil || e2 != nil || next == math.MaxInt64 || next != cur {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != native.TypeInteger || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
		}
	}
	bridge := view.Observation.Rows["Bridge"][i.Object.OVSUUID]
	labels, ok := bridge.Configuration["external_ids"].(map[string]any)
	if !ok || !d.multicastMaps["external_ids"] {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_UNSUPPORTED")
	}
	if len(labels) >= 128 && labels[candidate.MulticastMarker] == nil {
		return out, apitypes.Fail(429, "COMMIT_EVIDENCE_CAPACITY")
	}
	// Exact whole-table persistent images protect incoming references and
	// unrelated keys in the same transaction as this single native field.
	ops, err := treeFenceOperations(view, d)
	if err != nil {
		return out, err
	}
	n := nativePlan{Operations: ops[:4], Evidence: view.Observation.Evidence}
	where := []any{uuidCondition(i.Object.OVSUUID)}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	// Values is a deliberately filtered public projection. Only the complete
	// private persistent image can preserve unknown and unrelated native keys.
	other, ok := bridge.Configuration["other_config"].(map[string]any)
	if !ok {
		return out, apitypes.Fail(409, "MULTICAST_CONFIGURATION_UNKNOWN")
	}
	merged := candidate.CloneConfiguration(other)
	for _, key := range multicast.Keys {
		if value := i.Multicast.After.Parameters[key]; value == nil {
			delete(merged, key)
		} else {
			merged[key] = *value
		}
	}
	encoded, err := nativeValue(merged, d.native.Tables["Bridge"].Columns["other_config"])
	if err != nil {
		return out, err
	}
	n.Operations = append(n.Operations, map[string]any{"op": "update", "table": "Bridge", "where": where, "row": map[string]any{"mcast_snooping_enable": i.Multicast.After.Enabled, "other_config": encoded}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Bridge", "where": where, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{candidate.MulticastMarker}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{candidate.MulticastMarker, marker}}}}}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	bindings := append([]candidate.Binding{}, view.Candidate.Multicasts[i.Object.ManagementID].Bindings...)
	slices.SortFunc(bindings, func(a, b candidate.Binding) int { return strings.Compare(a.ManagementID, b.ManagementID) })
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data, GraphBindings: bindings}, nil
}
