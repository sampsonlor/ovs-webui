package candidate

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const MirrorCreate = "mirror.session.create"
const MirrorConfigure = "mirror.session.configure"
const MirrorDelete = "mirror.session.delete"
const MirrorCapability = "ovs.mirror.write"

func IsMirrorOperation(op string) bool {
	return slices.Contains([]string{MirrorCreate, MirrorConfigure, MirrorDelete}, op)
}

type MirrorChange struct {
	Object Binding               `json:"object"`
	Bridge Binding               `json:"bridge"`
	Before *mirror.Configuration `json:"before"`
	After  *mirror.Configuration `json:"after"`
}

func mirrorResolve(t TopologySnapshot, id string, native bool) (string, bool) {
	for _, n := range t.Nodes {
		if n.Binding.Table != "Port" {
			continue
		}
		if native && n.Binding.OVSUUID == id {
			return n.Binding.ManagementID, true
		}
		if !native && n.Binding.ManagementID == id {
			return n.Binding.OVSUUID, true
		}
	}
	return "", false
}

func mirrorDecode(t TopologySnapshot, n TopologyNode) (mirror.Configuration, error) {
	return mirror.Decode(t.Configurations[n.Binding.ManagementID], func(uuid string) (string, bool) { return mirrorResolve(t, uuid, true) })
}

func mirrorParent(t TopologySnapshot, object Binding) (TopologyNode, bool) {
	var parent TopologyNode
	count := 0
	for _, n := range t.Nodes {
		if n.Binding.Table != "Bridge" {
			continue
		}
		refs, known := t.Configurations[n.Binding.ManagementID]["mirrors"].([]any)
		if !known {
			return parent, false
		}
		for _, value := range refs {
			if value == object.OVSUUID {
				count++
				parent = n
			}
		}
	}
	return parent, count == 1
}

func mirrorTopologyProblem(i StoredIntent, t TopologySnapshot) string {
	g := i.Topology
	if g == nil || g.Mirror == nil || !t.MirrorSupported || g.Mirror.Object.Table != "Mirror" || g.Mirror.Bridge.Table != "Bridge" {
		return "MIRROR_SCHEMA_OR_GRAPH_UNSUPPORTED"
	}
	m := g.Mirror
	if !g.Compensating && !g.Observed && g.MirrorAuthorityRevision != t.MirrorAuthorityRevision {
		return "MIRROR_AUTHORITY_REVISION_CHANGED"
	}
	if g.MirrorDependency == "" || g.MirrorDependency != tunnelDependency(g, t) {
		return "MIRROR_DEPENDENCY_CHANGED"
	}
	if i.Operation == MirrorCreate && (m.Before != nil || m.After == nil) || i.Operation == MirrorConfigure && (m.Before == nil || m.After == nil) || i.Operation == MirrorDelete && (m.Before == nil || m.After != nil) {
		return "INVALID_MIRROR_REQUEST"
	}
	for _, c := range []*mirror.Configuration{m.Before, m.After} {
		if c != nil && mirror.Validate(*c) != nil {
			return "INVALID_MIRROR_REQUEST"
		}
	}
	desired := m.After
	if g.Compensating {
		desired = m.Before
	}
	if desired != nil {
		bridge, exists := t.Nodes[m.Bridge.ManagementID]
		if !exists || bridge.Binding != m.Bridge {
			return "MIRROR_BRIDGE_BINDING_CHANGED"
		}
		for _, id := range mirror.PortIDs(*desired) {
			port, exists := t.Nodes[id]
			if !exists || port.Binding.Table != "Port" || !slices.Contains(bridge.Links, port.Binding) {
				return "MIRROR_PORT_ON_OTHER_BRIDGE"
			}
			if desired.OutputPort != nil && *desired.OutputPort == id && port.Name == bridge.Name {
				return "MIRROR_LOCAL_INTERNAL_OUTPUT_PROTECTED"
			}
		}
		for _, other := range t.Nodes {
			if other.Binding.Table != "Mirror" || other.Binding == m.Object || g.Replacements[m.Object.OVSUUID] == other.Binding {
				continue
			}
			parent, known := mirrorParent(t, other.Binding)
			if !known {
				return "MIRROR_PARENT_UNPROVEN"
			}
			if parent.Binding != m.Bridge {
				continue
			}
			config, err := mirrorDecode(t, other)
			if err != nil {
				return "MIRROR_EXISTING_SESSION_UNQUALIFIED"
			}
			if config.OutputPort != nil && (slices.Contains(desired.SourcePorts, *config.OutputPort) || slices.Contains(desired.DestinationPorts, *config.OutputPort)) || desired.OutputPort != nil && (slices.Contains(config.SourcePorts, *desired.OutputPort) || slices.Contains(config.DestinationPorts, *desired.OutputPort)) {
				return "MIRROR_OUTPUT_SELECTION_CONFLICT"
			}
		}
	}
	return ""
}

