package inventory

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"slices"
)

// This independent root grant never inherits VLAN, topology or STP ownership.
func (s *Service) SetLocalIsolationPorts(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_ISOLATION_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_ISOLATION_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localIsolation = allow
	s.isolationAuthorityRevision = repository.NewID()
	return nil
}
func projectIsolation(v *view, allow map[string]bool, out *candidate.Snapshot, requested []candidate.Binding) {
	out.Isolations = map[string]candidate.IsolationPort{}
	supported := false
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Port" {
			for _, c := range t.Columns {
				if c.Name == "protected" {
					supported = c.IsolationCompatible && c.Monitored && c.Mutable
				}
			}
		}
	}
	for _, r := range requested {
		if r.Table != "Port" {
			continue
		}
		b := v.decision.Bindings[Key("Port", r.OVSUUID)]
		if b.State != "active" || b.ManagementID != r.ManagementID {
			continue
		}
		row := v.observation.Rows["Port"][b.UUID]
		value, known := row.Values["protected"].(bool)
		p := candidate.IsolationPort{Binding: candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Port", Generation: out.Generation}, Known: known, Protected: value, Supported: supported, Authority: "unknown"}
		if allow[b.ManagementID] {
			p.Authority = "local-managed"
		}
		bridge, unique := parent(v, "Bridge", "ports", b.UUID)
		p.Gate, p.Dependency, p.Bindings = isolationGraph(v, row, bridge, out.Generation)
		if !unique {
			p.Gate = "ISOLATION_BRIDGE_RELATION_UNKNOWN"
		}
		for _, bind := range p.Bindings {
			if externalControl(v.observation.Rows[bind.Table][bind.OVSUUID].Configuration["external_ids"]) {
				p.Authority = "externally-controlled"
			}
		}
		if externalControl(v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root].Configuration["external_ids"]) {
			p.Authority = "externally-controlled"
		}
		out.Isolations[b.ManagementID] = p
	}
	if len(out.Isolations) > 0 {
		out.Policy = Digest([]any{out.Policy, allow})
		out.Revision = Digest([]any{out.Revision, out.Isolations})
	}
}
func isolationGraph(v *view, target, bridge Row, generation string) (string, string, []candidate.Binding) {
	code := ""
	block := func(reason string) {
		if code == "" {
			code = reason
		}
	}
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	empty := func(x any) bool { a, ok := x.([]any); return ok && len(a) == 0 }
	if len(v.observation.Rows["Open_vSwitch"]) != 1 || root.UUID == "" || bridge.UUID == "" || !slices.Contains(refs(root.Values["bridges"]), bridge.UUID) {
		block("ISOLATION_BRIDGE_RELATION_UNKNOWN")
	}
	if typ, ok := bridge.Configuration["datapath_type"].(string); !ok || !slices.Contains([]string{"", "system"}, typ) {
		block("ISOLATION_SYSTEM_BRIDGE_REQUIRED")
	}
	if target.Values["name"] == bridge.Values["name"] {
		block("ISOLATION_LOCAL_PORT_UNSUPPORTED")
	}
	for _, flag := range []string{"stp_enable", "rstp_enable", "mcast_snooping_enable"} {
		if enabled, ok := bridge.Configuration[flag].(bool); !ok || enabled {
			block("ISOLATION_BRIDGE_COMBINATION_UNQUALIFIED")
		}
	}
	for _, field := range []string{"controller", "mirrors", "sflow", "netflow", "ipfix"} {
		if !empty(bridge.Configuration[field]) {
			block("ISOLATION_FOREIGN_DOMAIN_UNQUALIFIED")
		}
	}
	other, ok := root.Configuration["other_config"].(map[string]any)
	if !ok || other["hw-offload"] != nil && other["hw-offload"] != "false" {
		block("ISOLATION_OFFLOAD_UNQUALIFIED")
	}
	ports := refs(bridge.Values["ports"])
	if len(ports) < 2 || len(ports) > 32 {
		block("ISOLATION_GRAPH_BUDGET")
	}
	bindings := []candidate.Binding{}
	images := map[string]any{}
	inspect := func(table string, row Row) {
		b := v.decision.Bindings[Key(table, row.UUID)]
		if row.UUID == "" || len(row.Configuration) == 0 || table != "Open_vSwitch" && b.State != "active" {
			block("ISOLATION_GRAPH_UNKNOWN")
		}
		cfg := candidate.CloneConfiguration(row.Configuration)
		if table == "Port" && row.UUID == target.UUID {
			delete(cfg, "protected")
			if labels, ok := cfg["external_ids"].(map[string]any); ok {
				delete(labels, execution.MarkerKey)
			} else {
				block("ISOLATION_GRAPH_UNKNOWN")
			}
		}
		images[Key(table, row.UUID)] = []any{b.ManagementID, cfg}
		if table != "Open_vSwitch" {
			bindings = append(bindings, candidate.Binding{ManagementID: b.ManagementID, OVSUUID: row.UUID, Table: table, Generation: generation})
		}
	}
	inspect("Open_vSwitch", root)
	inspect("Bridge", bridge)
	for _, id := range ports {
		p := v.observation.Rows["Port"][id]
		inspect("Port", p)
		owner, unique := parent(v, "Bridge", "ports", id)
		if !unique || owner.UUID != bridge.UUID {
			block("ISOLATION_SHARED_PORT_UNSUPPORTED")
		}
		members := refs(p.Values["interfaces"])
		if len(members) != 1 || !empty(p.Configuration["bond_mode"]) || !empty(p.Configuration["lacp"]) || !empty(p.Configuration["qos"]) {
			block("ISOLATION_PORT_COMBINATION_UNQUALIFIED")
		}
		vlan, known := nativeVLAN(p)
		if !known || len(vlan.CVLANs) > 0 || vlan.Mode != nil && !slices.Contains([]string{"access", "trunk", "native-tagged", "native-untagged"}, *vlan.Mode) {
			block("ISOLATION_VLAN_COMBINATION_UNQUALIFIED")
		}
		for _, iid := range members {
			i := v.observation.Rows["Interface"][iid]
			inspect("Interface", i)
			parentPort, unique := parent(v, "Port", "interfaces", iid)
			typ, known := i.Configuration["type"].(string)
			options, ok := i.Configuration["options"].(map[string]any)
			if !unique || parentPort.UUID != id || !known || !slices.Contains([]string{"", "system", "internal"}, typ) || !ok || len(options) != 0 {
				block("ISOLATION_INTERFACE_COMBINATION_UNQUALIFIED")
			}
		}
	}
	return code, Digest(images), bindings
}
