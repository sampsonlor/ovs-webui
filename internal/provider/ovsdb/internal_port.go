package ovsdb

import (
	"encoding/json"
	"errors"
	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"math"
	"slices"
	"strconv"
	"time"
)

func internalPortIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) == 1 {
		return candidate.InternalPortGraphIntent(c.Intents[0])
	}
	return candidate.StoredIntent{}, false
}
func internalPortSchemaSupported(d discovered) bool {
	if !bridgeSchemaSupported(d) {
		return false
	}
	for _, name := range []string{"vlan_mode", "tag", "trunks", "cvlans"} {
		col := d.native.Tables["Port"].Columns[name]
		if col == nil {
			return false
		}
		ok, modes := vlanConstraint(name, col)
		if !ok || name == "vlan_mode" && !slices.Contains(modes, "access") {
			return false
		}
	}
	return true
}
func internalPortRows(d discovered, i candidate.StoredIntent, marker string) (map[string]map[string]any, error) {
	p := i.PortCreation
	if p == nil || !internalPortSchemaSupported(d) || p.VLANID < 1 || p.VLANID > 4094 {
		return nil, errors.New("INTERNAL_PORT_SCHEMA_UNSUPPORTED")
	}
	// Reuse the canonical empty configuration defaults; the existing Bridge is
	// never inserted or rewritten. The legacy row marker also serves Port IDs.
	synthetic := candidate.StoredIntent{Object: p.Bridge, Creation: &candidate.BridgeCreation{Name: p.Name, Root: p.Root, Port: i.Object, Interface: p.Interface}}
	rows, err := creationRows(d, synthetic, marker)
	if err != nil {
		return nil, err
	}
	delete(rows, "Bridge")
	rows["Port"]["vlan_mode"] = []any{"set", []any{"access"}}
	rows["Port"]["tag"] = []any{"set", []any{p.VLANID}}
	return rows, nil
}

func internalParentGuards(d discovered, i candidate.StoredIntent, view inventory.ExecutionView, present bool) ([]map[string]any, error) {
	p := i.PortCreation
	if p == nil {
		return nil, errors.New("INVALID_INTERNAL_PORT")
	}
	parent, ok := view.Candidate.InternalPorts.Parents[p.Bridge.ManagementID]
	if !ok || parent.Binding != p.Bridge || parent.Dependency != i.Dependency || parent.LocalPort != p.LocalPort || parent.LocalInterface != p.LocalInterface {
		return nil, errors.New("INTERNAL_PORT_PARENT_CHANGED")
	}
	ops := []map[string]any{}
	for _, b := range []candidate.Binding{p.Bridge, p.LocalPort, p.LocalInterface} {
		row := view.Observation.Rows[b.Table][b.OVSUUID]
		values := inventory.InternalPortParentValues(b.Table, row)
		if row.UUID == "" || len(values) == 0 {
			return nil, errors.New("INTERNAL_PORT_PARENT_CHANGED")
		}
		cols := []string{}
		for col := range values {
			cols = append(cols, col)
		}
		slices.Sort(cols)
		g, err := guard(d, b.Table, row, cols, []any{uuidCondition(b.OVSUUID)})
		if err != nil {
			return nil, err
		}
		ops = append(ops, g)
	}
	members := []any{}
	for _, b := range p.Members {
		members = append(members, uuidValue(b.OVSUUID))
	}
	if present {
		members = append(members, uuidValue(i.Object.OVSUUID))
	}
	ops = append(ops, waitRows("Bridge", []any{uuidCondition(p.Bridge.OVSUUID)}, []string{"ports"}, []any{map[string]any{"ports": []any{"set", members}}}),
		waitRows("Open_vSwitch", []any{[]any{"bridges", "includes", uuidSet(p.Bridge.OVSUUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(p.Root)}}))
	return ops, nil
}

func compileInternalPortExecution(id, marker, creationMarker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	i, ok := internalPortIntent(envelope.Candidate)
	if !ok || i.PortCreation.BeforePresent == i.PortCreation.AfterPresent {
		return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
	}
	p := i.PortCreation
	root := view.Observation.Rows["Open_vSwitch"][p.Root]
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
	ops, err := graphGuards(d, i, creationMarker, p.BeforePresent)
	if err != nil {
		return out, err
	}
	parents, err := internalParentGuards(d, i, view, p.BeforePresent)
	if err != nil {
		return out, err
	}
	n := nativePlan{Operations: append([]map[string]any{g}, ops...), Evidence: view.Observation.Evidence, CreationMarker: creationMarker, InsertUUIDs: map[int]string{}}
	n.Operations = append(n.Operations, parents...)
	if deletion := envelope.Candidate.Intents[0].PortDeletion; deletion != nil && deletion.Restoring {
		n.Operations = append(n.Operations, deletedPortSourceGuards(deletion)...)
	}
	if p.AfterPresent {
		rows, err := internalPortRows(d, i, creationMarker)
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
	if !p.AfterPresent {
		mutation = "delete"
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations), len(n.Operations)+1)
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Bridge", "where": []any{uuidCondition(p.Bridge.OVSUUID)}, "mutations": []any{[]any{"ports", mutation, uuidSet(i.Object.OVSUUID)}}},
		map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{bridgeMarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{bridgeMarkerKey, marker}}}}, []any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data}, nil
}

