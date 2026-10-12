package inventory

import (
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// An explicit root-owned identity grant permits draft preparation only. No
// observed owner, schema mutability or grant in another domain substitutes it.
func (s *Service) SetLocalSpanningTreeBridges(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_SPANNING_TREE_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_SPANNING_TREE_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localSpanningTree = allow
	return nil
}

func projectSpanningTree(v *view, allow map[string]bool, out *candidate.Snapshot, bindings []candidate.Binding) {
	out.SpanningTrees = map[string]candidate.SpanningTreeBridge{}
	compatible := 0
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Bridge" {
			for _, c := range t.Columns {
				if slices.Contains([]string{"stp_enable", "rstp_enable", "other_config"}, c.Name) && c.Monitored && c.Mutable && c.SpanningTreeCompatible {
					compatible++
				}
			}
		}
	}
	for _, requested := range bindings {
		if requested.Table != "Bridge" {
			continue
		}
		b := v.decision.Bindings[Key("Bridge", requested.OVSUUID)]
		if b.State != "active" || b.ManagementID != requested.ManagementID {
			continue
		}
		row := v.observation.Rows["Bridge"][b.UUID]
		p := candidate.SpanningTreeBridge{Binding: candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Bridge", Generation: out.Generation}, Supported: compatible == 3, Authority: "unknown", Known: true}
		if allow[b.ManagementID] {
			p.Authority = "local-managed"
		}
		fields, _ := spanningTreeFields(v, row, "Bridge", "fresh", true)
		p.Configuration.Parameters = map[string]*string{}
		for _, name := range append([]string{"stp_enable", "rstp_enable"}, candidate.SpanningTreeBasicKeys...) {
			f := fields[name].(map[string]any)
			if name == "stp_enable" || name == "rstp_enable" {
				value, ok := f["value"].(bool)
				p.Known = p.Known && ok && f["availability"] == "known"
				if name == "stp_enable" {
					p.Configuration.STP = value
				} else {
					p.Configuration.RSTP = value
				}
				continue
			}
			p.Configuration.Parameters[name] = nil
			if f["availability"] == "known" {
				value, ok := f["value"].(string)
				p.Known = p.Known && ok
				p.Configuration.Parameters[name] = &value
			} else if f["availability"] != "unset" {
				p.Known = false
			}
		}
		p.GraphGate, p.Dependency, p.Authority = spanningTreeGraph(v, row, p.Authority)
		out.SpanningTrees[b.ManagementID] = p
	}
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, out.SpanningTrees})
}

