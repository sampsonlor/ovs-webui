package inventory

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
)

func mirrorObservation(v *view, b Binding, fresh string, claims authn.Claims, t candidate.TopologySnapshot) map[string]any {
	out := map[string]any{"configuration": nil, "configuration_availability": "unknown", "reason": "MIRROR_NATIVE_IMAGE_UNKNOWN", "bridge_ref": nil, "ports": nil, "ports_truncated": false, "port_count": nil, "source": source(v, fresh, "ovsdb-configuration"), "execution": map[string]any{"available": false, "reason": "MIRROR_NATIVE_IMAGE_UNKNOWN", "capability": candidate.MirrorCapability}}
	row := v.observation.Rows["Mirror"][b.UUID]
	value, err := mirrorConfiguration(v, row)
	if err != nil {
		out["configuration_availability"], out["reason"] = "unsupported", err.Error()
		if slices.Contains([]string{"MIRROR_CONFIGURATION_UNKNOWN", "MIRROR_PORT_BINDING_UNPROVEN"}, err.Error()) {
			out["configuration_availability"] = "unknown"
		}
		out["execution"] = map[string]any{"available": false, "reason": err.Error(), "capability": candidate.MirrorCapability}
		return out
	}
	out["configuration"], out["configuration_availability"], out["reason"] = value, "known", ""
	bridge, found := parent(v, "Bridge", "mirrors", b.UUID)
	bb := v.decision.Bindings[Key("Bridge", bridge.UUID)]
	if !found || bb.State != "active" {
		out["reason"] = "MIRROR_PARENT_UNPROVEN"
		out["execution"] = map[string]any{"available": false, "reason": "MIRROR_PARENT_UNPROVEN", "capability": candidate.MirrorCapability}
		return out
	}
	out["bridge_ref"] = ref(v, "Bridge", bridge.UUID)
	ports := []map[string]any{}
	for _, binding := range t.Nodes[bb.ManagementID].Links {
		node, known := t.Nodes[binding.ManagementID]
		if !known || node.Binding != binding || binding.Table != "Port" {
			continue
		}
		editable := fresh == "fresh" && t.MirrorAuthority[binding.ManagementID] && len(node.Links) == 1 && t.MirrorAuthority[node.Links[0].ManagementID]
		ports = append(ports, map[string]any{"binding": binding, "name": node.Name, "editable": editable, "output_eligible": editable && node.Name != bridge.Values["name"]})
	}
	slices.SortFunc(ports, func(a, b map[string]any) int { return naturalNameCompare(a["name"].(string), b["name"].(string)) })
	count := len(refs(bridge.Values["ports"]))
	out["port_count"] = count
	truncated := len(ports) != count || len(ports) > 32
	if len(ports) > 32 {
		ports = ports[:32]
	}
	out["ports"], out["ports_truncated"] = ports, truncated
	reason := ""
	switch {
	case fresh != "fresh":
		reason = "MIRROR_OBSERVATION_STALE"
	case truncated:
		reason = "MIRROR_PORT_DETAIL_TRUNCATED"
	case !t.MirrorSupported:
		reason = "MIRROR_SCHEMA_OR_GRAPH_UNSUPPORTED"
	case t.MirrorRuntime == "":
		reason = "MIRROR_RUNTIME_UNQUALIFIED"
	case !t.MirrorAuthority[b.ManagementID] || !t.MirrorAuthority[bb.ManagementID]:
		reason = "MIRROR_AUTHORITY_OR_GRAPH_REQUIRED"
	case !slices.Contains(claims.Capabilities, "workspace.write") || !slices.Contains(claims.Capabilities, candidate.MirrorCapability):
		reason = "CAPABILITY_DENIED"
	}
	ids := mirror.PortIDs(value)
	if value.SelectAll || value.OutputVLAN != nil {
		for _, uuid := range refs(bridge.Values["ports"]) {
			ids = append(ids, v.decision.Bindings[Key("Port", uuid)].ManagementID)
		}
	}
	for _, id := range ids {
		node, known := t.Nodes[id]
		if reason == "" && (!known || node.Binding.Table != "Port" || !t.MirrorAuthority[id] || len(node.Links) != 1 || !t.MirrorAuthority[node.Links[0].ManagementID]) {
			reason = "MIRROR_AUTHORITY_OR_GRAPH_REQUIRED"
		}
	}
	out["execution"] = map[string]any{"available": reason == "", "reason": reason, "capability": candidate.MirrorCapability}
	return out
}
