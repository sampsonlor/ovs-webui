package ovsdb

import (
	"encoding/json"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"slices"
)

var treeFenceTables = []string{"Open_vSwitch", "Bridge", "Port", "Interface"}
var treeFenceChanged = errors.New("SPANNING_TREE_NATIVE_FENCE_CHANGED")

func treeFenceOperations(v inventory.ExecutionView, d discovered) ([]map[string]any, error) {
	ops := []map[string]any{}
	count := 0
	for _, table := range treeFenceTables {
		rows, exists := v.Observation.Rows[table]
		if !exists {
			return nil, treeFenceChanged
		}
		columns := []string{"_uuid"}
		for name, c := range d.native.Tables[table].Columns {
			if graphConfigurationColumn(table, name, c) {
				columns = append(columns, name)
			}
		}
		if table == "Open_vSwitch" {
			if len(rows) != 1 {
				return nil, treeFenceChanged
			}
			columns = append(columns, "next_cfg", "cur_cfg")
		}
		slices.Sort(columns)
		ids := []string{}
		for id := range rows {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		expected := []any{}
		for _, id := range ids {
			row := rows[id]
			count++
			if count > inventory.MaxRows || row.UUID != id || !apitypes.UUID(id) {
				return nil, treeFenceChanged
			}
			values := map[string]any{"_uuid": uuidValue(id)}
			for _, name := range columns {
				if name == "_uuid" {
					continue
				}
				value, known := row.Configuration[name]
				if table == "Open_vSwitch" && (name == "next_cfg" || name == "cur_cfg") {
					next, e1 := number(row.Values["next_cfg"])
					current, e2 := number(row.Values["cur_cfg"])
					if id != v.Observation.Evidence.Root || e1 != nil || e2 != nil || next != current {
						return nil, errors.New("SPANNING_TREE_CONFIGURATION_NOT_SETTLED")
					}
					value, known = row.Values[name]
				}
				if !known {
					return nil, treeFenceChanged
				}
				native, err := nativeValue(value, d.native.Tables[table].Columns[name])
				if err != nil {
					return nil, treeFenceChanged
				}
				values[name] = native
			}
			expected = append(expected, values)
		}
		ops = append(ops, waitRows(table, []any{}, columns, expected))
	}
	for _, table := range treeFenceTables {
		ops = append(ops, map[string]any{"op": "select", "table": table, "where": []any{}, "columns": []string{"_uuid", "_version"}})
	}
	data, err := json.Marshal(ops)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return nil, errors.New("SPANNING_TREE_FENCE_BUDGET")
	}
	return ops, nil
}
