package inventory

import (
	"maps"
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

var telemetryProtocols = []string{"sflow", "netflow", "ipfix"}

func (s *Service) SetLocalTelemetryBindings(grants []string) error {
	if len(grants) > 128 {
		return apitypes.Fail(422, "INVALID_TELEMETRY_AUTHORITY")
	}
	allowed := make(map[string]bool, len(grants))
	for _, grant := range grants {
		bridge, protocol, found := strings.Cut(grant, ":")
		if !found || !apitypes.ManagementID(bridge) || candidate.TelemetryTable(protocol) == "" || allowed[grant] {
			return apitypes.Fail(422, "INVALID_TELEMETRY_AUTHORITY")
		}
		allowed[grant] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localTelemetry = allowed
	return nil
}

func telemetryBinding(v *view, b Binding) candidate.Binding {
	if b.Table != "Bridge" || b.State != "active" {
		return candidate.Binding{}
	}
	return candidate.Binding{Table: b.Table, ManagementID: b.ManagementID, OVSUUID: b.UUID, Generation: v.decision.Generation}
}

func telemetryReference(value any) (string, bool) {
	refs, ok := value.([]any)
	if !ok || len(refs) > 1 {
		return "", false
	}
	if len(refs) == 0 {
		return "", true
	}
	id, ok := refs[0].(string)
	return id, ok && apitypes.UUID(id)
}

func telemetryWriteTarget(v *view, b candidate.Binding, protocol string, grant bool) candidate.TelemetryTarget {
	target := candidate.TelemetryTarget{Binding: b, Reason: "TELEMETRY_WRITE_AUTHORITY_REQUIRED"}
	if !grant || b.Table != "Bridge" {
		return target
	}
	table := candidate.TelemetryTable(protocol)
	if table == "" || !v.observation.Schema.TelemetryWrite[protocol] {
		target.Reason = "TELEMETRY_WRITE_SCHEMA_UNSUPPORTED"
		return target
	}
	bridge := v.observation.Rows["Bridge"][b.OVSUUID]
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	rootConfig, rootKnown := root.Configuration["other_config"].(map[string]any)
	datapath, knownType := bridge.Configuration["datapath_type"].(string)
	if bridge.UUID != b.OVSUUID || !apitypes.UUID(root.UUID) || !slices.Contains(refs(root.Values["bridges"]), b.OVSUUID) ||
		!rootKnown || rootConfig["hw-offload"] != nil && rootConfig["hw-offload"] != "false" ||
		!knownType || !slices.Contains([]string{"", "system", "netdev", "dummy"}, datapath) {
		target.Reason = "TELEMETRY_BRIDGE_UNSUPPORTED"
		return target
	}
	if !qosEmptySet(bridge.Configuration["controller"]) || !qosLocalLabels(bridge) || !qosLocalLabels(root) {
		target.Reason = "EXTERNALLY_CONTROLLED"
		return target
	}
	id, known := telemetryReference(bridge.Values[protocol])
	if !known {
		target.Reason = "TELEMETRY_CONFIGURATION_UNKNOWN"
		return target
	}
	if id != "" {
		row, found := v.observation.Rows[table][id]
		if !found || !qosLocalLabels(row) {
			target.Reason = "TELEMETRY_CONFIGURATION_UNKNOWN"
			if found {
				target.Reason = "EXTERNALLY_CONTROLLED"
			}
			return target
		}
		for _, other := range v.observation.Rows["Bridge"] {
			if other.UUID == b.OVSUUID {
				continue
			}
			otherID, known := telemetryReference(other.Values[protocol])
			if !known || otherID == id {
				target.Reason = "TELEMETRY_REFERENCE_SHARED"
				return target
			}
		}
		if protocol == "ipfix" {
			for _, collector := range v.observation.Rows["Flow_Sample_Collector_Set"] {
				ref, known := telemetryReference(collector.Configuration["ipfix"])
				if !known || ref == id {
					target.Reason = "TELEMETRY_REFERENCE_SHARED"
					return target
				}
			}
		}
		var ok bool
		target.Image, ok = telemetryWriteImage(row, protocol)
		if !ok {
			target.Reason = "TELEMETRY_CONFIGURATION_UNKNOWN"
			return target
		}
	}
	labels, _ := bridge.Configuration["external_ids"].(map[string]any)
	labels = maps.Clone(labels)
	delete(labels, candidate.TelemetryMarker)
	target.Dependency = Digest([]any{root.UUID, b, protocol, datapath, labels, rootConfig, root.Configuration["external_ids"]})
	target.Reason = ""
	return target
}

func telemetryWriteImage(row Row, protocol string) (*candidate.TelemetryImage, bool) {
	for _, field := range row.Configuration {
		if field == nil {
			return nil, false
		}
	}
	targets, ok := telemetryTargets(row.Configuration["targets"])
	if !ok || len(targets) > telemetryTargetLimit {
		return nil, false
	}
	settings := map[string]*int64{}
	for _, spec := range candidate.TelemetrySettings(protocol) {
		field := telemetryField(row.Configuration[spec.Key], spec.Key, true)
		switch field.State {
		case "unset":
			if !spec.Optional {
				return nil, false
			}
			settings[spec.Key] = nil
		case "known":
			n, ok := field.Value.(int64)
			if !ok || n < spec.Min || n > spec.Max {
				return nil, false
			}
			settings[spec.Key] = &n
		default:
			return nil, false
		}
	}
	return &candidate.TelemetryImage{UUID: row.UUID, Targets: targets, Settings: settings, Digest: Digest(row.Configuration)}, true
}

func projectTelemetryWrites(v *view, allow map[string]bool, s *candidate.Snapshot, bindings []candidate.Binding) {
	if len(allow) == 0 {
		return
	}
	s.Telemetry = map[string]candidate.TelemetryTarget{}
	for _, want := range bindings {
		if want.Table != "Bridge" {
			continue
		}
		b := telemetryBinding(v, v.decision.Bindings[Key("Bridge", want.OVSUUID)])
		if b != want {
			continue
		}
		for _, protocol := range telemetryProtocols {
			key := candidate.TelemetryKey(b.ManagementID, protocol)
			if allow[key] {
				s.Telemetry[key] = telemetryWriteTarget(v, b, protocol, true)
			}
		}
	}
	if len(s.Telemetry) != 0 {
		s.Revision = Digest([]any{s.Revision, s.Telemetry})
	}
}

func telemetryWriteStatus(v *view, binding Binding, fresh string, allow map[string]bool, c authn.Claims) map[string]any {
	if !slices.Contains(c.Capabilities, candidate.TelemetryCapability) || !slices.Contains(c.Capabilities, "workspace.write") {
		return nil
	}
	b := telemetryBinding(v, binding)
	protocols := map[string]any{}
	for _, protocol := range telemetryProtocols {
		available := false
		reason := "OVS_ASSOCIATION_STALE"
		var targets []string
		var revision any
		settings := map[string]*int64{}
		id, configured := telemetryReference(v.observation.Rows["Bridge"][b.OVSUUID].Values[protocol])
		if fresh == "fresh" {
			at := telemetryWriteTarget(v, b, protocol, allow[candidate.TelemetryKey(b.ManagementID, protocol)])
			reason = at.Reason
			if reason == "" {
				available, reason = true, "TELEMETRY_WRITE_AVAILABLE"
				revision = Digest([]any{at.Dependency, at.Image})
				if at.Image != nil {
					targets, settings = at.Image.Targets, at.Image.Settings
				}
			}
		}
		protocols[protocol] = map[string]any{"available": available, "reason": reason, "targets": targets,
			"settings": settings, "configured": configured && id != "", "revision": revision}
	}
	return map[string]any{"object": b, "protocols": protocols}
}
