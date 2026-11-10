package ovsdb

import (
	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

func qosReadSchema(d discovered, raw map[string]any) bool {
	for _, table := range []string{"QoS", "Queue"} {
		root, err := d.native.IsRoot(table)
		if err != nil || !root || !multicastMapConstraint(d.native.Tables[table].Columns["other_config"], multicastRawColumnType(raw, table, "other_config")) {
			return false
		}
	}
	qos := d.native.Tables["QoS"].Columns
	if qos["type"] == nil || qos["type"].Type != native.TypeString ||
		!mirrorReference(d.native.Tables["Port"].Columns["qos"], multicastRawColumnType(raw, "Port", "qos"), "QoS", "strong", 1) ||
		!mirrorInteger(d.native.Tables["Queue"].Columns["dscp"], multicastRawColumnType(raw, "Queue", "dscp"), 0, 63, 1) {
		return false
	}
	queues := qos["queues"]
	if queues == nil || queues.Type != native.TypeMap || queues.TypeObj == nil {
		return false
	}
	t := queues.TypeObj
	if t.Min() != 0 || t.Key == nil || t.Key.Type != native.TypeInteger || t.Value == nil || t.Value.Type != native.TypeUUID {
		return false
	}
	table, tableErr := t.Value.RefTable()
	strength, strengthErr := t.Value.RefType()
	return tableErr == nil && strengthErr == nil && table == "Queue" && strength == "strong"
}

// Oversized optional QoS maps are unknown. Keep the rest of the inventory
// usable and distinguish an unavailable queue mapping from a known empty one.
func normalizeInventoryColumn(table string, value any, column *native.ColumnSchema) (any, error) {
	if (table == "QoS" || table == "Queue") && column.Type == native.TypeMap {
		if wire, ok := value.([]any); ok && len(wire) == 2 && wire[0] == "map" {
			if pairs, ok := wire[1].([]any); ok && len(pairs) > 128 {
				return nil, nil
			}
		}
	}
	return normalize(value, column)
}

func sanitizeQoSOptions(value any) any {
	options, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	safe := map[string]any{}
	for _, key := range []string{"min-rate", "max-rate"} {
		if value, present := options[key]; present {
			safe[key] = nil
			if rate, ok := value.(string); ok && len(rate) <= 20 {
				safe[key] = rate
			}
		}
	}
	return safe
}
