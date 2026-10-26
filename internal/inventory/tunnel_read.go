package inventory

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/tunnel"
)

func tunnelType(typ string) bool {
	return slices.Contains([]string{"vxlan", "geneve", "gre", "ip6gre", "erspan", "ip6erspan", "lisp", "stt", "gtpu", "bareudp", "srv6"}, typ)
}

func portHasTunnel(v *view, row Row) bool {
	for _, id := range refs(row.Values["interfaces"]) {
		typ, _ := v.observation.Rows["Interface"][id].Values["type"].(string)
		if tunnelType(typ) {
			return true
		}
	}
	return false
}

func tunnelObservation(v *view, b Binding, fresh string, c authn.Claims, t candidate.TopologySnapshot) map[string]any {
	out := map[string]any{"configuration": nil, "configuration_availability": "unknown", "reason": "TUNNEL_RELATION_UNKNOWN", "interface_binding": nil, "interface_ref": nil, "bridge_ref": nil, "native_type": nil, "operational": nil, "source": source(v, fresh, "ovsdb-configuration"), "execution": map[string]any{"available": false, "reason": "TUNNEL_RELATION_UNKNOWN", "capability": candidate.TunnelCapability}}
	port := v.observation.Rows["Port"][b.UUID]
	interfaces := refs(port.Values["interfaces"])
	bridge, parentKnown := parent(v, "Bridge", "ports", b.UUID)
	if len(interfaces) != 1 || !parentKnown {
		return out
	}
	f := v.observation.Rows["Interface"][interfaces[0]]
	ib := v.decision.Bindings[Key("Interface", f.UUID)]
	bb := v.decision.Bindings[Key("Bridge", bridge.UUID)]
	if ib.State != "active" || bb.State != "active" {
		return out
	}
	typ, _ := f.Values["type"].(string)
	value, err := tunnel.Decode(typ, f.Configuration["options"])
	public := tunnel.Configuration{Type: typ, Options: map[string]string{}}
	for _, key := range tunnel.Keys {
		if text, exists := value.Options[key]; exists && len(text) <= 128 {
			public.Options[key] = text
		}
	}
	out["configuration"], out["configuration_availability"], out["reason"] = public, "known", ""
	if err != nil {
		out["configuration_availability"], out["reason"] = "unsupported", err.Error()
	}
	out["native_type"] = typ
	out["interface_binding"] = candidate.Binding{ManagementID: ib.ManagementID, OVSUUID: ib.UUID, Table: "Interface", Generation: v.decision.Generation}
	out["interface_ref"], out["bridge_ref"] = ref(v, "Interface", ib.UUID), ref(v, "Bridge", bb.UUID)
	out["operational"] = map[string]any{"ofport": f.Values["ofport"], "link_state": f.Values["link_state"], "admin_state": f.Values["admin_state"], "error": f.Values["error"], "source": source(v, fresh, "ovs-vswitchd-observation")}
	reason := ""
	switch {
	case fresh != "fresh":
		reason = "TUNNEL_OBSERVATION_STALE"
	case err != nil:
		reason = err.Error()
	case !t.TunnelSupported:
		reason = "TUNNEL_SCHEMA_OR_RUNTIME_UNAVAILABLE"
	case !slices.Contains(t.TunnelTypes, typ):
		reason = "TUNNEL_RUNTIME_TYPE_UNAVAILABLE"
	case !t.TunnelAuthority[b.ManagementID] || !t.TunnelAuthority[ib.ManagementID] || !t.TunnelAuthority[bb.ManagementID]:
		reason = "TUNNEL_AUTHORITY_OR_GRAPH_REQUIRED"
	case !slices.Contains(c.Capabilities, "workspace.write") || !slices.Contains(c.Capabilities, candidate.TunnelCapability):
		reason = "CAPABILITY_DENIED"
	}
	out["execution"] = map[string]any{"available": reason == "", "reason": reason, "capability": candidate.TunnelCapability}
	return out
}
