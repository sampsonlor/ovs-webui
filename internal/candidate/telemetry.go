package candidate

import (
	"maps"
	"net/netip"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	TelemetrySet        = "telemetry.exporter.set"
	TelemetryClear      = "telemetry.exporter.clear"
	TelemetryCapability = "ovs.telemetry.write"
	TelemetryMarker     = "ovs-webui-telemetry-execution"
)

type TelemetrySettingSpec struct {
	Key      string
	Min, Max int64
	Optional bool
}

func TelemetrySettings(protocol string) []TelemetrySettingSpec {
	switch protocol {
	case "sflow":
		return []TelemetrySettingSpec{{"sampling", 1, 4294967295, true}, {"polling", 0, 3600, true}}
	case "netflow":
		return []TelemetrySettingSpec{{"active_timeout", -1, 86400, false}}
	case "ipfix":
		return []TelemetrySettingSpec{{"sampling", 1, 4294967295, true}, {"obs_domain_id", 0, 4294967295, true}, {"obs_point_id", 0, 4294967295, true}}
	default:
		return nil
	}
}

func TelemetryTable(protocol string) string {
	switch protocol {
	case "sflow":
		return "sFlow"
	case "netflow":
		return "NetFlow"
	case "ipfix":
		return "IPFIX"
	default:
		return ""
	}
}

type TelemetryImage struct {
	UUID     string            `json:"uuid"`
	Targets  []string          `json:"targets"`
	Settings map[string]*int64 `json:"settings"`
	Digest   string            `json:"digest,omitempty"`
}

type TelemetryChange struct {
	Protocol    string          `json:"protocol"`
	Before      *TelemetryImage `json:"before"`
	After       *TelemetryImage `json:"after"`
	RestoreUUID string          `json:"restore_uuid,omitempty"`
}

type TelemetryTarget struct {
	Binding    Binding
	Image      *TelemetryImage
	Dependency string
	Reason     string
}

func TelemetryKey(bridgeID, protocol string) string { return bridgeID + ":" + protocol }

func (s Snapshot) TelemetryTarget(bridge Binding, protocol string) TelemetryTarget {
	return s.Telemetry[TelemetryKey(bridge.ManagementID, protocol)]
}

func IsTelemetryOperation(operation string) bool {
	return operation == TelemetrySet || operation == TelemetryClear
}

func ValidTelemetryTargets(input []string) ([]string, bool) {
	if len(input) < 1 || len(input) > 16 {
		return nil, false
	}
	targets := make([]string, 0, len(input))
	for _, value := range input {
		endpoint, err := netip.ParseAddrPort(value)
		if err != nil {
			return nil, false
		}
		addr := endpoint.Addr().Unmap()
		if endpoint.Port() == 0 || addr.Zone() != "" || !addr.IsGlobalUnicast() && !addr.IsLoopback() {
			return nil, false
		}
		targets = append(targets, netip.AddrPortFrom(addr, endpoint.Port()).String())
	}
	slices.Sort(targets)
	for i := 1; i < len(targets); i++ {
		if targets[i] == targets[i-1] {
			return nil, false
		}
	}
	return targets, true
}

