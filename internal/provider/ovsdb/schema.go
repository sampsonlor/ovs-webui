// Package ovsdb is the only adapter allowed to understand native OVSDB types.
// It uses the approved libovsdb schema codec; transport is deliberately limited
// to discovery, monitor and echo, with allocation limits before JSON decoding.
package ovsdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

var selected = map[string][]string{
	"Open_vSwitch": {"bridges", "cur_cfg", "next_cfg", "ovs_version", "external_ids"},
	"Bridge":       {"name", "ports", "datapath_type", "controller", "fail_mode", "stp_enable", "rstp_enable", "flood_vlans", "external_ids"},
	"Port":         {"name", "interfaces", "vlan_mode", "tag", "trunks", "cvlans", "lacp", "bond_mode", "other_config", "external_ids"},
	"Interface":    {"name", "type", "options", "link_state", "admin_state", "ofport", "ifindex", "mtu", "link_speed", "duplex", "error", "external_ids"},
}
var required = map[string][]string{"Open_vSwitch": {"bridges"}, "Bridge": {"name", "ports"}, "Port": {"name", "interfaces"}, "Interface": {"name"}}

type discovered struct {
	public   inventory.Schema
	native   native.DatabaseSchema
	requests map[string]any
}

func discover(data []byte) (discovered, error) {
	var d discovered
	if len(data) > 512<<10 || validateJSON(data) != nil {
		return d, errors.New("OVSDB_SCHEMA_INVALID")
	}
	if err := json.Unmarshal(data, &d.native); err != nil || d.native.Name != "Open_vSwitch" || len(d.native.Tables) > 128 {
		return d, errors.New("OVSDB_SCHEMA_INVALID")
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&raw) != nil {
		return d, errors.New("OVSDB_SCHEMA_INVALID")
	}
	d.public = inventory.Schema{Name: d.native.Name, Version: d.native.Version, Digest: inventory.Digest(raw), Tables: []inventory.Table{}}
	d.requests = map[string]any{}
	for t, columns := range required {
		table := d.native.Tables[t]
		for _, c := range columns {
			if table.Columns[c] == nil {
				return d, errors.New("OVSDB_CORE_SCHEMA_UNSUPPORTED")
			}
		}
	}
	for t, table := range d.native.Tables {
		if len(table.Columns) > 256 {
			return d, errors.New("OVSDB_SCHEMA_BUDGET")
		}
		root, _ := d.native.IsRoot(t)
		tt := inventory.Table{Name: t, Root: root, Indexes: table.Indexes, Columns: []inventory.Column{}}
		if tt.Indexes == nil {
			tt.Indexes = [][]string{}
		}
		names := []string{}
		for name, col := range table.Columns {
			if col == nil || col.TypeObj == nil || col.TypeObj.Key == nil {
				return d, errors.New("OVSDB_SCHEMA_INVALID")
			}
			typ, err := json.Marshal(col.TypeObj)
			if err != nil {
				return d, errors.New("OVSDB_SCHEMA_INVALID")
			}
			c := inventory.Column{Name: name, Type: col.Type, NativeType: typ, Mutable: col.Mutable(), Ephemeral: col.Ephemeral(), References: []inventory.Reference{}, Monitored: slices.Contains(selected[t], name)}
			if t == "Port" {
				c.VLANCompatible, c.VLANModes = vlanConstraint(name, col)
				c.BondCompatible = bondConstraint(name, col)
			}
			for pos, b := range map[string]*native.BaseType{"key": col.TypeObj.Key, "value": col.TypeObj.Value} {
				if b != nil && b.Type == native.TypeUUID {
					ref, _ := b.RefTable()
					strength, _ := b.RefType()
					if ref != "" {
						c.References = append(c.References, inventory.Reference{Table: ref, Strength: strength, Position: pos})
					}
				}
			}
			sort.Slice(c.References, func(i, j int) bool { return c.References[i].Position < c.References[j].Position })
			tt.Columns = append(tt.Columns, c)
			if c.Monitored {
				names = append(names, name)
			}
		}
		sort.Slice(tt.Columns, func(i, j int) bool { return tt.Columns[i].Name < tt.Columns[j].Name })
		d.public.Tables = append(d.public.Tables, tt)
		if _, ok := selected[t]; ok {
			sort.Strings(names)
			d.requests[t] = []any{map[string]any{"columns": names, "select": map[string]bool{"initial": true, "insert": true, "delete": true, "modify": true}}}
		}
	}
	sort.Slice(d.public.Tables, func(i, j int) bool { return d.public.Tables[i].Name < d.public.Tables[j].Name })
	return d, nil
}