func internalPortProof(p execution.Plan, view inventory.ExecutionView, d discovered) ([]any, error) {
	i, ok := internalPortIntent(p.Envelope.Candidate)
	if !ok {
		return nil, errors.New("INVALID_INTERNAL_PORT_PLAN")
	}
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil {
		return nil, errors.New("INVALID_INTERNAL_PORT_PLAN")
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
	guards, err := graphGuards(d, i, n.CreationMarker, i.PortCreation.AfterPresent)
	if err != nil {
		return nil, err
	}
	parents, err := internalParentGuards(d, i, view, i.PortCreation.AfterPresent)
	if err != nil {
		return nil, err
	}
	if deletion := p.Envelope.Candidate.Intents[0].PortDeletion; deletion != nil && deletion.Restoring {
		guards = append(guards, deletedPortSourceGuards(deletion)...)
	}
	ops := []any{g}
	for _, g := range append(guards, parents...) {
		ops = append(ops, g)
	}
	if i.PortCreation.AfterPresent {
		iface := view.Observation.Rows["Interface"][i.PortCreation.Interface.OVSUUID]
		if !internalInterfaceHealthy(iface) {
			return nil, errors.New("INTERNAL_PORT_INTERFACE_NOT_APPLIED")
		}
		g, err := guard(d, "Interface", iface, []string{"error", "ofport"}, []any{uuidCondition(iface.UUID)})
		if err != nil {
			return nil, err
		}
		ops = append(ops, g)
	}
	return append(ops, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(p.Root)}, "columns": []string{"_uuid", "next_cfg", "cur_cfg"}}), nil
}
func internalInterfaceHealthy(row inventory.Row) bool {
	errors, ok := row.Values["error"].([]any)
	if !ok || len(errors) != 0 {
		return false
	}
	values, ok := row.Values["ofport"].([]any)
	if !ok {
		values = []any{row.Values["ofport"]}
	}
	if len(values) != 1 {
		return false
	}
	n, err := number(values[0])
	return err == nil && n >= 1 && n <= 65279
}

func lifecycleHost(c candidate.Candidate) (string, bool, bool, bool) {
	if i, ok := internalPortIntent(c); ok {
		return i.PortCreation.Name, i.PortCreation.BeforePresent, i.PortCreation.AfterPresent, true
	}
	if i, ok := bridgeIntent(c); ok {
		return i.Creation.Name, i.Creation.BeforePresent, i.Creation.AfterPresent, true
	}
	return "", false, false, false
}
