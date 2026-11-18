package ovsdb

import (
	"encoding/json"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// Qualify each Bridge-scoped exporter independently. Another protocol or a
// field outside this projection cannot supply a missing native reference.
var telemetryTables = map[string]string{"sflow": "sFlow", "netflow": "NetFlow", "ipfix": "IPFIX"}

func telemetryTable(table string) bool {
	return table == "sFlow" || table == "NetFlow" || table == "IPFIX"
}

type telemetryShape struct {
	atom     string
	min, max int
	lower    json.Number
	upper    json.Number
}

var telemetryFields = map[string]map[string]telemetryShape{
	"sFlow": {
		"agent":    {atom: "string", max: 1},
		"header":   {atom: "integer", max: 1},
		"polling":  {atom: "integer", max: 1},
		"sampling": {atom: "integer", max: 1},
	},
	"NetFlow": {
		"engine_type":         {atom: "integer", max: 1, lower: "0", upper: "255"},
		"engine_id":           {atom: "integer", max: 1, lower: "0", upper: "255"},
		"active_timeout":      {atom: "integer", min: 1, max: 1, lower: "-1"},
		"add_id_to_interface": {atom: "boolean", min: 1, max: 1},
	},
	"IPFIX": {
		"sampling":             {atom: "integer", max: 1, lower: "1", upper: "4294967295"},
		"obs_domain_id":        {atom: "integer", max: 1, lower: "0", upper: "4294967295"},
		"obs_point_id":         {atom: "integer", max: 1, lower: "0", upper: "4294967295"},
		"cache_active_timeout": {atom: "integer", max: 1, lower: "0", upper: "4200"},
		"cache_max_flows":      {atom: "integer", max: 1, lower: "0", upper: "4294967295"},
		"stats_interval":       {atom: "integer", max: 1, lower: "1", upper: "3600"},
		"template_interval":    {atom: "integer", max: 1, lower: "1", upper: "3600"},
	},
}

func telemetryColumnShape(c *native.ColumnSchema, raw any, want telemetryShape) bool {
	if c == nil || c.Ephemeral() || c.TypeObj == nil || c.TypeObj.Key == nil || c.TypeObj.Value != nil ||
		c.TypeObj.Key.Type != want.atom || c.TypeObj.Min() != want.min || c.TypeObj.Max() != want.max {
		return false
	}
	key := raw
	if descriptor, ok := raw.(map[string]any); ok {
		for name := range descriptor {
			switch name {
			case "key", "min", "max":
			default:
				return false
			}
		}
		key = descriptor["key"]
	}
	if want.lower == "" && want.upper == "" {
		return mirrorFreeAtom(key, want.atom)
	}
	atom, ok := key.(map[string]any)
	// Require the documented primitive semantics rather than just the type
	// returned by libovsdb after it has dropped unknown descriptor keys.
	if !ok || atom["type"] != want.atom {
		return false
	}
	expected := 1
	for name, value := range map[string]json.Number{"minInteger": want.lower, "maxInteger": want.upper} {
		if value != "" {
			expected++
			if atom[name] != value {
				return false
			}
		}
	}
	return len(atom) == expected
}

func discoverTelemetry(d *discovered, raw map[string]any) {
	d.public.Telemetry = map[string]bool{}
	d.telemetryColumns = map[string]map[string]bool{"Bridge": {}}
	for protocol, table := range telemetryTables {
		exporter := d.native.Tables[table]
		root, err := d.native.IsRoot(table)
		targetMin := 1
		if table == "IPFIX" {
			targetMin = 0
		}
		if err != nil || root ||
			!mirrorReference(d.native.Tables["Bridge"].Columns[protocol], multicastRawColumnType(raw, "Bridge", protocol), table, "strong", 1) ||
			!telemetryColumnShape(exporter.Columns["targets"], multicastRawColumnType(raw, table, "targets"), telemetryShape{atom: "string", min: targetMin, max: native.Unlimited}) {
			continue
		}
		d.public.Telemetry[protocol] = true
		d.telemetryColumns["Bridge"][protocol] = true
		columns := map[string]bool{"targets": true}
		for name, shape := range telemetryFields[table] {
			if telemetryColumnShape(exporter.Columns[name], multicastRawColumnType(raw, table, name), shape) {
				columns[name] = true
			}
		}
		if table == "IPFIX" && multicastMapConstraint(exporter.Columns["other_config"], multicastRawColumnType(raw, table, "other_config")) {
			columns["other_config"] = true
		}
		d.telemetryColumns[table] = columns
	}
}

func constrainTelemetryField(table, name string, normalized any) any {
	if shape := telemetryFields[table][name]; shape.atom == native.TypeInteger {
		lowest, highest := int64(math.MinInt64), int64(math.MaxInt64)
		if shape.lower != "" {
			lowest, _ = shape.lower.Int64()
		}
		if shape.upper != "" {
			highest, _ = shape.upper.Int64()
		}
		valid := func(v any) bool {
			raw, _ := v.(string)
			number, err := strconv.ParseInt(raw, 10, 64)
			return err == nil && number >= lowest && number <= highest
		}
		if values, ok := normalized.([]any); ok {
			for _, value := range values {
				if !valid(value) {
					return nil
				}
			}
		} else if !valid(normalized) {
			return nil
		}
	}
	if name == "targets" || table == "sFlow" && name == "agent" {
		values, ok := normalized.([]any)
		if !ok || len(values) > 32 {
			return nil
		}
		for _, value := range values {
			text, ok := value.(string)
			if !ok || !safeTelemetryAddress(text, name == "targets") {
				return nil
			}
		}
	}
	if table == "IPFIX" && name == "other_config" {
		options, ok := normalized.(map[string]any)
		if !ok {
			return nil
		}
		out := map[string]any{}
		for _, key := range []string{"enable-input-sampling", "enable-output-sampling", "enable-tunnel-sampling"} {
			if value, present := options[key]; present {
				if value == "true" || value == "false" {
					out[key] = value
				} else {
					out[key] = nil
				}
			}
		}
		return out
	}
	return normalized
}

// Collector targets and the sFlow agent are plain configuration text. Accept
// address or device tokens without trying to resolve hosts or inspect sockets.
func safeTelemetryAddress(value string, target bool) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	host := value
	if target && (strings.Contains(value, ":") && !strings.HasSuffix(value, "]")) {
		var port string
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil || port == "" || strings.IndexFunc(port, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return false
		}
		if _, err = strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
	} else if target && strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		host = value[1 : len(value)-1]
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == "" || telemetryToken(addr.Zone())
	}
	return telemetryToken(host)
}

func telemetryToken(value string) bool {
	return value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
	}) < 0
}
