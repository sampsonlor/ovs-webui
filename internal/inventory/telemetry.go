package inventory

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const telemetryTargetLimit = 16

// Targets and the sFlow agent are labels from native configuration. Allow
// endpoint and interface syntax without publishing arbitrary native text.
var telemetryLabel = regexp.MustCompile(`^[a-zA-Z0-9._:\[\]%-]+$`)

type telemetrySetting struct {
	State string `json:"state"`
	Value any    `json:"value"`
}

type telemetryExporter struct {
	State        string                      `json:"state"`
	Reason       string                      `json:"reason"`
	NativeUUID   *string                     `json:"native_uuid"`
	TargetCount  *int                        `json:"target_count"`
	Targets      []string                    `json:"targets"`
	TargetsState string                      `json:"targets_state"`
	Settings     map[string]telemetrySetting `json:"settings"`
}

func telemetryResource(v *view, b Binding, fresh string, detail bool) (map[string]any, error) {
	bridge, found := v.observation.Rows["Bridge"][b.UUID]
	if b.State != "active" || !found {
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	out := base(b.ManagementID, "telemetry", fresh, v)
	out["source"] = source(v, fresh, "ovsdb-configuration")
	out["name"] = textValue(bridge.Values["name"])
	out["bridge_ref"] = apitypes.Ref{Kind: "bridge", ID: b.ManagementID}
	out["instance_generation"] = v.decision.Generation
	out["native_telemetry"] = map[string]any{
		"delivery": "unverified",
		"exporters": map[string]telemetryExporter{
			"sflow":   telemetryProjection(v, bridge, detail, "sflow", "sFlow", []string{"sampling", "polling", "header", "agent"}),
			"netflow": telemetryProjection(v, bridge, detail, "netflow", "NetFlow", []string{"engine_type", "engine_id", "active_timeout", "add_id_to_interface"}),
			"ipfix":   telemetryProjection(v, bridge, detail, "ipfix", "IPFIX", []string{"sampling", "obs_domain_id", "obs_point_id", "cache_active_timeout", "cache_max_flows", "stats_interval", "template_interval"}),
		},
	}
	return out, nil
}

func telemetryProjection(v *view, bridge Row, detail bool, protocol, table string, fields []string) telemetryExporter {
	out := telemetryExporter{State: "unsupported", Reason: "TELEMETRY_SCHEMA_UNSUPPORTED",
		TargetsState: "not-projected", Settings: map[string]telemetrySetting{}}
	if !v.observation.Schema.Telemetry[protocol] {
		return out
	}
	native, ok := bridge.Values[protocol].([]any)
	switch {
	case !ok || len(native) > 1:
		out.State, out.Reason = "unknown", "TELEMETRY_REFERENCE_UNKNOWN"
		return out
	case len(native) == 0:
		out.State, out.Reason = "unconfigured", "TELEMETRY_REFERENCE_UNSET"
		return out
	}
	id, ok := native[0].(string)
	if !ok || !apitypes.UUID(id) {
		out.State, out.Reason = "unknown", "TELEMETRY_REFERENCE_UNKNOWN"
		return out
	}
	row, ok := v.observation.Rows[table][id]
	if !ok || row.UUID != id {
		out.State, out.Reason = "unknown", "TELEMETRY_EXPORTER_ROW_UNAVAILABLE"
		return out
	}
	out.State, out.Reason = "configured", "TELEMETRY_REFERENCE_CONFIGURED"
	out.NativeUUID = &id
	out.TargetsState = "unknown"
	if targets, valid := telemetryTargets(row.Values["targets"]); valid {
		n := len(targets)
		out.TargetCount = &n
		out.TargetsState = "not-projected"
		if detail {
			out.TargetsState = "known"
			if n > telemetryTargetLimit {
				out.TargetsState = "limited"
				targets = targets[:telemetryTargetLimit]
			}
			out.Targets = targets
		}
	}
	if !detail {
		return out
	}
	compatible := map[string]bool{}
	for _, t := range v.observation.Schema.Tables {
		if t.Name == table {
			for _, col := range t.Columns {
				compatible[col.Name] = col.Monitored && col.TelemetryCompatible
			}
			break
		}
	}
	for _, key := range fields {
		out.Settings[key] = telemetryField(row.Values[key], key, compatible[key])
	}
	if protocol == "ipfix" {
		config, available := row.Values["other_config"].(map[string]any)
		for _, key := range []string{"enable-tunnel-sampling", "enable-input-sampling", "enable-output-sampling"} {
			switch value, set := config[key]; {
			case !compatible["other_config"]:
				out.Settings[key] = telemetrySetting{State: "unsupported"}
			case !available:
				out.Settings[key] = telemetrySetting{State: "unknown"}
			case !set:
				out.Settings[key] = telemetrySetting{State: "unset"}
			case value == true || value == "true":
				out.Settings[key] = telemetrySetting{State: "known", Value: true}
			case value == false || value == "false":
				out.Settings[key] = telemetrySetting{State: "known", Value: false}
			default:
				out.Settings[key] = telemetrySetting{State: "unknown"}
			}
		}
	}
	return out
}

func telemetryTargets(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		target, ok := item.(string)
		if !ok || len(target) > 256 || !telemetryLabel.MatchString(target) {
			return nil, false
		}
		out = append(out, target)
	}
	slices.Sort(out)
	if len(slices.Compact(slices.Clone(out))) != len(out) {
		return nil, false
	}
	return out, true
}

func telemetryField(value any, key string, compatible bool) telemetrySetting {
	if !compatible {
		return telemetrySetting{State: "unsupported"}
	}
	if set, optional := value.([]any); optional {
		switch len(set) {
		case 0:
			return telemetrySetting{State: "unset"}
		case 1:
			value = set[0]
		default:
			return telemetrySetting{State: "unknown"}
		}
	}
	if key == "add_id_to_interface" {
		if flag, ok := value.(bool); ok {
			return telemetrySetting{State: "known", Value: flag}
		}
		return telemetrySetting{State: "unknown"}
	}
	text, ok := value.(string)
	if number, isNumber := value.(json.Number); isNumber {
		text, ok = string(number), true
	}
	if !ok {
		return telemetrySetting{State: "unknown"}
	}
	if key == "agent" {
		if len(text) <= 128 && telemetryLabel.MatchString(text) {
			return telemetrySetting{State: "known", Value: text}
		}
		return telemetrySetting{State: "unknown"}
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < -1 || n > 1<<53-1 {
		return telemetrySetting{State: "unknown"}
	}
	return telemetrySetting{State: "known", Value: n}
}
