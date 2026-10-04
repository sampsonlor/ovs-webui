package inventory

import (
	"slices"
	"sort"
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
func nativeMTURequest(v any) (*int, bool) {
	values, ok := v.([]any)
	if !ok || len(values) > 1 {
		return nil, false
	}
	if len(values) == 0 {
		return nil, true
	}
	s, ok := values[0].(string)
	if !ok {
		return nil, false
	}
	n, err := strconv.Atoi(s)
	return &n, err == nil && candidate.ValidMTU(n)
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
		p.Requested, p.Known = nativeMTURequest(row.Values["mtu_request"])
		if n, known := nativeMTURequest(row.Values["mtu"]); known && n != nil {
			p.Observed = *n
		}
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
		if p.Eligible {
			p.Default = mtuDefaultContext(v, bridge, row.UUID, out.Generation)
		}
		out.Interfaces[b.ManagementID] = p
	}
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, out.Interfaces})
}

// Bound and seal the whole Bridge's MTU inputs. Automatic internal Interfaces
// are excluded from the minimum and their derived MTU is not a dependency on
// itself. Missing/unstable devices and unsupported types fail closed; no 1500
// fallback is invented when there is no proven contributor.
func mtuDefaultContext(v *view, bridge Row, target, generation string) *candidate.MTUDefault {
	ports := refs(bridge.Values["ports"])
	if len(ports) == 0 || len(ports) > 32 {
		return nil
	}
	sort.Strings(ports)
	context := &candidate.MTUDefault{Bindings: []candidate.Binding{}}
	sealed := []any{}
	count := 0
	for _, portID := range ports {
		port, present := v.observation.Rows["Port"][portID]
		parentBridge, unique := parent(v, "Bridge", "ports", portID)
		if !present || !unique || parentBridge.UUID != bridge.UUID || externalControl(port.Values["external_ids"]) {
			return nil
		}
		b := v.decision.Bindings[Key("Port", portID)]
		if b.State != "active" {
			return nil
		}
		context.Bindings = append(context.Bindings, candidate.Binding{ManagementID: b.ManagementID, OVSUUID: portID, Table: "Port", Generation: generation})
		interfaces := refs(port.Values["interfaces"])
		if len(interfaces) == 0 {
			return nil
		}
		sort.Strings(interfaces)
		sealed = append(sealed, []any{portID, b.ManagementID, port.Values["name"], interfaces})
		for _, id := range interfaces {
			count++
			if count > 32 {
				return nil
			}
			if id == target {
				continue
			}
			row, present := v.observation.Rows["Interface"][id]
			parentPort, unique := parent(v, "Port", "interfaces", id)
			b := v.decision.Bindings[Key("Interface", id)]
			request, known := nativeMTURequest(row.Values["mtu_request"])
			typ, knownType := row.Values["type"].(string)
			errors, knownError := row.Values["error"].([]any)
			ofport, knownOfport := row.Values["ofport"].([]any)
			if !present || !unique || parentPort.UUID != portID || b.State != "active" || !known || !knownType || (typ != "internal" && typ != "system" && typ != "") || !knownError || len(errors) != 0 || !knownOfport || len(ofport) != 1 || row.InterfaceOptionsEmpty == nil || !*row.InterfaceOptionsEmpty || externalControl(row.Values["external_ids"]) {
				return nil
			}
			portText, textKnown := ofport[0].(string)
			if !textKnown {
				return nil
			}
			portNumber, err := strconv.Atoi(portText)
			if err != nil || portNumber < 1 || portNumber > 65534 {
				return nil
			}
			context.Bindings = append(context.Bindings, candidate.Binding{ManagementID: b.ManagementID, OVSUUID: id, Table: "Interface", Generation: generation})
			var observed any
			if typ != "internal" || request != nil {
				mtu, known := nativeMTURequest(row.Values["mtu"])
				if !known || mtu == nil || request != nil && *request != *mtu {
					return nil
				}
				observed = *mtu
				if context.MTU == 0 || *mtu < context.MTU {
					context.MTU = *mtu
				}
			}
			sealed = append(sealed, []any{id, b.ManagementID, row.Values["name"], typ, request, observed, ofport, row.InterfaceOptionsEmpty})
		}
	}
	if len(context.Bindings) > 60 || !candidate.ValidMTU(context.MTU) {
		return nil
	}
	context.Dependency = Digest(sealed)
	return context
}
