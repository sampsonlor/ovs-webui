package ovsdb

import (
	"encoding/json"
	"math"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func mtuConstraint(c *native.ColumnSchema) bool {
	if c == nil || !c.Mutable() || c.Ephemeral() || c.TypeObj == nil {
		return false
	}
	t := c.TypeObj
	if t.Key == nil || t.Key.Type != native.TypeInteger || t.Value != nil || t.Min() != 0 || t.Max() != 1 || len(t.Key.Enum) != 0 {
		return false
	}
	min, e1 := t.Key.MinInteger()
	max, e2 := t.Key.MaxInteger()
	return e1 == nil && e2 == nil && min <= 576 && max >= 65535
}
func compileMTUExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	if len(envelope.Candidate.Intents) != 1 {
		return out, apitypes.Fail(422, "MTU_SINGLE_INTENT_REQUIRED")
	}
	i := envelope.Candidate.Intents[0]
	if !candidate.IsMTUOperation(i.Operation) || i.MTU == nil || i.MTU.Before != nil && !candidate.ValidMTU(*i.MTU.Before) || !candidate.ValidMTU(candidate.ExpectedMTU(i.MTU)) || (i.MTU.Before == nil || i.MTU.After == nil) && i.MTU.Default == nil || !mtuConstraint(d.native.Tables["Interface"].Columns["mtu_request"]) {
		return out, apitypes.Fail(409, "MTU_SCHEMA_UNSUPPORTED")
	}
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	port := view.Observation.Rows["Port"][i.MTU.Port.OVSUUID]
	bridge := view.Observation.Rows["Bridge"][i.MTU.Bridge.OVSUUID]
	iface := view.Observation.Rows["Interface"][i.Object.OVSUUID]
	if iface.InterfaceOptionsEmpty == nil || !*iface.InterfaceOptionsEmpty {
		return out, apitypes.Fail(409, "MTU_OPTIONS_UNPROVEN")
	}
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	if e1 != nil || e2 != nil || next == math.MaxInt64 || cur > next {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != native.TypeInteger || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
		}
	}
	meta := d.native.Tables["Interface"].Columns["external_ids"]
	labels, ok := iface.Values["external_ids"].(map[string]any)
	if meta == nil || !bondConstraint("other_config", meta) || !ok {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_UNSUPPORTED")
	}
	if len(labels) >= 128 && labels[execution.MarkerKey] == nil {
		return out, apitypes.Fail(429, "COMMIT_EVIDENCE_CAPACITY")
	}
	n := nativePlan{Operations: []map[string]any{}, CountIndexes: []int{}, Evidence: view.Observation.Evidence}
	for _, g := range []struct {
		table   string
		row     inventory.Row
		columns []string
		where   []any
	}{
		{"Open_vSwitch", root, []string{"external_ids"}, []any{uuidCondition(root.UUID), []any{"bridges", "includes", uuidSet(bridge.UUID)}, []any{"next_cfg", "<", json.Number(strconv.FormatInt(math.MaxInt64, 10))}, []any{"next_cfg", ">=", next}, []any{"cur_cfg", ">=", cur}}},
		{"Bridge", bridge, []string{"name", "datapath_type", "ports", "external_ids"}, []any{uuidCondition(bridge.UUID)}},
		{"Port", port, []string{"name", "interfaces", "external_ids"}, []any{uuidCondition(port.UUID)}},
		{"Interface", iface, []string{"name", "type", "options", "mtu_request", "external_ids"}, []any{uuidCondition(iface.UUID)}},
	} {
		op, err := guard(d, g.table, g.row, g.columns, g.where)
		if err != nil {
			return out, err
		}
		n.Operations = append(n.Operations, op)
	}
	// These set-wide guards detect a late second parent as well as detachment.
	n.Operations = append(n.Operations, waitRows("Port", []any{[]any{"interfaces", "includes", uuidSet(iface.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(port.UUID)}}), waitRows("Bridge", []any{[]any{"ports", "includes", uuidSet(port.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(bridge.UUID)}}))
	if context := i.MTU.Default; context != nil {
		current := view.Candidate.Interfaces[i.Object.ManagementID].Default
		if current == nil || candidate.Digest(context) != candidate.Digest(current) || len(context.Bindings) == 0 || len(context.Bindings) > 64 {
			return out, apitypes.Fail(409, "MTU_DEFAULT_DEPENDENCY_CHANGED")
		}
		for _, b := range context.Bindings {
			row, present := view.Observation.Rows[b.Table][b.OVSUUID]
			if !present || b.Generation != view.Candidate.Generation || !apitypes.ManagementID(b.ManagementID) || (b.Table != "Port" && b.Table != "Interface") {
				return out, apitypes.Fail(409, "MTU_DEFAULT_DEPENDENCY_CHANGED")
			}
			columns := []string{"name", "interfaces", "external_ids"}
			if b.Table == "Interface" {
				if row.InterfaceOptionsEmpty == nil || !*row.InterfaceOptionsEmpty {
					return out, apitypes.Fail(409, "MTU_OPTIONS_UNPROVEN")
				}
				columns = []string{"name", "type", "options", "mtu_request", "ofport", "error", "external_ids"}
				request, known := row.Values["mtu_request"].([]any)
				if !known {
					return out, apitypes.Fail(409, "MTU_DEFAULT_DEPENDENCY_CHANGED")
				}
				if row.Values["type"] != "internal" || len(request) != 0 {
					columns = append(columns, "mtu")
				}
			}
			g, err := guard(d, b.Table, row, columns, []any{uuidCondition(row.UUID)})
			if err != nil {
				return out, err
			}
			n.Operations = append(n.Operations, g)
			if b.Table == "Port" {
				n.Operations = append(n.Operations, waitRows("Bridge", []any{[]any{"ports", "includes", uuidSet(row.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(bridge.UUID)}}))
			} else {
				parentID := ""
				for _, parentBinding := range context.Bindings {
					if parentBinding.Table == "Port" {
						for _, id := range nativeRefs(view.Observation.Rows["Port"][parentBinding.OVSUUID].Values["interfaces"]) {
							if id == row.UUID {
								parentID = parentBinding.OVSUUID
							}
						}
					}
				}
				if parentID == "" {
					return out, apitypes.Fail(409, "MTU_DEFAULT_DEPENDENCY_CHANGED")
				}
				n.Operations = append(n.Operations, waitRows("Port", []any{[]any{"interfaces", "includes", uuidSet(row.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(parentID)}}))
			}
		}
	}
	after := []any{}
	if i.MTU.After != nil {
		after = append(after, *i.MTU.After)
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "update", "table": "Interface", "where": []any{uuidCondition(iface.UUID)}, "row": map[string]any{"mtu_request": []any{"set", after}}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Interface", "where": []any{uuidCondition(iface.UUID)}, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{execution.MarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{execution.MarkerKey, marker}}}}}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	b, err := json.Marshal(n)
	if err != nil || len(b) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: b}, nil
}
