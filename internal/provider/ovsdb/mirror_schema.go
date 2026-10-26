package ovsdb

import (
	"bytes"
	"encoding/json"
	"slices"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

func mirrorFreeAtom(value any, typ string) bool {
	if value == typ {
		return true
	}
	m, ok := value.(map[string]any)
	return ok && len(m) == 1 && m["type"] == typ
}

func mirrorReference(c *native.ColumnSchema, raw any, table, strength string, max int) bool {
	if c == nil || !c.Mutable() || c.Ephemeral() || c.Type != native.TypeSet || c.TypeObj == nil || c.TypeObj.Value != nil || c.TypeObj.Key == nil || c.TypeObj.Min() != 0 || c.TypeObj.Max() != max {
		return false
	}
	b := c.TypeObj.Key
	ref, e1 := b.RefTable()
	kind, e2 := b.RefType()
	if b.Type != native.TypeUUID || len(b.Enum) != 0 || e1 != nil || e2 != nil || ref != table || kind != strength {
		return false
	}
	m, ok := raw.(map[string]any)
	key, keyOK := m["key"].(map[string]any)
	if !ok || !keyOK || len(m) != 3 || m["min"] != json.Number("0") || key["type"] != "uuid" || key["refTable"] != table {
		return false
	}
	if len(key) != 3 && !(len(key) == 2 && strength == "strong") || key["refType"] != nil && key["refType"] != strength {
		return false
	}
	return max == native.Unlimited && m["max"] == "unlimited" || max != native.Unlimited && m["max"] == json.Number("1")
}

func mirrorInteger(c *native.ColumnSchema, raw any, min, max, count int) bool {
	if c == nil || !c.Mutable() || c.Ephemeral() || c.Type != native.TypeSet || c.TypeObj == nil || c.TypeObj.Key == nil || c.TypeObj.Value != nil || c.TypeObj.Min() != 0 || c.TypeObj.Max() != count {
		return false
	}
	// Compare the complete wire descriptor as well as the typed codec. Unknown
	// constraints must not disappear when qualifying a new native writer.
	want := map[string]any{"key": map[string]any{"type": "integer", "minInteger": min, "maxInteger": max}, "min": 0, "max": count}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(raw)
	return bytes.Equal(a, b)
}

func mirrorSchemaSupported(d discovered, raw map[string]any) bool {
	if !d.public.BridgeCreation || !d.public.InternalPortCreation {
		return false
	}
	// A map-valued incoming Mirror reference cannot be guarded by the closed
	// scalar/set includes checks. Reject it for creation as well as existing
	// sessions, before allocating or mutating any native object.
	for _, table := range d.native.Tables {
		for _, c := range table.Columns {
			if c == nil || c.TypeObj == nil || c.Type != native.TypeMap {
				continue
			}
			for _, base := range []*native.BaseType{c.TypeObj.Key, c.TypeObj.Value} {
				if base != nil && base.Type == native.TypeUUID {
					if target, _ := base.RefTable(); target == "Mirror" {
						return false
					}
				}
			}
		}
	}
	t, exists := d.native.Tables["Mirror"]
	root, err := d.native.IsRoot("Mirror")
	if !exists || err != nil || root {
		return false
	}
	if !mirrorReference(d.native.Tables["Bridge"].Columns["mirrors"], multicastRawColumnType(raw, "Bridge", "mirrors"), "Mirror", "strong", native.Unlimited) {
		return false
	}
	known := []string{"name", "select_all", "select_src_port", "select_dst_port", "select_vlan", "output_port", "output_vlan", "snaplen", "external_ids", "statistics", "filter"}
	for name, c := range t.Columns {
		if !slices.Contains(known, name) || name != "statistics" && (c == nil || !c.Mutable() || c.Ephemeral()) {
			return false
		}
	}
	if c := t.Columns["name"]; c == nil || c.Type != native.TypeString || !mirrorFreeAtom(multicastRawColumnType(raw, "Mirror", "name"), "string") {
		return false
	}
	if c := t.Columns["filter"]; c != nil {
		wire, ok := multicastRawColumnType(raw, "Mirror", "filter").(map[string]any)
		if c.Type != native.TypeSet || c.TypeObj == nil || c.TypeObj.Min() != 0 || c.TypeObj.Max() != 1 || !ok || len(wire) != 3 || wire["min"] != json.Number("0") || wire["max"] != json.Number("1") || !mirrorFreeAtom(wire["key"], "string") {
			return false
		}
	}
	if !isolationConstraint(t.Columns["select_all"]) || !mirrorFreeAtom(multicastRawColumnType(raw, "Mirror", "select_all"), "boolean") {
		return false
	}
	for _, name := range []string{"select_src_port", "select_dst_port", "output_port"} {
		max := native.Unlimited
		if name == "output_port" {
			max = 1
		}
		if !mirrorReference(t.Columns[name], multicastRawColumnType(raw, "Mirror", name), "Port", "weak", max) {
			return false
		}
	}
	if !mirrorInteger(t.Columns["select_vlan"], multicastRawColumnType(raw, "Mirror", "select_vlan"), 0, 4095, 4096) || !mirrorInteger(t.Columns["output_vlan"], multicastRawColumnType(raw, "Mirror", "output_vlan"), 1, 4095, 1) || !mirrorInteger(t.Columns["snaplen"], multicastRawColumnType(raw, "Mirror", "snaplen"), 14, 65535, 1) {
		return false
	}
	return multicastMapConstraint(t.Columns["external_ids"], multicastRawColumnType(raw, "Mirror", "external_ids"))
}

func mirrorDefaults(d discovered) map[string]any {
	if !d.public.MirrorConfiguration {
		return nil
	}
	out := map[string]any{}
	for name, c := range d.native.Tables["Mirror"].Columns {
		if !graphConfigurationColumn("Mirror", name, c) {
			continue
		}
		value, err := emptyNative(c)
		if err != nil {
			return nil
		}
		data, _ := json.Marshal(value)
		var exact any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if decoder.Decode(&exact) != nil {
			return nil
		}
		value, err = normalize(exact, c)
		if err != nil {
			return nil
		}
		out[name] = value
	}
	return out
}
