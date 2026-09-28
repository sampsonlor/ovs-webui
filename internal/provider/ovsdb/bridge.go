package ovsdb

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

const bridgeMarkerKey = "ovs-webui.bridge-commit"

func bridgeIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) == 1 {
		return candidate.BridgeGraphIntent(c.Intents[0])
	}
	return candidate.StoredIntent{}, false
}

func bridgeSchemaSupported(d discovered) bool {
	for _, table := range []string{"Open_vSwitch", "Bridge", "Port", "Interface"} {
		t, exists := d.native.Tables[table]
		if !exists {
			return false
		}
		root, err := d.native.IsRoot(table)
		if err != nil || root != (table == "Open_vSwitch") {
			return false
		}
		if table != "Open_vSwitch" && !slices.ContainsFunc(t.Indexes, func(cols []string) bool { return slices.Equal(cols, []string{"name"}) }) {
			return false
		}
		meta := t.Columns["external_ids"]
		if meta == nil || !bondConstraint("other_config", meta) {
			return false
		}
	}
	for _, ref := range [][3]string{{"Open_vSwitch", "bridges", "Bridge"}, {"Bridge", "ports", "Port"}, {"Port", "interfaces", "Interface"}} {
		c := d.native.Tables[ref[0]].Columns[ref[1]]
		if c == nil || c.Type != native.TypeSet || !c.Mutable() || c.Ephemeral() || c.TypeObj.Key.Type != native.TypeUUID {
			return false
		}
		table, _ := c.TypeObj.Key.RefTable()
		strength, _ := c.TypeObj.Key.RefType()
		if table != ref[2] || strength != "strong" {
			return false
		}
	}
	// Every incoming graph reference must be guardable, including weak refs.
	for _, t := range d.native.Tables {
		for _, col := range t.Columns {
			for _, base := range []*native.BaseType{col.TypeObj.Key, col.TypeObj.Value} {
				if base == nil || base.Type != native.TypeUUID {
					continue
				}
				target, _ := base.RefTable()
				if slices.Contains([]string{"Bridge", "Port", "Interface"}, target) && col.Type == native.TypeMap {
					return false
				}
			}
		}
	}
	return true
}

// Some OVS status columns are persistent. Exclude only these documented daemon
// outputs; every other non-ephemeral column is checked before graph deletion.
func bridgeStatusColumn(table, col string) bool {
	return table == "Bridge" && col == "datapath_version" || table == "Interface" && slices.Contains([]string{"ofport", "error", "cfm_flap_count"}, col)
}

func emptyNative(c *native.ColumnSchema) (any, error) {
	if c.TypeObj.Min() == 0 {
		if c.Type == native.TypeMap {
			return []any{"map", []any{}}, nil
		}
		return []any{"set", []any{}}, nil
	}
	switch c.Type {
	case native.TypeString:
		return "", nil
	case native.TypeInteger, native.TypeReal:
		return 0, nil
	case native.TypeBoolean:
		return false, nil
	}
	return nil, errors.New("BRIDGE_LIFECYCLE_SCHEMA_UNSUPPORTED")
}

func creationRows(d discovered, i candidate.StoredIntent, marker string) (map[string]map[string]any, error) {
	if !bridgeSchemaSupported(d) || i.Creation == nil || !candidate.ValidBridgeName(i.Creation.Name) || len(marker) != 64 {
		return nil, errors.New("BRIDGE_LIFECYCLE_SCHEMA_UNSUPPORTED")
	}
	rows := map[string]map[string]any{}
	for _, b := range candidate.CreationBindings(i) {
		row := map[string]any{"name": i.Creation.Name, "external_ids": []any{"map", []any{[]any{candidate.BridgeCreationMarker, marker}}}}
		for name, col := range d.native.Tables[b.Table].Columns {
			if col.Ephemeral() || bridgeStatusColumn(b.Table, name) || name == "name" || name == "external_ids" || name == "interfaces" {
				continue
			}
			value, err := emptyNative(col)
			if err != nil {
				return nil, err
			}
			row[name] = value
		}
		rows[b.Table] = row
	}
	rows["Bridge"]["datapath_type"] = "system"
	rows["Bridge"]["fail_mode"] = []any{"set", []any{"secure"}}
	rows["Bridge"]["ports"] = uuidSet(i.Creation.Port.OVSUUID)
	rows["Port"]["interfaces"] = uuidSet(i.Creation.Interface.OVSUUID)
	rows["Interface"]["type"] = "internal"
	return rows, nil
}

