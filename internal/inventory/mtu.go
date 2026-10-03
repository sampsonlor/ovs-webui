package inventory

import (
	"slices"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// Independent root policy, bound to immutable Interface IDs. Neither a Port
// VLAN grant nor a browser's requested MTU confers this field authority.
func (s *Service) SetLocalMTUInterfaces(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_MTU_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_MTU_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localMTU = allow
	return nil
}
func explicitMTU(v any) (int, bool) {
	values, ok := v.([]any)
	if !ok || len(values) != 1 {
		return 0, false
	}
	s, ok := values[0].(string)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && candidate.ValidMTU(n)
}
func projectMTU(v *view, allow map[string]bool, out *candidate.Snapshot, bindings []candidate.Binding) {
	out.Interfaces = map[string]candidate.InterfaceMTU{}
	supported := false
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Interface" {
			for _, c := range t.Columns {
				if c.Name == "mtu_request" && c.Monitored && c.MTUCompatible {
					supported = true
				}
			}
		}
	}
	for _, requested := range bindings {
		if requested.Table != "Interface" {
			continue
		}
		b := v.decision.Bindings[Key("Interface", requested.OVSUUID)]
		if b.ManagementID != requested.ManagementID || b.State != "active" {
			continue
		}
		row := v.observation.Rows["Interface"][b.UUID]
		p := candidate.InterfaceMTU{Binding: candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Interface", Generation: out.Generation}, Supported: supported, Authority: "unknown"}
		p.Requested, p.Known = explicitMTU(row.Values["mtu_request"])
		port, portKnown := parent(v, "Port", "interfaces", b.UUID)
		bridge, bridgeKnown := parent(v, "Bridge", "ports", port.UUID)
		bind := func(table string, r Row) candidate.Binding {
			b := v.decision.Bindings[Key(table, r.UUID)]
			if b.State != "active" {
				return candidate.Binding{}
			}
			return candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: table, Generation: out.Generation}
		}
		p.Port, p.Bridge = bind("Port", port), bind("Bridge", bridge)
		root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
		if allow[b.ManagementID] {
			p.Authority = "local-managed"
		}
		for _, r := range []Row{root, bridge, port, row} {
			if externalControl(r.Values["external_ids"]) {
				p.Authority = "externally-controlled"
			}
		}
		p.Eligible = portKnown && bridgeKnown && p.Port.ManagementID != "" && p.Bridge.ManagementID != "" && len(v.observation.Rows["Open_vSwitch"]) == 1 && slices.Contains(refs(root.Values["bridges"]), bridge.UUID) && bridge.Values["datapath_type"] == "system" && row.Values["type"] == "internal" && row.Values["name"] == port.Values["name"] && port.Values["name"] != bridge.Values["name"] && len(refs(port.Values["interfaces"])) == 1 && row.InterfaceOptionsEmpty != nil && *row.InterfaceOptionsEmpty
		// Daemon observations and the executor's own commit marker are excluded;
		// attachment, type, raw option emptiness and control ownership are sealed.
		p.Dependency = Digest([]any{root.UUID, p.Port, p.Bridge, bridge.Values["name"], bridge.Values["datapath_type"], bridge.Values["ports"], port.Values["name"], port.Values["interfaces"], row.Values["name"], row.Values["type"], row.Values["options"], row.InterfaceOptionsEmpty, p.Authority})
		out.Interfaces[b.ManagementID] = p
	}
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, out.Interfaces})
}