// Keep signed 64-bit native integers exact: libovsdb's generic Row decoder uses
// float64, so the bounded read adapter decodes values with json.Number instead.
func atom(v any, typ string) (any, error) {
	switch typ {
	case native.TypeInteger:
		n, ok := v.(json.Number)
		if ok {
			if _, err := strconv.ParseInt(string(n), 10, 64); err == nil {
				return string(n), nil
			}
		}
	case native.TypeReal:
		n, ok := v.(json.Number)
		if ok {
			if _, err := strconv.ParseFloat(string(n), 64); err == nil {
				return string(n), nil
			}
		}
	case native.TypeString:
		s, ok := v.(string)
		if ok && len(s) <= 4096 {
			return s, nil
		}
	case native.TypeBoolean:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case native.TypeUUID:
		a, ok := v.([]any)
		if ok && len(a) == 2 && a[0] == "uuid" {
			if s, ok := a[1].(string); ok && apitypes.UUID(s) {
				return s, nil
			}
		}
	}
	return nil, errors.New("OVSDB_VALUE_TYPE_INVALID")
}
func normalize(v any, c *native.ColumnSchema) (any, error) {
	if c.Type == native.TypeMap {
		a, ok := v.([]any)
		if !ok || len(a) != 2 || a[0] != "map" {
			return nil, errors.New("OVSDB_MAP_INVALID")
		}
		pairs, ok := a[1].([]any)
		if !ok || len(pairs) > 128 {
			return nil, errors.New("OVSDB_MAP_BUDGET")
		}
		out := map[string]any{}
		for _, pair := range pairs {
			p, ok := pair.([]any)
			if !ok || len(p) != 2 {
				return nil, errors.New("OVSDB_MAP_INVALID")
			}
			key, err := atom(p[0], c.TypeObj.Key.Type)
			if err != nil {
				return nil, err
			}
			value, err := atom(p[1], c.TypeObj.Value.Type)
			if err != nil {
				return nil, err
			}
			s := fmt.Sprint(key)
			if _, ok = out[s]; ok {
				return nil, errors.New("OVSDB_DUPLICATE_KEY")
			}
			out[s] = value
		}
		return out, nil
	}
	if c.Type == native.TypeSet {
		values := []any{v}
		if a, ok := v.([]any); ok && len(a) == 2 && a[0] == "set" {
			var valid bool
			values, valid = a[1].([]any)
			if !valid {
				return nil, errors.New("OVSDB_SET_INVALID")
			}
		}
		if len(values) > 4096 || len(values) < c.TypeObj.Min() || (c.TypeObj.Max() != native.Unlimited && len(values) > c.TypeObj.Max()) {
			return nil, errors.New("OVSDB_SET_BUDGET")
		}
		out := []any{}
		seen := map[string]bool{}
		for _, v := range values {
			n, err := atom(v, c.TypeObj.Key.Type)
			if err != nil {
				return nil, err
			}
			k := inventory.Digest(n)
			if seen[k] {
				return nil, errors.New("OVSDB_DUPLICATE_ELEMENT")
			}
			seen[k] = true
			out = append(out, n)
		}
		sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
		return out, nil
	}
	return atom(v, c.TypeObj.Key.Type)
}

type rowUpdate struct {
	New json.RawMessage `json:"new"`
	Old json.RawMessage `json:"old"`
}

