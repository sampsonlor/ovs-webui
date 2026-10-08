package ovsdb

import (
	"encoding/json"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"math"
	"slices"
	"strconv"
	"time"
)

func topologyIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) == 1 && candidate.IsTopologyOperation(c.Intents[0].Operation) && c.Intents[0].Topology != nil {
		return c.Intents[0], true
	}
	return candidate.StoredIntent{}, false
}
func topologyImages(d discovered, nodes []candidate.TopologyNode, images map[string]map[string]any) ([]map[string]any, error) {
	ops := []map[string]any{}
	for _, n := range nodes {
		cfg := images[n.Binding.OVSUUID]
		if candidate.ConfigurationDigest(cfg) != n.Digest {
			return nil, errors.New("TOPOLOGY_CONFIGURATION_CHANGED")
		}
		columns := []string{}
		values := map[string]any{"_uuid": uuidValue(n.Binding.OVSUUID)}
		for name, value := range cfg {
			if !graphConfigurationColumn(n.Binding.Table, name, d.native.Tables[n.Binding.Table].Columns[name]) {
				return nil, errors.New("TOPOLOGY_SCHEMA_CHANGED")
			}
			v, err := nativeValue(value, d.native.Tables[n.Binding.Table].Columns[name])
			if err != nil {
				return nil, err
			}
			values[name] = v
			columns = append(columns, name)
		}
		columns = append(columns, "_uuid")
		slices.Sort(columns)
		ops = append(ops, waitRows(n.Binding.Table, []any{uuidCondition(n.Binding.OVSUUID), []any{"name", "==", n.Name}}, columns, []any{values}))
	}
	return ops, nil
}

