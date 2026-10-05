package inventory

import "strconv"

var InterfaceConfigurationColumns = []string{
	"ofport_request", "ingress_policing_rate", "ingress_policing_burst",
	"ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst",
}

// Preserve exact native integers and optional emptiness. A configuration request
// cannot prove port allocation or kernel policing enforcement.
func interfaceConfigurationValue(name string, value any) bool {
	if name == "ofport_request" {
		set, ok := value.([]any)
		if !ok || len(set) > 1 {
			return false
		}
		if len(set) == 0 {
			return true
		}
		text, ok := set[0].(string)
		n, err := strconv.ParseInt(text, 10, 64)
		return ok && err == nil && n >= 1 && n <= 65279 && strconv.FormatInt(n, 10) == text
	}
	text, ok := value.(string)
	n, err := strconv.ParseInt(text, 10, 64)
	return ok && err == nil && n >= 0 && strconv.FormatInt(n, 10) == text
}

func projectInterfaceConfiguration(v *view, row Row, fields, values map[string]any, fresh string, config bool) {
	columns := map[string]Column{}
	for _, table := range v.observation.Schema.Tables {
		if table.Name == "Interface" {
			for _, column := range table.Columns {
				columns[column.Name] = column
			}
		}
	}
	for _, name := range InterfaceConfigurationColumns {
		column, present := columns[name]
		value, observed := row.Values[name]
		state, reason := "known", ""
		switch {
		case !config:
			state, reason = "withheld", "CONFIGURATION_WITHHELD"
		case !present:
			state, reason = "unsupported", "SCHEMA_COLUMN_UNAVAILABLE"
		case !column.InterfaceConfigCompatible:
			state, reason = "unsupported", "SCHEMA_TYPE_UNSUPPORTED"
		case !column.Monitored:
			state, reason = "unsupported", "SCHEMA_COLUMN_UNAVAILABLE"
		case !observed:
			state, reason = "unknown", "NATIVE_VALUE_UNAVAILABLE"
		case !interfaceConfigurationValue(name, value):
			state, reason = "unknown", "NATIVE_VALUE_INVALID"
		}
		if state != "known" {
			value = nil
		}
		fields[name] = map[string]any{"value": value, "availability": state, "reason": reason,
			"source": source(v, fresh, "ovsdb-configuration"), "schema_mutable": column.Mutable,
			"ownership": "unknown", "editable": false}
		values[name] = value
	}
}