func telemetryProblem(i StoredIntent, s Snapshot) string {
	c := i.Telemetry
	if !IsTelemetryOperation(i.Operation) || c == nil || i.Object.Table != "Bridge" || TelemetryTable(c.Protocol) == "" {
		return "INVALID_TELEMETRY_EXPORTER"
	}
	if i.Object.Generation != s.Generation {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	if i.Schema != s.Schema {
		return "TELEMETRY_WRITE_SCHEMA_UNSUPPORTED"
	}
	target := s.TelemetryTarget(i.Object, c.Protocol)
	switch {
	case target.Binding != i.Object:
		return "TELEMETRY_WRITE_AUTHORITY_REQUIRED"
	case target.Reason != "":
		return target.Reason
	case i.Dependency == "" || target.Dependency != i.Dependency:
		return "TELEMETRY_BRIDGE_DEPENDENCY_CHANGED"
	case Digest(c.Before) != Digest(target.Image):
		return "TELEMETRY_CONFIGURATION_CHANGED"
	}
	return ""
}

func stageTelemetry(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) != 0 {
		return c, apitypes.Fail(409, "TELEMETRY_RESTAGE_REQUIRED")
	}
	in := cmd.Intents[0]
	if !IsTelemetryOperation(in.Operation) || !apitypes.ManagementID(in.ID) || in.Object.Table != "Bridge" ||
		TelemetryTable(in.TelemetryProtocol) == "" || in.Name != "" || in.MTURequest != 0 || in.VLANID != 0 ||
		in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_TELEMETRY_EXPORTER")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	target := s.TelemetryTarget(in.Object, in.TelemetryProtocol)
	change := TelemetryChange{Protocol: in.TelemetryProtocol, Before: target.Image}
	i := StoredIntent{
		ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema,
		Dependency: target.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), Telemetry: &change,
	}
	if reason := telemetryProblem(i, s); reason != "" {
		return c, apitypes.Fail(409, reason)
	}
	if in.Operation == TelemetryClear {
		if change.Before == nil || in.TelemetryTargets != nil || in.TelemetrySettings != nil {
			return c, apitypes.Fail(422, "INVALID_TELEMETRY_CLEAR")
		}
		change.RestoreUUID = repository.NewID()
	} else {
		targets, valid := ValidTelemetryTargets(in.TelemetryTargets)
		if !valid {
			return c, apitypes.Fail(422, "INVALID_TELEMETRY_TARGETS")
		}
		after := &TelemetryImage{Targets: targets, Settings: map[string]*int64{}}
		if change.Before == nil {
			after.UUID = repository.NewID()
			for _, spec := range TelemetrySettings(change.Protocol) {
				after.Settings[spec.Key] = nil
				if !spec.Optional {
					after.Settings[spec.Key] = new(int64)
				}
			}
		} else {
			after.UUID = change.Before.UUID
			after.Settings = maps.Clone(change.Before.Settings)
		}
		for key, value := range in.TelemetrySettings {
			settings := TelemetrySettings(change.Protocol)
			index := slices.IndexFunc(settings, func(spec TelemetrySettingSpec) bool { return spec.Key == key })
			if index < 0 {
				return c, apitypes.Fail(422, "INVALID_TELEMETRY_SETTINGS")
			}
			spec := settings[index]
			if (value == nil && !spec.Optional) || (value != nil && (*value < spec.Min || *value > spec.Max)) {
				return c, apitypes.Fail(422, "INVALID_TELEMETRY_SETTINGS")
			}
			after.Settings[key] = value
		}
		if change.Before != nil && slices.Equal(change.Before.Targets, after.Targets) && Digest(change.Before.Settings) == Digest(after.Settings) {
			return c, apitypes.Fail(422, "TELEMETRY_CONFIGURATION_UNCHANGED")
		}
		change.After = after
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func telemetryDiff(i StoredIntent, s Snapshot) []Diff {
	c := i.Telemetry
	if c == nil {
		return nil
	}
	target := s.TelemetryTarget(i.Object, c.Protocol)
	conflict := telemetryProblem(i, s) != ""
	field := func(name string, before, after, current any) Diff {
		return Diff{Object: i.Object, Field: name, Before: before, After: after, Current: current,
			Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: conflict}
	}
	state := func(image *TelemetryImage) string {
		if image == nil {
			return "unconfigured"
		}
		return "configured"
	}
	targets := func(image *TelemetryImage) any {
		if image == nil {
			return nil
		}
		return image.Targets
	}
	setting := func(image *TelemetryImage, key string) any {
		if image == nil || image.Settings[key] == nil {
			return nil
		}
		return *image.Settings[key]
	}
	var current any
	if target.Binding == i.Object && target.Reason == "" {
		current = state(target.Image)
	}
	out := []Diff{
		field(c.Protocol+" exporter", state(c.Before), state(c.After), current),
		field("Collector targets", targets(c.Before), targets(c.After), targets(target.Image)),
	}
	for _, spec := range TelemetrySettings(c.Protocol) {
		out = append(out, field(spec.Key, setting(c.Before, spec.Key), setting(c.After, spec.Key), setting(target.Image, spec.Key)))
	}
	return out
}