// Inspect every incoming reference declared by the discovered schema, including
// unmonitored domains and weak references. Foreign dependencies cannot be
// silently severed, and the checks are atomic with strong-reference changes.
func topologyReferences(d discovered, nodes []candidate.TopologyNode, all map[string]candidate.TopologyNode) []map[string]any {
	ops := []map[string]any{}
	for _, n := range nodes {
		for table, t := range d.native.Tables {
			for name, c := range t.Columns {
				if c.TypeObj.Key.Type != "uuid" {
					continue
				}
				target, _ := c.TypeObj.Key.RefTable()
				if target != n.Binding.Table {
					continue
				}
				want := []any{}
				for _, p := range all {
					if p.Binding.Table == table && slices.Contains(p.Links, n.Binding) && (table == "Bridge" && name == "ports" || table == "Port" && name == "interfaces") {
						want = append(want, map[string]any{"_uuid": uuidValue(p.Binding.OVSUUID)})
					}
				}
				if table == "Open_vSwitch" && name == "bridges" {
					continue
				} // guarded by the root row and explicit inclusion below
				value := any(uuidSet(n.Binding.OVSUUID))
				if c.Type == "uuid" {
					value = uuidValue(n.Binding.OVSUUID)
				}
				ops = append(ops, waitRows(table, []any{[]any{name, "includes", value}}, []string{"_uuid"}, want))
			}
		}
	}
	return ops
}
func compileTopologyExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered, checkpoint map[string]map[string]any) (execution.Plan, error) {
	out := execution.Plan{}
	i, ok := topologyIntent(envelope.Candidate)
	if !ok || !bridgeSchemaSupported(d) {
		return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
	}
	g := i.Topology
	before, after := g.Before, g.After
	if g.Compensating {
		before, after = g.After, g.Restored
	}
	images := map[string]map[string]any{}
	for _, n := range before {
		images[n.Binding.OVSUUID] = candidate.CloneConfiguration(view.Observation.Rows[n.Binding.Table][n.Binding.OVSUUID].Configuration)
	}
	guards, err := topologyImages(d, before, images)
	if err != nil {
		return out, err
	}
	desired, err := candidate.TopologyConfigurations(i, view.Candidate.Topology, checkpoint)
	if err != nil {
		return out, err
	}
	root := view.Observation.Rows["Open_vSwitch"][g.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	if e1 != nil || e2 != nil || next == math.MaxInt64 || cur > next {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	if !candidate.Passed(topologyCounterChecks(d)) {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
	}
	cols := []string{}
	rootRow := root
	rootRow.Values = candidate.CloneConfiguration(root.Configuration)
	for col := range rootRow.Values {
		cols = append(cols, col)
	}
	slices.Sort(cols)
	rg, err := guard(d, "Open_vSwitch", rootRow, cols, []any{uuidCondition(root.UUID), []any{"next_cfg", ">=", next}, []any{"next_cfg", "<", json.Number(strconv.FormatInt(math.MaxInt64, 10))}, []any{"cur_cfg", ">=", cur}})
	if err != nil {
		return out, err
	}
	n := nativePlan{Operations: append([]map[string]any{rg}, guards...), Evidence: view.Observation.Evidence, CreationMarker: marker, InsertUUIDs: map[int]string{}, TopologyBefore: images, TopologyAfter: desired}
	n.Operations = append(n.Operations, topologyReferences(d, before, view.Candidate.Topology.Nodes)...)
	for id := range g.Allocations {
		binding := view.Candidate.Topology.Nodes[id].Binding
		row := view.Observation.Rows["Interface"][binding.OVSUUID]
		allocationGuard, err := guard(d, "Interface", row, []string{"ofport"}, []any{uuidCondition(row.UUID)})
		if err != nil {
			return out, err
		}
		n.Operations = append(n.Operations, allocationGuard)
	}
	for _, p := range before {
		if p.Binding.Table == "Bridge" {
			n.Operations = append(n.Operations, waitRows("Open_vSwitch", []any{[]any{"bridges", "includes", uuidSet(p.Binding.OVSUUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(g.Root)}}))
		}
	}
	for _, p := range after {
		exists := slices.ContainsFunc(before, func(b candidate.TopologyNode) bool { return b.Binding == p.Binding })
		cfg := candidate.CloneConfiguration(desired[p.Binding.OVSUUID])
		row := map[string]any{}
		for name, value := range cfg {
			if exists && candidate.Digest(value) == candidate.Digest(images[p.Binding.OVSUUID][name]) {
				continue
			}
			v, err := nativeValue(value, d.native.Tables[p.Binding.Table].Columns[name])
			if err != nil {
				return out, err
			}
			row[name] = v
		}
		if !exists {
			n.Operations = append(n.Operations, waitRows(p.Binding.Table, []any{uuidCondition(p.Binding.OVSUUID)}, []string{"_uuid"}, []any{}), waitRows(p.Binding.Table, []any{[]any{"name", "==", p.Name}}, []string{"_uuid"}, []any{}))
			ids, _ := cfg["external_ids"].(map[string]any)
			if ids == nil {
				ids = map[string]any{}
			}
			ids[candidate.BridgeCreationMarker] = marker
			cfg["external_ids"] = ids
			desired[p.Binding.OVSUUID] = cfg
			if len(ids) > 128 {
				return out, apitypes.Fail(409, "COMMIT_EVIDENCE_CAPACITY")
			}
			row["external_ids"], err = nativeValue(ids, d.native.Tables[p.Binding.Table].Columns["external_ids"])
			if err != nil {
				return out, err
			}
			n.InsertUUIDs[len(n.Operations)] = p.Binding.OVSUUID
			n.Operations = append(n.Operations, map[string]any{"op": "insert", "table": p.Binding.Table, "uuid": p.Binding.OVSUUID, "row": row})
		} else if len(row) != 0 {
			delete(row, "name") // immutable identity, even when the runtime schema permits mutation
			n.CountIndexes = append(n.CountIndexes, len(n.Operations))
			n.Operations = append(n.Operations, map[string]any{"op": "update", "table": p.Binding.Table, "where": []any{uuidCondition(p.Binding.OVSUUID)}, "row": row})
		}
	}
	for _, p := range before {
		if p.Binding.Table == "Bridge" && !slices.ContainsFunc(after, func(a candidate.TopologyNode) bool { return a.Binding == p.Binding }) {
			n.CountIndexes = append(n.CountIndexes, len(n.Operations))
			n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"bridges", "delete", uuidSet(p.Binding.OVSUUID)}}})
		}
	}
	for _, p := range after {
		if p.Binding.Table == "Bridge" && !slices.ContainsFunc(before, func(a candidate.TopologyNode) bool { return a.Binding == p.Binding }) {
			n.CountIndexes = append(n.CountIndexes, len(n.Operations))
			n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"bridges", "insert", uuidSet(p.Binding.OVSUUID)}}})
		}
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{bridgeMarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{bridgeMarkerKey, marker}}}}, []any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: g.Root, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data}, nil
}
func topologyCounterChecks(d discovered) []candidate.Gate {
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != "integer" || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return []candidate.Gate{{State: "blocked"}}
		}
	}
	return nil
}
func topologyProof(p execution.Plan, view inventory.ExecutionView, d discovered) ([]any, error) {
	i, ok := topologyIntent(p.Envelope.Candidate)
	if !ok {
		return nil, errors.New("INVALID_TOPOLOGY_PLAN")
	}
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil {
		return nil, errors.New("INVALID_TOPOLOGY_PLAN")
	}
	g := i.Topology
	after, before := g.After, g.Before
	if g.Compensating {
		after, before = g.Restored, g.After
	}
	guards, err := topologyImages(d, after, n.TopologyAfter)
	if err != nil {
		return nil, err
	}
	ops := []any{}
	for _, op := range guards {
		ops = append(ops, op)
	}
	for _, op := range topologyReferences(d, after, view.Candidate.Topology.Nodes) {
		ops = append(ops, op)
	}
	for _, old := range before {
		if !slices.ContainsFunc(after, func(a candidate.TopologyNode) bool { return a.Binding == old.Binding }) {
			ops = append(ops, waitRows(old.Binding.Table, []any{uuidCondition(old.Binding.OVSUUID)}, []string{"_uuid"}, []any{}))
		}
	}
	root := view.Observation.Rows["Open_vSwitch"][g.Root]
	ids, _ := root.Values["external_ids"].(map[string]any)
	if ids[bridgeMarkerKey] != p.Marker {
		return nil, errors.New("TOPOLOGY_COMMIT_MARKER_CHANGED")
	}
	root.Values = candidate.CloneConfiguration(root.Configuration)
	cols := []string{}
	for column := range root.Values {
		cols = append(cols, column)
	}
	slices.Sort(cols)
	rg, err := guard(d, "Open_vSwitch", root, cols, []any{uuidCondition(g.Root)})
	if err != nil {
		return nil, err
	}
	ops = append(ops, rg)
	for _, a := range after {
		if a.Binding.Table == "Bridge" {
			ops = append(ops, waitRows("Open_vSwitch", []any{[]any{"bridges", "includes", uuidSet(a.Binding.OVSUUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(g.Root)}}))
		}
		if a.Binding.Table != "Interface" {
			continue
		}
		row := view.Observation.Rows["Interface"][a.Binding.OVSUUID]
		local := false
		for _, port := range after {
			if port.Binding.Table != "Port" || port.Name != a.Name || !slices.Contains(port.Links, a.Binding) {
				continue
			}
			for _, bridge := range after {
				if bridge.Binding.Table == "Bridge" && bridge.Name == port.Name && slices.Contains(bridge.Links, port.Binding) {
					local = true
				}
			}
		}
		if !internalInterfaceHealthy(row) && !(local && candidate.Digest(row.Values["ofport"]) == candidate.Digest([]any{"65534"}) && candidate.Digest(row.Values["error"]) == candidate.Digest([]any{})) {
			return nil, errors.New("TOPOLOGY_INTERFACE_NOT_APPLIED")
		}
		if (i.Operation == "interface.ofport.set" || i.Operation == "interface.ofport.clear") && a.Binding == i.Object {
			requested, ok := n.TopologyAfter[a.Binding.OVSUUID]["ofport_request"].([]any)
			if !ok {
				return nil, errors.New("OFPORT_REQUEST_UNPROVEN")
			}
			if len(requested) == 1 && candidate.Digest(row.Values["ofport"]) != candidate.Digest(requested) {
				return nil, errors.New("OFPORT_ALLOCATION_UNPROVEN")
			}
		}
		guard, err := guard(d, "Interface", row, []string{"error", "ofport"}, []any{uuidCondition(row.UUID)})
		if err != nil {
			return nil, err
		}
		ops = append(ops, guard)
	}
	return append(ops, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(g.Root)}, "columns": []string{"_uuid", "next_cfg", "cur_cfg"}}), nil
}