func update(d discovered, rows inventory.Rows, data []byte, initial bool) error {
	if validateJSON(data) != nil {
		return errors.New("OVSDB_UPDATE_INVALID")
	}
	var u map[string]map[string]rowUpdate
	if json.Unmarshal(data, &u) != nil {
		return errors.New("OVSDB_UPDATE_INVALID")
	}
	count := 0
	for _, r := range rows {
		count += len(r)
	}
	for table, updates := range u {
		if _, ok := d.requests[table]; !ok {
			return errors.New("OVSDB_UNREQUESTED_TABLE")
		}
		if rows[table] == nil {
			rows[table] = map[string]inventory.Row{}
		}
		for id, change := range updates {
			if !apitypes.UUID(id) {
				return errors.New("OVSDB_INVALID_UUID")
			}
			prior, exists := rows[table][id]
			hasOld := len(change.Old) > 0 && string(change.Old) != "null"
			hasNew := len(change.New) > 0 && string(change.New) != "null"
			if initial && hasOld || (!hasOld && !hasNew) || hasOld && !exists || !hasOld && exists {
				return errors.New("OVSDB_MONITOR_INCONSISTENT")
			}
			if hasOld {
				var old map[string]any
				dec := json.NewDecoder(bytes.NewReader(change.Old))
				dec.UseNumber()
				if dec.Decode(&old) != nil {
					return errors.New("OVSDB_ROW_INVALID")
				}
				for name, value := range old {
					if !slices.Contains(selected[table], name) || d.native.Tables[table].Columns[name] == nil {
						return errors.New("OVSDB_UNREQUESTED_COLUMN")
					}
					n, err := normalize(value, d.native.Tables[table].Columns[name])
					if err != nil {
						return err
					}
					if inventory.Digest(sanitize(table, name, n)) != inventory.Digest(prior.Values[name]) {
						return errors.New("OVSDB_MONITOR_INCONSISTENT")
					}
				}
			}
			if !hasNew {
				delete(rows[table], id)
				count--
				continue
			}
			var values map[string]any
			dec := json.NewDecoder(bytes.NewReader(change.New))
			dec.UseNumber()
			if dec.Decode(&values) != nil {
				return errors.New("OVSDB_ROW_INVALID")
			}
			if !exists {
				prior = inventory.Row{UUID: id, Values: map[string]any{}}
				count++
				if count > inventory.MaxRows {
					return errors.New("OVSDB_INVENTORY_BUDGET")
				}
			}
			for name, v := range values {
				if !slices.Contains(selected[table], name) || d.native.Tables[table].Columns[name] == nil {
					return errors.New("OVSDB_UNREQUESTED_COLUMN")
				}
				n, err := normalize(v, d.native.Tables[table].Columns[name])
				if err != nil {
					return err
				}
				prior.Values[name] = sanitize(table, name, n)
			}
			b, _ := json.Marshal(prior)
			if len(b) > inventory.MaxRowBytes {
				return errors.New("OVSDB_ROW_BUDGET")
			}
			rows[table][id] = prior
		}
	}
	// A whole monitor transaction is validated before publication, never a row
	// callback exposing dangling intermediate Bridge -> Port -> Interface links.
	return nil
}
func sanitize(table, name string, n any) any {
	if table != "Interface" {
		return n
	}
	if name == "options" {
		safe := map[string]any{}
		m, _ := n.(map[string]any)
		for k, v := range m {
			if slices.Contains([]string{"peer", "remote_ip", "local_ip", "dst_port", "key"}, k) {
				safe[k] = v
			}
		}
		return safe
	}
	// Native free-form errors may contain command options. Publish their presence
	// rather than forwarding a daemon's potentially sensitive diagnostic text.
	if name == "error" {
		if len(references(n)) > 0 {
			return []any{"provider-reported-error"}
		}
		return []any{}
	}
	return n
}
func references(v any) []string {
	out := []string{}
	if s, ok := v.(string); ok {
		return []string{s}
	}
	if a, ok := v.([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func validateRelations(rows inventory.Rows) error {
	if len(rows["Open_vSwitch"]) > 1 {
		return errors.New("OVSDB_MULTIPLE_ROOTS")
	}
	parents := map[string]string{}
	for _, edge := range []struct{ from, column, to string }{{"Open_vSwitch", "bridges", "Bridge"}, {"Bridge", "ports", "Port"}, {"Port", "interfaces", "Interface"}} {
		for id, row := range rows[edge.from] {
			v, exists := row.Values[edge.column]
			if !exists {
				return errors.New("OVSDB_RELATION_UNKNOWN")
			}
			refs := references(v)
			if (edge.from == "Bridge" && len(refs) > 500) || (edge.from == "Port" && (len(refs) < 1 || len(refs) > 128)) {
				return errors.New("OVSDB_RELATION_BUDGET")
			}
			for _, ref := range refs {
				if _, ok := rows[edge.to][ref]; !ok {
					return errors.New("OVSDB_REFERENCE_MISSING")
				}
				k := inventory.Key(edge.to, ref)
				if _, ok := parents[k]; ok {
					return errors.New("OVSDB_REFERENCE_AMBIGUOUS")
				}
				parents[k] = id
			}
		}
	}
	for table, objects := range rows {
		if table == "Open_vSwitch" {
			continue
		}
		for uuid, row := range objects {
			if _, ok := parents[inventory.Key(table, uuid)]; !ok {
				return errors.New("OVSDB_ORPHAN_OBJECT")
			}
			name, ok := row.Values["name"].(string)
			if !ok || len(name) == 0 || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") {
				return errors.New("OVSDB_NAME_INVALID")
			}
		}
	}
	return nil
}