func mirrorConfigureImages(i StoredIntent, t TopologySnapshot, node TopologyNode, cfg map[string]any) error {
	g := i.Topology
	if g == nil || g.Mirror == nil {
		return apitypes.Fail(422, "INVALID_MIRROR_REQUEST")
	}
	m := g.Mirror
	if node.Binding == m.Object && m.After != nil {
		fields, err := mirror.Fields(*m.After, func(id string) (string, bool) { return mirrorResolve(t, id, false) })
		if err != nil {
			return err
		}
		for key, value := range fields {
			cfg[key] = value
		}
	}
	if node.Binding == m.Bridge {
		refs, ok := cfg["mirrors"].([]any)
		if !ok {
			return apitypes.Fail(409, "MIRROR_PARENT_UNPROVEN")
		}
		if i.Operation == MirrorCreate {
			cfg["mirrors"] = append(refs, m.Object.OVSUUID)
		} else if i.Operation == MirrorDelete {
			values := []any{}
			count := 0
			for _, value := range refs {
				if value == m.Object.OVSUUID {
					count++
				} else {
					values = append(values, value)
				}
			}
			if count != 1 {
				return apitypes.Fail(409, "MIRROR_PARENT_UNPROVEN")
			}
			cfg["mirrors"] = values
		}
	}
	return nil
}

