package ovswitness

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed input-types.json
var inputTypes []byte

type columnType struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Min   int    `json:"min"`
	Max   any    `json:"max"`
}

func atom(v any, kind string) (string, error) {
	var raw string
	switch kind {
	case "string":
		s, ok := v.(string)
		if !ok || len(s) > 4096 || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return "", ErrInvalid
		}
		raw = "s" + s
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return "", ErrInvalid
		}
		i, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil || strconv.FormatInt(i, 10) != string(n) {
			return "", ErrInvalid
		}
		raw = "i" + string(n)
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return "", ErrInvalid
		}
		raw = "b0"
		if b {
			raw = "b1"
		}
	case "uuid":
		a, ok := v.([]any)
		if !ok || len(a) != 2 || a[0] != "uuid" {
			return "", ErrInvalid
		}
		s, ok := a[1].(string)
		if !ok || !uuid(s) {
			return "", ErrInvalid
		}
		raw = "u" + s
	default:
		return "", ErrInvalid
	}
	return hex.EncodeToString([]byte(raw)), nil
}

func datum(v any, t columnType) (string, error) {
	var entries []string
	keys := map[string]bool{}
	add := func(k, v any) error {
		key, err := atom(k, t.Key)
		if err != nil || keys[key] {
			return ErrInvalid
		}
		keys[key] = true
		value := ""
		if t.Value != "void" {
			value, err = atom(v, t.Value)
			if err != nil {
				return err
			}
		}
		entries = append(entries, key+":"+value)
		return nil
	}
	a, array := v.([]any)
	if t.Value != "void" {
		if !array || len(a) != 2 || a[0] != "map" {
			return "", ErrInvalid
		}
		pairs, ok := a[1].([]any)
		if !ok || len(pairs) > 128 {
			return "", ErrInvalid
		}
		for _, pair := range pairs {
			p, ok := pair.([]any)
			if !ok || len(p) != 2 || add(p[0], p[1]) != nil {
				return "", ErrInvalid
			}
		}
	} else if array && len(a) == 2 && a[0] == "set" {
		items, ok := a[1].([]any)
		if !ok || len(items) > 128 {
			return "", ErrInvalid
		}
		for _, item := range items {
			if add(item, nil) != nil {
				return "", ErrInvalid
			}
		}
	} else if add(v, nil) != nil {
		return "", ErrInvalid
	}
	if len(entries) < t.Min {
		return "", ErrInvalid
	}
	if max, ok := t.Max.(float64); ok && len(entries) > int(max) {
		return "", ErrInvalid
	}
	sort.Strings(entries)
	return strings.Join(entries, ","), nil
}

func references(v any) (map[string]bool, error) {
	items := []any{v}
	if a, ok := v.([]any); ok && len(a) == 2 && a[0] == "set" {
		var valid bool
		items, valid = a[1].([]any)
		if !valid {
			return nil, ErrInvalid
		}
	}
	if len(items) > 128 {
		return nil, ErrInvalid
	}
	result := map[string]bool{}
	for _, value := range items {
		id, err := atom(value, "uuid")
		if err != nil || result[id] {
			return nil, ErrInvalid
		}
		result[id] = true
	}
	return result, nil
}

func inputGraph(rows map[string][]map[string]any, registry map[string]map[string]columnType) bool {
	root, bridge := rows["Open_vSwitch"][0], rows["Bridge"][0]
	bridgeID, err := atom(bridge["_uuid"], "uuid")
	rootRefs, re := references(root["bridges"])
	ports, pe := references(bridge["ports"])
	if err != nil || re != nil || pe != nil || !rootRefs[bridgeID] || len(ports) != len(rows["Port"]) {
		return false
	}
	for table, fields := range map[string][]string{"Open_vSwitch": {"datapaths", "manager_options", "ssl"}, "Bridge": {"auto_attach", "controller", "flow_tables", "ipfix", "mirrors", "netflow", "sflow"}, "Port": {"qos"}} {
		for _, row := range rows[table] {
			for _, field := range fields {
				empty, err := datum(row[field], registry[table][field])
				if err != nil || empty != "" {
					return false
				}
			}
		}
	}
	interfaces := map[string]bool{}
	for _, row := range rows["Port"] {
		id, err := atom(row["_uuid"], "uuid")
		refs, re := references(row["interfaces"])
		if err != nil || re != nil || !ports[id] || len(refs) != 1 {
			return false
		}
		delete(ports, id)
		for ref := range refs {
			if interfaces[ref] {
				return false
			}
			interfaces[ref] = true
		}
	}
	if len(interfaces) != len(rows["Interface"]) {
		return false
	}
	for _, row := range rows["Interface"] {
		id, err := atom(row["_uuid"], "uuid")
		if err != nil || !interfaces[id] || row["type"] != "" && row["type"] != "system" && row["type"] != "internal" {
			return false
		}
		delete(interfaces, id)
	}
	return len(ports) == 0 && len(interfaces) == 0
}

// Digest reproduces the C snapshot's bounded, explicitly enumerated IDL input
// image. It is not the inventory dependency hash or a full-database witness.
// Rows must come from an independent exact 3.3.9 read; no I/O or fallback occurs.
func Digest(rows map[string][]map[string]any) (string, error) {
	if len(rows) != 4 || len(rows["Open_vSwitch"]) != 1 || len(rows["Bridge"]) != 1 || len(rows["Port"]) > 128 || len(rows["Interface"]) > 128 {
		return "", ErrInvalid
	}
	var registry map[string]map[string]columnType
	if json.Unmarshal(inputTypes, &registry) != nil {
		return "", ErrInvalid
	}
	if !inputGraph(rows, registry) {
		return "", ErrInvalid
	}
	var image strings.Builder
	image.WriteString("ovs-webui.stp-input.v1\n")
	for _, table := range []string{"Open_vSwitch", "Bridge", "Port", "Interface"} {
		columns := registry[table]
		var names []string
		for name := range columns {
			names = append(names, name)
		}
		sort.Strings(names)
		byID := map[string]map[string]any{}
		var ids []string
		for _, row := range rows[table] {
			n := len(columns) + 1
			if table == "Interface" {
				n++
			}
			id, err := atom(row["_uuid"], "uuid")
			if err != nil || byID[id] != nil || len(row) != n {
				return "", ErrInvalid
			}
			byID[id] = row
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			row := byID[id]
			decoded, _ := hex.DecodeString(id)
			fmt.Fprintf(&image, "@%s:%s\n", table, decoded[1:])
			for _, name := range names {
				encoded, err := datum(row[name], columns[name])
				if err != nil {
					return "", ErrInvalid
				}
				fmt.Fprintf(&image, "%s=%s\n", name, encoded)
				if image.Len() > 256<<10 {
					return "", ErrInvalid
				}
			}
			if table == "Interface" {
				ofport, err := datum(row["ofport"], columnType{Key: "integer", Value: "void", Min: 1, Max: float64(1)})
				if err != nil {
					return "", ErrInvalid
				}
				decoded, _ := hex.DecodeString(strings.TrimSuffix(ofport, ":"))
				n, err := strconv.ParseUint(string(decoded[1:]), 10, 16)
				if err != nil || n == 65535 {
					return "", ErrInvalid
				}
				fmt.Fprintf(&image, "ofport=%d\n", n)
			}
		}
	}
	if image.Len() > 256<<10 {
		return "", ErrInvalid
	}
	hash := sha256.Sum256([]byte(image.String()))
	return hex.EncodeToString(hash[:]), nil
}