// Scope is a conservative preparation boundary, not an execution qualification.
// Complete persistent images are hashed privately; daemon/runtime maps never
// enter the dependency. Mirror output references are not observed, so any
// present Mirror reference keeps this first preparation slice unavailable.
func spanningTreeGraph(v *view, bridge Row, authority string) (string, string, string) {
	code := ""
	block := func(reason string) {
		if code == "" {
			code = reason
		}
	}
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	refSet := func(value any) ([]string, bool) {
		a, known := value.([]any)
		ids := refs(value)
		return ids, known && len(a) == len(ids) && len(slices.Compact(slices.Sorted(slices.Values(ids)))) == len(ids)
	}
	rootBridges, rootRefsKnown := refSet(root.Values["bridges"])
	if len(v.observation.Rows["Open_vSwitch"]) != 1 || root.UUID == "" || !rootRefsKnown || !slices.Contains(rootBridges, bridge.UUID) {
		block("SPANNING_TREE_ROOT_RELATION_UNKNOWN")
	}
	datapath, datapathKnown := bridge.Configuration["datapath_type"].(string)
	if !datapathKnown || !slices.Contains([]string{"", "system"}, datapath) {
		block("SPANNING_TREE_SYSTEM_GRAPH_REQUIRED")
	}
	images := map[string]any{}
	inspect := func(table string, row Row) {
		b := v.decision.Bindings[Key(table, row.UUID)]
		if table != "Open_vSwitch" && (row.UUID == "" || b.State != "active") || len(row.Configuration) == 0 {
			block("SPANNING_TREE_GRAPH_CONFIGURATION_UNKNOWN")
		}
		if name, known := row.Configuration["name"].(string); table != "Open_vSwitch" && (!known || name == "") {
			block("SPANNING_TREE_GRAPH_CONFIGURATION_UNKNOWN")
		}
		cfg := candidate.CloneConfiguration(row.Configuration)
		if _, known := cfg["external_ids"].(map[string]any); !known {
			block("SPANNING_TREE_GRAPH_CONFIGURATION_UNKNOWN")
		}
		if externalControl(cfg["external_ids"]) {
			authority = "externally-controlled"
		}
		if table == "Bridge" {
			delete(cfg, "stp_enable")
			delete(cfg, "rstp_enable")
			other, ok := cfg["other_config"].(map[string]any)
			if !ok {
				block("SPANNING_TREE_GRAPH_CONFIGURATION_UNKNOWN")
			}
			for _, key := range SpanningTreeBridgeKeys {
				if slices.Contains(candidate.SpanningTreeBasicKeys, key) {
					delete(other, key)
				} else if _, exists := other[key]; exists {
					block("SPANNING_TREE_ADVANCED_PARAMETERS_UNSUPPORTED")
				}
			}
		}
		images[table+":"+row.UUID] = []any{b.ManagementID, cfg}
	}
	inspect("Open_vSwitch", root)
	inspect("Bridge", bridge)
	rootOther, rootKnown := root.Configuration["other_config"].(map[string]any)
	if !rootKnown || rootOther["hw-offload"] != nil && rootOther["hw-offload"] != "false" {
		block("SPANNING_TREE_OFFLOAD_UNVERIFIED")
	}
	empty := func(value any) bool { a, ok := value.([]any); return ok && len(a) == 0 }
	noReferences := func(value any) bool {
		if m, ok := value.(map[string]any); ok {
			return len(m) == 0
		}
		return empty(value)
	}
	for _, b := range v.observation.Rows["Bridge"] {
		if !empty(b.Configuration["mirrors"]) {
			block("SPANNING_TREE_MIRROR_RELATION_UNVERIFIED")
		}
	}
	if !empty(bridge.Configuration["controller"]) {
		block("SPANNING_TREE_EXTERNAL_CONTROLLER")
	}
	// Positive references to telemetry or other native domains need separate
	// compatibility evidence. Missing/unknown reference values do not pass.
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Bridge" {
			for _, c := range t.Columns {
				if len(c.References) != 0 && c.Name != "ports" && !noReferences(bridge.Configuration[c.Name]) {
					block("SPANNING_TREE_DOMAIN_REFERENCE_UNSUPPORTED")
				}
			}
		}
	}
	ports, portsKnown := refSet(bridge.Values["ports"])
	if !portsKnown || len(ports) == 0 || len(ports) > 128 {
		block("SPANNING_TREE_GRAPH_BUDGET_OR_LOCAL_PORT")
	}
	local := 0
	seen := map[string]bool{}
	for _, id := range ports {
		port := v.observation.Rows["Port"][id]
		inspect("Port", port)
		parentBridge, unique := parent(v, "Bridge", "ports", id)
		if !unique || parentBridge.UUID != bridge.UUID {
			block("SPANNING_TREE_SHARED_PORT_UNSUPPORTED")
		}
		members, membersKnown := refSet(port.Values["interfaces"])
		if !membersKnown || len(members) != 1 {
			block("SPANNING_TREE_BOND_OR_MEMBER_GRAPH_UNSUPPORTED")
			continue
		}
		iface := v.observation.Rows["Interface"][members[0]]
		inspect("Interface", iface)
		parentPort, unique := parent(v, "Port", "interfaces", iface.UUID)
		if !unique || parentPort.UUID != port.UUID || seen[iface.UUID] {
			block("SPANNING_TREE_SHARED_INTERFACE_UNSUPPORTED")
		}
		seen[iface.UUID] = true
		typ, known := iface.Configuration["type"].(string)
		if !known || !slices.Contains([]string{"", "system", "internal"}, typ) {
			block("SPANNING_TREE_INTERFACE_TYPE_UNSUPPORTED")
		}
		options, known := iface.Configuration["options"].(map[string]any)
		if !known || len(options) != 0 {
			block("SPANNING_TREE_INTERFACE_OPTIONS_UNSUPPORTED")
		}
		if port.Values["name"] == bridge.Values["name"] {
			local++
			if typ != "internal" || iface.Values["name"] != bridge.Values["name"] {
				block("SPANNING_TREE_LOCAL_PORT_UNVERIFIED")
			}
		}
		vlan, known := nativeVLAN(port)
		if !known || len(vlan.CVLANs) != 0 || vlan.Mode != nil && !slices.Contains([]string{"access", "trunk", "native-tagged", "native-untagged"}, *vlan.Mode) || vlan.Tag != nil && (*vlan.Tag == 0 || *vlan.Tag == 4095) {
			block("SPANNING_TREE_VLAN_COMBINATION_UNSUPPORTED")
		}
		if !empty(port.Configuration["qos"]) || !empty(port.Configuration["bond_mode"]) || !empty(port.Configuration["lacp"]) {
			block("SPANNING_TREE_PORT_DOMAIN_UNSUPPORTED")
		}
		other, known := port.Configuration["other_config"].(map[string]any)
		if !known {
			block("SPANNING_TREE_GRAPH_CONFIGURATION_UNKNOWN")
		}
		for key := range other {
			if strings.HasPrefix(key, "stp-") || strings.HasPrefix(key, "rstp-") {
				block("SPANNING_TREE_PORT_PARAMETERS_UNSUPPORTED")
			}
		}
	}
	if local != 1 {
		block("SPANNING_TREE_LOCAL_PORT_UNVERIFIED")
	}
	return code, Digest(images), authority
}

func spanningTreePreparation(v *view, b Binding, fresh string, config bool, allow map[string]bool, claims authn.Claims) map[string]any {
	reason, authority := "CONFIGURATION_WITHHELD", "withheld"
	if config {
		binding := candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Bridge", Generation: v.decision.Generation}
		snapshot := candidate.Snapshot{Generation: v.decision.Generation}
		projectSpanningTree(v, allow, &snapshot, []candidate.Binding{binding})
		p := snapshot.SpanningTrees[b.ManagementID]
		reason, authority = candidate.TreePreparationProblem(p), p.Authority
		if fresh != "fresh" {
			reason = "SPANNING_TREE_OBSERVATION_STALE"
		} else if !slices.Contains(claims.Capabilities, "workspace.write") || !slices.Contains(claims.Capabilities, candidate.SpanningTreePrepareCapability) {
			reason = "CAPABILITY_DENIED"
		}
	}
	return map[string]any{"available": reason == "", "reason": reason, "authority": authority, "mode": "draft-only", "capability": candidate.SpanningTreePrepareCapability}
}