func stageMirror(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	fail := func(code string) (Candidate, error) { return c, apitypes.Fail(409, code) }
	if len(cmd.Intents) != 1 || len(c.Intents) != 0 {
		return fail("TOPOLOGY_RESTAGE_REQUIRED")
	}
	in := cmd.Intents[0]
	r, t := in.Topology, s.Topology
	if r == nil || !IsMirrorOperation(in.Operation) || !apitypes.ManagementID(in.ID) || r.Tunnel != nil || r.NativeType != "" || len(r.InterfaceNames)+len(r.Sources)+len(r.Members) != 0 || r.Destination != nil || r.Peer != nil || r.Ofport != 0 || Digest(in.Value) != Digest(VLAN{}) || in.Multicast != nil || in.Protected != nil || in.SpanningTree != nil || in.Policing != nil || in.MTURequest != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || in.Name != "" || in.VLANID != 0 || len(in.Members) != 0 {
		return fail("INVALID_MIRROR_REQUEST")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return fail("GENERATION_RECONCILIATION_REQUIRED")
	}
	if !t.MirrorSupported || !t.Supported || !t.Capacity {
		return fail("MIRROR_SCHEMA_OR_GRAPH_UNSUPPORTED")
	}
	object, exists := t.Nodes[in.Object.ManagementID]
	if !exists || object.Binding != in.Object {
		return fail("OBJECT_BINDING_CHANGED")
	}
	before := map[string]TopologyNode{}
	after := map[string]TopologyNode{}
	configs := map[string]map[string]any{}
	load := func(n TopologyNode) {
		before[n.Binding.ManagementID], after[n.Binding.ManagementID] = n, n
		configs[n.Binding.ManagementID] = CloneConfiguration(t.Configurations[n.Binding.ManagementID])
	}
	change := &MirrorChange{}
	bridge := object
	if in.Operation == MirrorCreate {
		if object.Binding.Table != "Bridge" || !mirror.ValidName(r.Name) || r.Mirror == nil || len(t.MirrorDefaults) == 0 {
			return fail("INVALID_MIRROR_REQUEST")
		}
		refs, ok := t.Configurations[object.Binding.ManagementID]["mirrors"].([]any)
		if !ok || len(refs) >= 32 {
			return fail("MIRROR_NATIVE_SESSION_CAPACITY")
		}
		for _, n := range t.Nodes {
			if n.Binding.Table == "Mirror" && n.Name == r.Name {
				return fail("OBJECT_NAME_IN_USE")
			}
		}
		change.Object = Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Mirror", Generation: s.Generation}
		object = TopologyNode{Binding: change.Object, Name: r.Name, Type: "mirror", Links: []Binding{}}
		configs[object.Binding.ManagementID] = CloneConfiguration(t.MirrorDefaults)
		configs[object.Binding.ManagementID]["name"] = r.Name
		after[object.Binding.ManagementID] = object
	} else {
		if object.Binding.Table != "Mirror" || r.Name != "" {
			return fail("INVALID_MIRROR_REQUEST")
		}
		var ok bool
		bridge, ok = mirrorParent(t, object.Binding)
		if !ok {
			return fail("MIRROR_PARENT_UNPROVEN")
		}
		value, err := mirrorDecode(t, object)
		if err != nil {
			return fail(err.Error())
		}
		change.Object, change.Before = object.Binding, &value
		load(object)
	}
	load(bridge)
	change.Bridge = bridge.Binding
	if in.Operation == MirrorDelete {
		if r.Mirror != nil {
			return fail("INVALID_MIRROR_REQUEST")
		}
		delete(after, object.Binding.ManagementID)
	} else {
		if r.Mirror == nil || mirror.Validate(*r.Mirror) != nil {
			return fail("INVALID_MIRROR_REQUEST")
		}
		value := mirror.Clone(*r.Mirror)
		change.After = &value
	}
	for _, value := range []*mirror.Configuration{change.Before, change.After} {
		if value == nil {
			continue
		}
		ids := mirror.PortIDs(*value)
		if value.SelectAll || value.OutputVLAN != nil {
			for _, port := range bridge.Links {
				ids = append(ids, port.ManagementID)
			}
		}
		for _, id := range ids {
			port, ok := t.Nodes[id]
			if !ok || port.Binding.Table != "Port" || !slices.Contains(bridge.Links, port.Binding) || len(port.Links) != 1 {
				return fail("MIRROR_PORT_ON_OTHER_BRIDGE")
			}
			iface, ok := t.Nodes[port.Links[0].ManagementID]
			if !ok || iface.Binding != port.Links[0] || iface.Binding.Table != "Interface" {
				return fail("MIRROR_PORT_BINDING_UNPROVEN")
			}
			load(port)
			load(iface)
		}
	}
	g := &TopologyChange{Mirror: change, MirrorAuthorityRevision: t.MirrorAuthorityRevision, Request: *r, Root: t.Root, RootDependency: t.RootDependency, Before: topologyNodes(before), Replacements: map[string]Binding{}}
	if r.Mirror != nil {
		value := mirror.Clone(*r.Mirror)
		g.Request.Mirror = &value
	}
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: t.RootDependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), Topology: g}
	for id, n := range after {
		cfg := configs[id]
		if err := mirrorConfigureImages(i, t, n, cfg); err != nil {
			return fail("MIRROR_PRIVATE_IMAGE_UNPROVEN")
		}
		n.Digest = ConfigurationDigest(cfg)
		if n.Binding.Table == "Mirror" {
			value := mirror.Clone(*change.After)
			n.Summary = &TopologyConfigurationSummary{Mirror: &value}
		}
		after[id] = n
	}
	g.After = topologyNodes(after)
	for id, n := range before {
		if _, exists := after[id]; !exists {
			g.Replacements[n.Binding.OVSUUID] = Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: n.Binding.Table, Generation: s.Generation}
		}
	}
	for id, n := range before {
		cfg := RemapConfiguration(t.Configurations[id], g.Replacements)
		if replacement, exists := g.Replacements[n.Binding.OVSUUID]; exists {
			n.Binding = replacement
		}
		n.Digest = ConfigurationDigest(cfg)
		g.Restored = append(g.Restored, n)
	}
	g.MirrorDependency = tunnelDependency(g, t)
	for _, check := range topologyChecks(i, s) {
		if check.State != "allowed" {
			return fail(check.Code)
		}
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
