package ovsdb

import (
	"encoding/json"
	"slices"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

func qosCreationSchema(d discovered, raw map[string]any) bool {
	if !d.public.QoSRateWrite ||
		!multicastMapConstraint(d.native.Tables["Open_vSwitch"].Columns["external_ids"], multicastRawColumnType(raw, "Open_vSwitch", "external_ids")) {
		return false
	}
	// Compensation deletes the created rows. Qualify every column so a
	// deployment with unobserved persistent data cannot enter that path.
	for table, names := range map[string][]string{
		"QoS":   {"type", "queues", "other_config", "external_ids"},
		"Queue": {"dscp", "other_config", "external_ids"},
	} {
		cols := d.native.Tables[table].Columns
		if len(cols) != len(names) {
			return false
		}
		for _, name := range names {
			if col := cols[name]; col == nil || col.Ephemeral() || !col.Mutable() {
				return false
			}
		}
	}
	if !mirrorFreeAtom(multicastRawColumnType(raw, "QoS", "type"), "string") {
		return false
	}
	q, ok := multicastRawColumnType(raw, "QoS", "queues").(map[string]any)
	key, keyOK := q["key"].(map[string]any)
	val, valOK := q["value"].(map[string]any)
	return ok && keyOK && valOK && len(q) == 4 && q["min"] == json.Number("0") && q["max"] == "unlimited" &&
		len(key) == 3 && key["type"] == "integer" && key["minInteger"] == json.Number("0") && key["maxInteger"] == json.Number("4294967295") &&
		val["type"] == "uuid" && val["refTable"] == "Queue" &&
		(len(val) == 2 || len(val) == 3 && val["refType"] == "strong")
}

func qosWriteSchema(d discovered, raw map[string]any) bool {
	if !d.public.QoSConfiguration {
		return false
	}
	for _, table := range []string{"QoS", "Queue", "Port"} {
		labels := d.native.Tables[table].Columns["external_ids"]
		if !multicastMapConstraint(labels, multicastRawColumnType(raw, table, "external_ids")) {
			return false
		}
	}
	for _, table := range d.public.Tables {
		for _, column := range table.Columns {
			for _, ref := range column.References {
				if ref.Table == "QoS" && (table.Name != "Port" || column.Name != "qos") ||
					ref.Table == "Queue" && (table.Name != "QoS" || column.Name != "queues") {
					return false
				}
			}
		}
	}
	return true
}

func qosPrivateColumn(table, name string, c *native.ColumnSchema) bool {
	return (table == "QoS" || table == "Queue") && slices.Contains([]string{"other_config", "external_ids"}, name) && c != nil && !c.Ephemeral() &&
		bondConstraint("other_config", c)
}

func boundedPrivateQoS(table string, value any) any {
	if table != "QoS" && table != "Queue" {
		return value
	}
	options, _ := value.(map[string]any)
	size := 0
	for k, v := range options {
		text, _ := v.(string)
		size += len(k) + len(text)
	}
	if size > 4096 {
		return nil
	}
	return value
}

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
func normalizeInventoryColumn(table, name string, value any, column *native.ColumnSchema) (any, error) {
	if telemetryTable(table) {
		normalized, err := normalize(value, column)
		if err != nil {
			return nil, nil
		}
		return constrainTelemetryField(table, name, normalized), nil
	}
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