func graphGuards(d discovered, i candidate.StoredIntent, marker string, present bool) ([]map[string]any, error) {
	rows, err := creationRows(d, i, marker)
	if err != nil {
		return nil, err
	}
	ops := []map[string]any{}
	for _, b := range candidate.CreationBindings(i) {
		if !present {
			ops = append(ops, waitRows(b.Table, []any{uuidCondition(b.OVSUUID)}, []string{"_uuid"}, []any{}), waitRows(b.Table, []any{[]any{"name", "==", i.Creation.Name}}, []string{"_uuid"}, []any{}))
			continue
		}
		row := rows[b.Table]
		row["_uuid"] = uuidValue(b.OVSUUID)
		columns := []string{}
		for name := range row {
			columns = append(columns, name)
		}
		slices.Sort(columns)
		ops = append(ops, waitRows(b.Table, []any{[]any{"name", "==", i.Creation.Name}}, columns, []any{row}))
		// Check all possible parents, even tables outside the inventory monitor.
		for table, schema := range d.native.Tables {
			for name, col := range schema.Columns {
				if col.TypeObj.Key.Type != native.TypeUUID {
					continue
				}
				target, _ := col.TypeObj.Key.RefTable()
				if target != b.Table {
					continue
				}
				want := []any{}
				if table == "Open_vSwitch" && name == "bridges" && b.Table == "Bridge" {
					want = append(want, map[string]any{"_uuid": uuidValue(i.Creation.Root)})
				}
				if table == "Bridge" && name == "ports" && b.Table == "Port" {
					want = append(want, map[string]any{"_uuid": uuidValue(i.Object.OVSUUID)})
				}
				if table == "Port" && name == "interfaces" && b.Table == "Interface" {
					want = append(want, map[string]any{"_uuid": uuidValue(i.Creation.Port.OVSUUID)})
				}
				value := any(uuidSet(b.OVSUUID))
				if col.Type == native.TypeUUID {
					value = uuidValue(b.OVSUUID)
				}
				ops = append(ops, waitRows(table, []any{[]any{name, "includes", value}}, []string{"_uuid"}, want))
			}
		}
	}
	if !present {
		ops = append(ops, waitRows("Open_vSwitch", []any{uuidCondition(i.Creation.Root), []any{"bridges", "includes", uuidSet(i.Object.OVSUUID)}}, []string{"_uuid"}, []any{}))
	}
	return ops, nil
}

