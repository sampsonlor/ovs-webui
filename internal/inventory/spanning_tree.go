package inventory

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

var SpanningTreeBridgeKeys = []string{
	"stp-system-id", "stp-priority", "stp-hello-time", "stp-max-age", "stp-forward-delay",
	"rstp-address", "rstp-priority", "rstp-ageing-time", "rstp-force-protocol-version",
	"rstp-max-age", "rstp-forward-delay", "rstp-transmit-hold-count",
}
var spanningTreePortKeys = []string{
	"stp-enable", "stp-port-num", "stp-port-priority", "stp-path-cost",
	"rstp-enable", "rstp-port-num", "rstp-port-priority", "rstp-path-cost",
	"rstp-port-admin-edge", "rstp-port-auto-edge", "rstp-port-mcheck",
}

func SpanningTreeRuntimeKeys(table, column string) []string {
	if table == "Bridge" {
		if column == "status" {
			return []string{"stp_bridge_id", "stp_designated_root", "stp_root_path_cost"}
		}
		return []string{"rstp_bridge_id", "rstp_root_id", "rstp_root_path_cost", "rstp_designated_id", "rstp_designated_port_id", "rstp_bridge_port_id"}
	}
	if column == "status" {
		return []string{"stp_port_id", "stp_state", "stp_sec_in_state", "stp_role"}
	}
	return []string{"rstp_port_id", "rstp_port_role", "rstp_port_state", "rstp_designated_bridge_id", "rstp_designated_port_id", "rstp_designated_path_cost"}
}

func spanningTreeField(v *view, row Row, table, column, key, fresh string, config bool) map[string]any {
	var c Column
	present := false
	for _, t := range v.observation.Schema.Tables {
		if t.Name == table {
			for _, col := range t.Columns {
				if col.Name == column {
					c, present = col, true
				}
			}
		}
	}
	value, observed := row.Values[column]
	availability, reason := "known", ""
	authority := "ovsdb-configuration"
	runtime := operational(table, column)
	if runtime {
		authority = "ovs-vswitchd-observation"
	}
	switch {
	case !config && !runtime:
		availability, reason = "withheld", "CONFIGURATION_WITHHELD"
	case !present || !c.Monitored:
		availability, reason = "unsupported", "SCHEMA_COLUMN_UNAVAILABLE"
	case !c.SpanningTreeCompatible:
		availability, reason = "unsupported", "SCHEMA_TYPE_UNSUPPORTED"
	case !observed:
		availability, reason = "unknown", "NATIVE_VALUE_UNAVAILABLE"
	default:
		if key == "" {
			if _, ok := value.(bool); !ok {
				availability, reason = "unknown", "NATIVE_VALUE_INVALID"
			}
		} else if m, ok := value.(map[string]any); ok {
			value, observed = m[key]
			if !observed {
				availability, reason = "unset", "NATIVE_DEFAULT_NOT_MATERIALIZED"
				if runtime {
					availability, reason = "unknown", "RUNTIME_NOT_REPORTED"
				}
			} else if text, ok := value.(string); !ok || len(text) > 128 {
				availability, reason = "unknown", "NATIVE_VALUE_INVALID"
			} else if allowed := spanningTreeStateValues(key); allowed != nil && !slices.Contains(allowed, text) {
				availability, reason = "unknown", "RUNTIME_VALUE_UNRECOGNIZED"
			}
		} else {
			availability, reason = "unknown", "NATIVE_VALUE_INVALID"
		}
	}
	if availability != "known" {
		value = nil
	}
	return map[string]any{"value": value, "availability": availability, "reason": reason,
		"source": source(v, fresh, authority), "schema_mutable": c.Mutable, "ownership": "unknown", "editable": false}
}

func spanningTreeStateValues(key string) []string {
	switch key {
	case "stp_state":
		return []string{"blocking", "disabled", "forwarding", "learning", "listening"}
	case "stp_role":
		return []string{"alternate", "designated", "root"}
	case "rstp_port_state":
		return []string{"Disabled", "Discarding", "Forwarding", "Learning"}
	case "rstp_port_role":
		return []string{"Alternate", "Backup", "Designated", "Disabled", "Root"}
	}
	return nil
}

