package ovsdb

import (
	"encoding/json"
	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

func isolationConstraint(c *native.ColumnSchema) bool {
	return c != nil && c.Mutable() && !c.Ephemeral() && c.Type == native.TypeBoolean && c.TypeObj != nil && c.TypeObj.Key != nil && c.TypeObj.Key.Type == native.TypeBoolean && c.TypeObj.Value == nil && c.TypeObj.Min() == 1 && c.TypeObj.Max() == 1 && len(c.TypeObj.Key.Enum) == 0
}
func compileIsolationExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	if len(envelope.Candidate.Intents) != 1 || envelope.Candidate.Intents[0].Operation != candidate.PortIsolationSet || envelope.Candidate.Intents[0].Isolation == nil {
		return out, apitypes.Fail(422, "INVALID_PORT_ISOLATION")
	}
	i := envelope.Candidate.Intents[0]
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return out, apitypes.Fail(409, "ISOLATION_EXECUTION_CONFLICT")
	}
	if !isolationConstraint(d.native.Tables["Port"].Columns["protected"]) {
		return out, apitypes.Fail(409, "ISOLATION_SCHEMA_UNSUPPORTED")
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
	port := view.Observation.Rows["Port"][i.Object.OVSUUID]
	labels, ok := port.Values["external_ids"].(map[string]any)
	if !ok || !bondConstraint("other_config", d.native.Tables["Port"].Columns["external_ids"]) {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_UNSUPPORTED")
	}
	if len(labels) >= 128 && labels[execution.MarkerKey] == nil {
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
	n.Operations = append(n.Operations, map[string]any{"op": "update", "table": "Port", "where": where, "row": map[string]any{"protected": i.Isolation.After}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Port", "where": where, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{execution.MarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{execution.MarkerKey, marker}}}}}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	bindings := append([]candidate.Binding{}, view.Candidate.Isolations[i.Object.ManagementID].Bindings...)
	slices.SortFunc(bindings, func(a, b candidate.Binding) int { return strings.Compare(a.ManagementID, b.ManagementID) })
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data, GraphBindings: bindings}, nil
}