func compileBridgeExecution(id, marker, creationMarker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	i, ok := bridgeIntent(envelope.Candidate)
	if !ok || i.Creation.BeforePresent == i.Creation.AfterPresent {
		return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
	}
	root := view.Observation.Rows["Open_vSwitch"][i.Creation.Root]
	next, err := number(root.Values["next_cfg"])
	if err != nil || next == math.MaxInt64 {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	cur, err := number(root.Values["cur_cfg"])
	if err != nil || cur > next {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != native.TypeInteger || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
		}
	}
	meta, ok := root.Values["external_ids"].(map[string]any)
	if !ok || len(meta) >= 128 && meta[bridgeMarkerKey] == nil {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_CAPACITY")
	}
	g, err := guard(d, "Open_vSwitch", root, []string{"external_ids"}, []any{uuidCondition(root.UUID), []any{"next_cfg", "<", json.Number(strconv.FormatInt(math.MaxInt64, 10))}, []any{"next_cfg", ">=", next}, []any{"cur_cfg", ">=", cur}})
	if err != nil {
		return out, err
	}
	ops, err := graphGuards(d, i, creationMarker, i.Creation.BeforePresent)
	if err != nil {
		return out, err
	}
	if deletion := envelope.Candidate.Intents[0].Deletion; deletion != nil && deletion.Restoring {
		ops = append(ops, deletedSourceGuards(deletion)...)
	}
	n := nativePlan{Operations: append([]map[string]any{g}, ops...), Evidence: view.Observation.Evidence, CreationMarker: creationMarker, InsertUUIDs: map[int]string{}}
	if i.Creation.AfterPresent {
		rows, err := creationRows(d, i, creationMarker)
		if err != nil {
			return out, err
		}
		bindings := candidate.CreationBindings(i)
		for k := len(bindings) - 1; k >= 0; k-- {
			b := bindings[k]
			n.InsertUUIDs[len(n.Operations)] = b.OVSUUID
			n.Operations = append(n.Operations, map[string]any{"op": "insert", "table": b.Table, "uuid": b.OVSUUID, "row": rows[b.Table]})
		}
	}
	mutation := "insert"
	if !i.Creation.AfterPresent {
		mutation = "delete"
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{
		[]any{"bridges", mutation, uuidSet(i.Object.OVSUUID)}, []any{"external_ids", "delete", []any{"set", []any{bridgeMarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{bridgeMarkerKey, marker}}}}, []any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data}, nil
}

func bridgeProof(p execution.Plan, view inventory.ExecutionView, d discovered) ([]any, error) {
	i, ok := bridgeIntent(p.Envelope.Candidate)
	if !ok {
		return nil, errors.New("INVALID_BRIDGE_PLAN")
	}
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil {
		return nil, errors.New("INVALID_BRIDGE_PLAN")
	}
	root := view.Observation.Rows["Open_vSwitch"][p.Root]
	labels, _ := root.Values["external_ids"].(map[string]any)
	if labels[bridgeMarkerKey] != p.Marker {
		return nil, errors.New("BRIDGE_COMMIT_MARKER_CHANGED")
	}
	g, err := guard(d, "Open_vSwitch", root, []string{"external_ids"}, []any{uuidCondition(p.Root)})
	if err != nil {
		return nil, err
	}
	guards, err := graphGuards(d, i, n.CreationMarker, i.Creation.AfterPresent)
	if err != nil {
		return nil, err
	}
	if deletion := p.Envelope.Candidate.Intents[0].Deletion; deletion != nil && deletion.Restoring {
		guards = append(guards, deletedSourceGuards(deletion)...)
	}
	ops := []any{g}
	for _, guard := range guards {
		ops = append(ops, guard)
	}
	if i.Creation.AfterPresent {
		iface := view.Observation.Rows["Interface"][i.Creation.Interface.OVSUUID]
		if !createdInterfaceHealthy(iface) {
			return nil, errors.New("BRIDGE_INTERFACE_NOT_APPLIED")
		}
		g, err := guard(d, "Interface", iface, []string{"error", "ofport"}, []any{uuidCondition(iface.UUID)})
		if err != nil {
			return nil, err
		}
		ops = append(ops, g)
	}
	return append(ops, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(p.Root)}, "columns": []string{"_uuid", "next_cfg", "cur_cfg"}}), nil
}

func createdInterfaceHealthy(row inventory.Row) bool {
	err, ok := row.Values["error"].([]any)
	if !ok || len(err) != 0 {
		return false
	}
	values, ok := row.Values["ofport"].([]any)
	if !ok {
		values = []any{row.Values["ofport"]}
	}
	if len(values) != 1 {
		return false
	}
	n, e := number(values[0])
	return e == nil && n == 65534
}
