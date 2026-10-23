package inventory

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"slices"
	"strings"
)

func (s *Service) SetLocalMulticastBridges(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_MULTICAST_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_MULTICAST_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localMulticast = allow
	s.multicastAuthorityRevision = repository.NewID()
	return nil
}
func projectMulticast(v *view, allow map[string]bool, out *candidate.Snapshot, requested []candidate.Binding) {
	out.Multicasts = map[string]candidate.MulticastBridge{}
	fields := map[string]bool{}
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Bridge" {
			for _, c := range t.Columns {
				if slices.Contains([]string{"mcast_snooping_enable", "other_config", "external_ids"}, c.Name) {
					fields[c.Name] = c.MulticastCompatible && c.Monitored && c.Mutable && !c.Ephemeral
				}
			}
		}
	}
	supported := fields["mcast_snooping_enable"] && fields["other_config"] && fields["external_ids"]
	for _, r := range requested {
		if r.Table != "Bridge" {
			continue
		}
		binding := v.decision.Bindings[Key("Bridge", r.OVSUUID)]
		if binding.State != "active" || binding.ManagementID != r.ManagementID {
			continue
		}
		row := v.observation.Rows["Bridge"][binding.UUID]
		enabled, known := row.Configuration["mcast_snooping_enable"].(bool)
		cfg := multicast.Configuration{Enabled: enabled, Parameters: map[string]*string{}}
		other, ok := row.Configuration["other_config"].(map[string]any)
		known = known && ok
		for _, key := range multicast.Keys {
			cfg.Parameters[key] = nil
			if raw, exists := other[key]; exists {
				value, ok := raw.(string)
				if !ok || len(value) > 128 {
					known = false
				} else {
					cfg.Parameters[key] = &value
				}
			}
		}
		b := candidate.MulticastBridge{Binding: candidate.Binding{ManagementID: binding.ManagementID, OVSUUID: binding.UUID, Table: "Bridge", Generation: out.Generation}, Configuration: cfg, Known: known, Supported: supported, Authority: "unknown"}
		if allow[binding.ManagementID] {
			b.Authority = "local-managed"
		}
		b.Gate, b.Dependency, b.Bindings = multicastGraph(v, row, out.Generation)
		for _, bind := range b.Bindings {
			if externalControl(v.observation.Rows[bind.Table][bind.OVSUUID].Configuration["external_ids"]) {
				b.Authority = "externally-controlled"
			}
		}
		if externalControl(v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root].Configuration["external_ids"]) {
			b.Authority = "externally-controlled"
		}
		if known && multicast.Validate(cfg) != nil {
			b.Gate = "MULTICAST_ORIGINAL_UNQUALIFIED"
		}
		out.Multicasts[binding.ManagementID] = b
	}
	if len(out.Multicasts) > 0 {
		out.Policy = Digest([]any{out.Policy, allow})
		out.Revision = Digest([]any{out.Revision, out.Multicasts})
	}
}
func multicastGraph(v *view, bridge Row, generation string) (string, string, []candidate.Binding) {
	code := ""
	block := func(reason string) {
		if code == "" {
			code = reason
		}
	}
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	empty := func(x any) bool { a, ok := x.([]any); return ok && len(a) == 0 }
	if len(v.observation.Rows["Open_vSwitch"]) != 1 || root.UUID == "" || bridge.UUID == "" || !slices.Contains(refs(root.Values["bridges"]), bridge.UUID) {
		block("MULTICAST_BRIDGE_RELATION_UNKNOWN")
	}
	if typ, ok := bridge.Configuration["datapath_type"].(string); !ok || !slices.Contains([]string{"", "system"}, typ) {
		block("MULTICAST_SYSTEM_BRIDGE_REQUIRED")
	}
	for _, flag := range []string{"stp_enable", "rstp_enable"} {
		if enabled, ok := bridge.Configuration[flag].(bool); !ok || enabled {
			block("MULTICAST_BRIDGE_COMBINATION_UNQUALIFIED")
		}
	}
	for _, field := range []string{"controller", "mirrors", "sflow", "netflow", "ipfix"} {
		if !empty(bridge.Configuration[field]) {
			block("MULTICAST_FOREIGN_DOMAIN_UNQUALIFIED")
		}
	}
	if !empty(bridge.Configuration["flood_vlans"]) {
		block("MULTICAST_ADVANCED_POLICY_UNQUALIFIED")
	}
	bridgeOther, bridgeOtherKnown := bridge.Configuration["other_config"].(map[string]any)
	if !bridgeOtherKnown {
		block("MULTICAST_GRAPH_UNKNOWN")
	}
	for key := range bridgeOther {
		if strings.HasPrefix(key, "mcast-") && !slices.Contains(multicast.Keys, key) {
			block("MULTICAST_ADVANCED_POLICY_UNQUALIFIED")
		}
	}
	other, ok := root.Configuration["other_config"].(map[string]any)
	if !ok || other["hw-offload"] != nil && other["hw-offload"] != "false" {
		block("MULTICAST_OFFLOAD_UNQUALIFIED")
	}
	ports := refs(bridge.Values["ports"])
	for _, otherBridge := range v.observation.Rows["Bridge"] {
		if !empty(otherBridge.Configuration["mirrors"]) {
			block("MULTICAST_MIRROR_RELATION_UNQUALIFIED")
		}
	}
	if len(ports) < 2 || len(ports) > 32 {
		block("MULTICAST_GRAPH_BUDGET")
	}
	bindings := []candidate.Binding{}
	images := map[string]any{}
	inspect := func(table string, row Row) {
		b := v.decision.Bindings[Key(table, row.UUID)]
		if row.UUID == "" || len(row.Configuration) == 0 || table != "Open_vSwitch" && b.State != "active" {
			block("MULTICAST_GRAPH_UNKNOWN")
		}
		cfg := candidate.CloneConfiguration(row.Configuration)
		if table == "Bridge" && row.UUID == bridge.UUID {
			delete(cfg, "mcast_snooping_enable")
			if other, ok := cfg["other_config"].(map[string]any); ok {
				for _, key := range multicast.Keys {
					delete(other, key)
				}
			} else {
				block("MULTICAST_GRAPH_UNKNOWN")
			}
			if labels, ok := cfg["external_ids"].(map[string]any); ok {
				delete(labels, candidate.MulticastMarker)
			} else {
				block("MULTICAST_GRAPH_UNKNOWN")
			}
		}

		images[Key(table, row.UUID)] = []any{b.ManagementID, cfg}
		if table != "Open_vSwitch" {
			bindings = append(bindings, candidate.Binding{ManagementID: b.ManagementID, OVSUUID: row.UUID, Table: table, Generation: generation})
		}
	}
	inspect("Open_vSwitch", root)
	inspect("Bridge", bridge)
	local := 0
	for _, id := range ports {
		p := v.observation.Rows["Port"][id]
		if p.Values["name"] == bridge.Values["name"] {
			local++
		}
		inspect("Port", p)
		portOther, portOtherKnown := p.Configuration["other_config"].(map[string]any)
		if !portOtherKnown {
			block("MULTICAST_GRAPH_UNKNOWN")
		}
		for key := range portOther {
			if strings.HasPrefix(key, "mcast-") {
				block("MULTICAST_ADVANCED_POLICY_UNQUALIFIED")
			}
		}
		if protected, known := p.Configuration["protected"].(bool); !known || protected {
			block("MULTICAST_ISOLATION_COMBINATION_UNQUALIFIED")
		}

		owner, unique := parent(v, "Bridge", "ports", id)
		if !unique || owner.UUID != bridge.UUID {
			block("MULTICAST_SHARED_PORT_UNSUPPORTED")
		}
		members := refs(p.Values["interfaces"])
		if len(members) != 1 || !empty(p.Configuration["bond_mode"]) || !empty(p.Configuration["lacp"]) || !empty(p.Configuration["qos"]) {
			block("MULTICAST_PORT_COMBINATION_UNQUALIFIED")
		}
		vlan, known := nativeVLAN(p)
		if !known || len(vlan.CVLANs) > 0 || vlan.Tag != nil && (*vlan.Tag < 1 || *vlan.Tag > 4094) || vlan.Mode != nil && !slices.Contains([]string{"access", "trunk", "native-tagged", "native-untagged"}, *vlan.Mode) {
			block("MULTICAST_VLAN_COMBINATION_UNQUALIFIED")
		}
		for _, iid := range members {
			i := v.observation.Rows["Interface"][iid]
			inspect("Interface", i)
			parentPort, unique := parent(v, "Port", "interfaces", iid)
			typ, known := i.Configuration["type"].(string)
			if p.Values["name"] == bridge.Values["name"] && (typ != "internal" || i.Values["name"] != bridge.Values["name"]) {
				block("MULTICAST_LOCAL_PORT_UNPROVEN")
			}
			options, ok := i.Configuration["options"].(map[string]any)
			if !unique || parentPort.UUID != id || !known || !slices.Contains([]string{"", "system", "internal"}, typ) || !ok || len(options) != 0 {
				block("MULTICAST_INTERFACE_COMBINATION_UNQUALIFIED")
			}
		}
	}
	if local != 1 {
		block("MULTICAST_LOCAL_PORT_UNPROVEN")
	}
	return code, Digest(images), bindings
}
