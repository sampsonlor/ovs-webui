package ovsdb

import (
	"errors"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Mirror selectors read the whole native graph. Keep every persistent Mirror
// image in the same transaction as the four core tables, including sessions
// outside the changed Bridge. Statistics are observations, not desired state.
func mirrorFenceOperations(view inventory.ExecutionView, d discovered) ([]map[string]any, error) {
	if !d.public.MirrorConfiguration || !view.Candidate.Topology.MirrorSupported {
		return nil, errors.New("MIRROR_SCHEMA_UNSUPPORTED")
	}
	core, err := treeFenceOperations(view, d)
	if err != nil {
		return nil, err
	}
	rows, known := view.Observation.Rows["Mirror"]
	if !known {
		return nil, errors.New("MIRROR_NATIVE_IMAGE_UNKNOWN")
	}
	columns := []string{"_uuid"}
	for name, c := range d.native.Tables["Mirror"].Columns {
		if graphConfigurationColumn("Mirror", name, c) {
			columns = append(columns, name)
		}
	}
	slices.Sort(columns)
	ids := []string{}
	for id := range rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	count := len(ids)
	for _, table := range treeFenceTables {
		count += len(view.Observation.Rows[table])
	}
	if count > inventory.MaxRows {
		return nil, errors.New("MIRROR_NATIVE_IMAGE_BUDGET")
	}
	expected := []any{}
	for _, id := range ids {
		row := rows[id]
		if row.UUID != id || !apitypes.UUID(id) {
			return nil, errors.New("MIRROR_NATIVE_IMAGE_UNKNOWN")
		}
		values := map[string]any{"_uuid": uuidValue(id)}
		for _, name := range columns {
			if name == "_uuid" {
				continue
			}
			value, known := row.Configuration[name]
			if !known {
				return nil, errors.New("MIRROR_NATIVE_IMAGE_UNKNOWN")
			}
			wire, err := nativeValue(value, d.native.Tables["Mirror"].Columns[name])
			if err != nil {
				return nil, errors.New("MIRROR_NATIVE_IMAGE_UNKNOWN")
			}
			values[name] = wire
		}
		expected = append(expected, values)
	}
	return append(core[:4], waitRows("Mirror", []any{}, columns, expected)), nil
}

func mirrorGraphReference(table, column string) bool {
	return table == "Open_vSwitch" && column == "bridges" ||
		table == "Bridge" && slices.Contains([]string{"ports", "mirrors"}, column) ||
		table == "Port" && column == "interfaces" ||
		table == "Mirror" && slices.Contains([]string{"select_src_port", "select_dst_port", "output_port"}, column)
}

// Known Mirror references are independent of Bridge->Port->Interface Links.
// Read their exact incoming row sets from the immutable private observation.
// A newly introduced map reference cannot be represented by a partial includes
// condition, so it blocks this writer instead of being silently severed.
func mirrorReferenceOperations(d discovered, nodes []candidate.TopologyNode, view inventory.ExecutionView) ([]map[string]any, error) {
	ops := []map[string]any{}
	for _, node := range nodes {
		for table, schema := range d.native.Tables {
			for column, c := range schema.Columns {
				if c == nil || c.TypeObj == nil {
					continue
				}
				if value := c.TypeObj.Value; value != nil && value.Type == "uuid" {
					if target, _ := value.RefTable(); target == node.Binding.Table {
						return nil, errors.New("MIRROR_FOREIGN_REFERENCE_UNQUALIFIED")
					}
				}
				key := c.TypeObj.Key
				if key == nil || key.Type != "uuid" {
					continue
				}
				target, _ := key.RefTable()
				if target != node.Binding.Table {
					continue
				}
				if c.TypeObj.Value != nil {
					return nil, errors.New("MIRROR_FOREIGN_REFERENCE_UNQUALIFIED")
				}
				want := []any{}
				if mirrorGraphReference(table, column) {
					rows, known := view.Observation.Rows[table]
					if !known {
						return nil, errors.New("MIRROR_NATIVE_IMAGE_UNKNOWN")
					}
					ids := []string{}
					for id := range rows {
						ids = append(ids, id)
					}
					slices.Sort(ids)
					for _, id := range ids {
						refs, known := rows[id].Configuration[column].([]any)
						if !known {
							return nil, errors.New("MIRROR_NATIVE_IMAGE_UNKNOWN")
						}
						if slices.Contains(refs, any(node.Binding.OVSUUID)) {
							want = append(want, map[string]any{"_uuid": uuidValue(id)})
						}
					}
				}
				value := any(uuidSet(node.Binding.OVSUUID))
				if c.Type == "uuid" {
					value = uuidValue(node.Binding.OVSUUID)
				}
				ops = append(ops, waitRows(table, []any{[]any{column, "includes", value}}, []string{"_uuid"}, want))
			}
		}
	}
	return ops, nil
}