func spanningTreeFields(v *view, row Row, table, fresh string, config bool) (map[string]any, map[string]any) {
	configuration, runtime := map[string]any{}, map[string]any{}
	keys := spanningTreePortKeys
	if table == "Bridge" {
		keys = SpanningTreeBridgeKeys
		for _, name := range []string{"stp_enable", "rstp_enable"} {
			configuration[name] = spanningTreeField(v, row, table, name, "", fresh, config)
		}
	}
	for _, key := range keys {
		configuration[key] = spanningTreeField(v, row, table, "other_config", key, fresh, config)
	}
	for _, column := range []string{"status", "rstp_status"} {
		for _, key := range SpanningTreeRuntimeKeys(table, column) {
			runtime[key] = spanningTreeField(v, row, table, column, key, fresh, config)
		}
	}
	return configuration, runtime
}

func spanningTreeOwnership(v *view, row Row) string {
	if externalControl(row.Values["external_ids"]) || len(refs(row.Values["controller"])) != 0 {
		return "externally-controlled"
	}
	for _, root := range v.observation.Rows["Open_vSwitch"] {
		if externalControl(root.Values["external_ids"]) {
			return "externally-controlled"
		}
	}
	return "unknown"
}

func spanningTreeBridge(v *view, row Row, fresh string, config bool) map[string]any {
	configuration, runtime := spanningTreeFields(v, row, "Bridge", fresh, config)
	protocol, availability, reason := "unknown", "unknown", "NATIVE_VALUE_UNAVAILABLE"
	stp, rstp := configuration["stp_enable"].(map[string]any), configuration["rstp_enable"].(map[string]any)
	if !config {
		availability, reason = "withheld", "CONFIGURATION_WITHHELD"
	} else if stp["availability"] == "known" && rstp["availability"] == "known" {
		protocol, availability, reason = "disabled", "known", ""
		if stp["value"] == true {
			protocol = "stp"
		}
		if rstp["value"] == true {
			protocol = "rstp"
		}
		if stp["value"] == true && rstp["value"] == true {
			protocol, availability, reason = "invalid-both-enabled", "invalid", "STP_RSTP_MUTUALLY_EXCLUSIVE"
		}
	} else if stp["availability"] == "unsupported" || rstp["availability"] == "unsupported" {
		availability, reason = "unsupported", "SPANNING_TREE_SCHEMA_UNSUPPORTED"
	}
	ownership := "withheld"
	if config {
		ownership = spanningTreeOwnership(v, row)
	}
	return map[string]any{"protocol": protocol, "availability": availability, "reason": reason,
		"configuration": configuration, "runtime": runtime, "source": source(v, fresh, "ovsdb-configuration"),
		"ownership": ownership, "editable": false, "write_reason": "SPANNING_TREE_WRITE_GATE_PENDING"}
}

// Participation is a configuration assessment. Reported state/role is kept
// independently; neither configured enablement nor exclusion proves forwarding.
func spanningTreeParticipation(v *view, row, bridge Row, fresh string, config bool, protocol string) map[string]any {
	value, availability, reason := any(nil), "unknown", "PORT_PARTICIPATION_UNVERIFIED"
	switch {
	case !config:
		availability, reason = "withheld", "CONFIGURATION_WITHHELD"
	case fresh != "fresh":
		reason = "SPANNING_TREE_OBSERVATION_STALE"
	case len(refs(row.Values["interfaces"])) > 1:
		value, availability, reason = "excluded-bond", "known", "NATIVE_BOND_EXCLUDED"
	default:
		ids := refs(row.Values["interfaces"])
		if len(ids) == 1 {
			iface := v.observation.Rows["Interface"][ids[0]]
			typ, known := iface.Values["type"].(string)
			if known && typ == "internal" {
				value, availability, reason = "excluded-internal", "known", "NATIVE_INTERNAL_EXCLUDED"
			} else if !known || !slices.Contains([]string{"", "system", "dummy"}, typ) {
				reason = "INTERFACE_TYPE_UNVERIFIED"
			} else if mirrors, ok := bridge.Values["mirrors"].([]any); !ok || len(mirrors) != 0 {
				// The current monitor does not resolve Mirror.output_port. Never
				// infer that an arbitrary Port is or is not a mirror output.
				reason = "MIRROR_OUTPUT_NOT_OBSERVED"
			} else {
				f := spanningTreeField(v, row, "Port", "other_config", protocol+"-enable", fresh, config)
				if f["availability"] == "unset" {
					value, availability, reason = "enabled-by-default", "known", "NATIVE_DEFAULT"
				} else if f["availability"] == "known" {
					switch f["value"] {
					case "true":
						value, availability, reason = "enabled-explicitly", "known", ""
					case "false":
						value, availability, reason = "disabled-on-port", "known", ""
					default:
						reason = "NATIVE_VALUE_INVALID"
					}
				} else {
					availability, reason = f["availability"].(string), f["reason"].(string)
				}
			}
		}
	}
	return map[string]any{"value": value, "availability": availability, "reason": reason, "source": source(v, fresh, "ovsdb-configuration"), "schema_mutable": false, "ownership": "unknown", "editable": false}
}

