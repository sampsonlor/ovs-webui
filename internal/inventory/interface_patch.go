package inventory

func patchColumn(v *view, table, name string) bool {
	for _, t := range v.observation.Schema.Tables {
		if t.Name == table {
			for _, c := range t.Columns {
				if c.Name == name {
					return c.Monitored && c.PatchCompatible
				}
			}
		}
	}
	return false
}

// Both ends and their bindings are resolved in this single immutable view. A
// native name is a configuration request, never an identity or forwarding proof.
func interfacePatchPeer(v *view, row, bridge Row, fresh string, config bool) map[string]any {
	out := map[string]any{"availability": "unknown", "reason": "PATCH_TYPE_UNKNOWN", "peer_ref": nil, "peer_port_ref": nil, "peer_bridge_ref": nil, "source": source(v, fresh, "ovsdb-configuration")}
	fail := func(availability, reason string) map[string]any {
		out["availability"], out["reason"] = availability, reason
		return out
	}
	// Check permission before applicability, names, shapes or freshness.
	if !config {
		return fail("withheld", "CONFIGURATION_WITHHELD")
	}
	if fresh != "fresh" || v.decision.State != "confirmed" {
		return fail("unknown", "PATCH_OBSERVATION_STALE")
	}
	if !patchColumn(v, "Interface", "type") || !patchColumn(v, "Interface", "options") || !patchColumn(v, "Bridge", "datapath_type") {
		return fail("unsupported", "PATCH_SCHEMA_UNSUPPORTED")
	}
	typ, ok := row.Values["type"].(string)
	if !ok {
		return out
	}
	if typ != "patch" {
		return fail("unsupported", "NOT_PATCH_INTERFACE")
	}
	options, ok := row.Values["options"].(map[string]any)
	if !ok {
		return fail("unknown", "PATCH_OPTIONS_UNKNOWN")
	}
	name, ok := options["peer"].(string)
	if !ok || name == "" {
		return fail("unknown", "PATCH_PEER_UNSPECIFIED")
	}
	selfName, ok := row.Values["name"].(string)
	if !ok || selfName == "" {
		return fail("unknown", "PATCH_IDENTITY_UNKNOWN")
	}
	if name == selfName {
		return fail("unknown", "PATCH_PEER_SELF")
	}
	var peer Row
	for _, candidate := range v.observation.Rows["Interface"] {
		if candidate.Values["name"] == name {
			if peer.UUID != "" {
				return fail("unknown", "PATCH_PEER_AMBIGUOUS")
			}
			peer = candidate
		}
	}
	if peer.UUID == "" {
		return fail("unknown", "PATCH_PEER_NOT_FOUND")
	}
	if peer.Values["type"] != "patch" {
		return fail("unknown", "PATCH_PEER_TYPE_MISMATCH")
	}
	peerOptions, ok := peer.Values["options"].(map[string]any)
	if !ok || peerOptions["peer"] != selfName {
		return fail("unknown", "PATCH_PEER_NOT_RECIPROCAL")
	}
	port, ok := parent(v, "Port", "interfaces", peer.UUID)
	if !ok {
		return fail("unknown", "PATCH_PEER_RELATION_UNKNOWN")
	}
	peerBridge, ok := parent(v, "Bridge", "ports", port.UUID)
	if !ok {
		return fail("unknown", "PATCH_PEER_RELATION_UNKNOWN")
	}
	datapath := func(r Row) (string, bool) {
		s, known := r.Values["datapath_type"].(string)
		if s == "" && known {
			s = "system"
		}
		return s, known
	}
	a, ka := datapath(bridge)
	b, kb := datapath(peerBridge)
	if !ka || !kb {
		return fail("unknown", "PATCH_DATAPATH_UNKNOWN")
	}
	if a != b {
		return fail("unknown", "PATCH_DATAPATH_MISMATCH")
	}
	for table, id := range map[string]string{"Interface": peer.UUID, "Port": port.UUID, "Bridge": peerBridge.UUID} {
		binding, exists := v.decision.Bindings[Key(table, id)]
		if !exists || binding.State != "active" || binding.ManagementID == "" || binding.UUID != id || binding.Table != table {
			return fail("unknown", "PATCH_PEER_IDENTITY_UNAVAILABLE")
		}
	}
	out["availability"], out["reason"] = "known", "PATCH_RECIPROCAL_CONFIGURATION"
	out["peer_ref"], out["peer_port_ref"], out["peer_bridge_ref"] = ref(v, "Interface", peer.UUID), ref(v, "Port", port.UUID), ref(v, "Bridge", peerBridge.UUID)
	return out
}