func spanningTreePort(v *view, row, bridge Row, fresh string, config bool) map[string]any {
	configuration, runtime := spanningTreeFields(v, row, "Port", fresh, config)
	ownership := "withheld"
	if config {
		ownership = spanningTreeOwnership(v, bridge)
		if externalControl(row.Values["external_ids"]) {
			ownership = "externally-controlled"
		}
		for _, id := range refs(row.Values["interfaces"]) {
			if externalControl(v.observation.Rows["Interface"][id].Values["external_ids"]) {
				ownership = "externally-controlled"
			}
		}
	}
	return map[string]any{"configuration": configuration, "runtime": runtime,
		"stp_participation":  spanningTreeParticipation(v, row, bridge, fresh, config, "stp"),
		"rstp_participation": spanningTreeParticipation(v, row, bridge, fresh, config, "rstp"),
		"source":             source(v, fresh, "ovsdb-configuration"), "ownership": ownership, "editable": false}
}

// Keep shared Port inventory compact. The independent spanning-tree detail
// carries advanced parameters; common fields still use the same projection.
func spanningTreePortSummary(v *view, row, bridge Row, fresh string, config bool) map[string]any {
	out := spanningTreePort(v, row, bridge, fresh, config)
	for group, names := range map[string][]string{
		"configuration": {"stp-enable", "rstp-enable"},
		"runtime":       {"stp_state", "stp_role", "rstp_port_state", "rstp_port_role"},
	} {
		fields := out[group].(map[string]any)
		for name := range fields {
			if !slices.Contains(names, name) {
				delete(fields, name)
			}
		}
	}
	return out
}

func spanningTreeResource(v *view, b Binding, fresh string, config, detail bool) (map[string]any, error) {
	row, exists := v.observation.Rows["Bridge"][b.UUID]
	if !exists || b.Table != "Bridge" || b.State != "active" {
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	out := base(b.ManagementID, "spanning_tree", fresh, v)
	out["management_id"], out["ovs_uuid"], out["instance_generation"] = b.ManagementID, b.UUID, v.decision.Generation
	out["snapshot_id"], out["name"], out["bridge_ref"] = v.id, row.Values["name"], ref(v, "Bridge", b.UUID)
	bridge := spanningTreeBridge(v, row, fresh, config)
	out["spanning_tree"] = bridge
	values := map[string]any{}
	for name, field := range bridge["configuration"].(map[string]any) {
		f := field.(map[string]any)
		values[name] = []any{f["value"], f["availability"]}
	}
	out["config_revision"] = Digest(values)
	out["ports"], out["ports_truncated"] = []map[string]any{}, false
	out["port_count"] = len(refs(row.Values["ports"]))
	out["mode"], out["editable"], out["allowed_operations"] = "observe", false, []string{}
	out["write_reason"] = "SPANNING_TREE_WRITE_GATE_PENDING"
	if detail {
		ports := []Row{}
		for _, id := range refs(row.Values["ports"]) {
			p, ok := v.observation.Rows["Port"][id]
			binding := v.decision.Bindings[Key("Port", id)]
			if !ok || binding.State != "active" {
				return nil, apitypes.Fail(503, "INVENTORY_RELATION_UNKNOWN")
			}
			ports = append(ports, p)
		}
		slices.SortFunc(ports, func(a, b Row) int {
			if n := naturalNameCompare(textValue(a.Values["name"]), textValue(b.Values["name"])); n != 0 {
				return n
			}
			return strings.Compare(a.UUID, b.UUID)
		})
		items := []map[string]any{}
		for _, p := range ports {
			item := map[string]any{"name": p.Values["name"], "port_ref": ref(v, "Port", p.UUID), "spanning_tree": spanningTreePort(v, p, row, fresh, config)}
			out["ports"] = append(items, item)
			body, _ := json.Marshal(out)
			if len(items) >= 128 || len(body) > 52<<10 {
				out["ports"], out["ports_truncated"] = items, true
				break
			}
			items = append(items, item)
		}
	}
	return out, nil
}
