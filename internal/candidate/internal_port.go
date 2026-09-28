package candidate

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"slices"
)

const InternalPortCreate = "port.create-internal"
const MaxInternalPortParentMembers = 32

// The parent and its existing members are captured by mgrd. The two child
// identities are minted before execution and can never be adopted by name.
type InternalPortCreation struct {
	Name           string    `json:"name"`
	VLANID         int       `json:"vlan_id"`
	Root           string    `json:"root_uuid"`
	Bridge         Binding   `json:"bridge"`
	BridgeName     string    `json:"bridge_name"`
	Interface      Binding   `json:"interface"`
	Members        []Binding `json:"original_members"`
	LocalPort      Binding   `json:"local_port"`
	LocalInterface Binding   `json:"local_interface"`
	BeforePresent  bool      `json:"before_present"`
	AfterPresent   bool      `json:"after_present"`
}

type InternalPortParent struct {
	Binding                   Binding
	Name, Dependency          string
	Members                   []Binding
	LocalPort, LocalInterface Binding
	Eligible                  bool
}
type InternalPortSnapshot struct {
	Supported, Capacity bool
	Targets             map[string]bool
	Parents             map[string]InternalPortParent
}

func IsGraphOperation(op string) bool { return IsBridgeOperation(op) || op == InternalPortCreate }

func cloneInternalPort(p *InternalPortCreation) *InternalPortCreation {
	if p == nil {
		return nil
	}
	c := *p
	c.Members = append([]Binding{}, p.Members...)
	return &c
}

func internalPortProblem(i StoredIntent, s Snapshot) string {
	p := i.PortCreation
	if p == nil || i.Operation != InternalPortCreate {
		return "INVALID_INTERNAL_PORT"
	}
	if i.Object.Generation != s.Generation || p.Root != s.Creation.Root {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	parent, ok := s.InternalPorts.Parents[p.Bridge.ManagementID]
	if !ok || parent.Binding != p.Bridge || parent.LocalPort != p.LocalPort || parent.LocalInterface != p.LocalInterface {
		return "OBJECT_BINDING_CHANGED"
	}
	if parent.Dependency != i.Dependency {
		return "DEPENDENCY_CHANGED"
	}
	want := append([]Binding{}, p.Members...)
	if p.BeforePresent {
		want = append(want, i.Object)
	}
	sortBindings(want)
	if !slices.Equal(want, parent.Members) {
		return "BRIDGE_PORT_MEMBERSHIP_CHANGED"
	}
	if !p.BeforePresent && p.AfterPresent && s.Creation.Names[p.Name] {
		return "OBJECT_NAME_IN_USE"
	}
	for _, b := range CreationBindings(i) {
		current, exists := s.Creation.Objects[b.OVSUUID]
		if p.BeforePresent && (!exists || current != b) || !p.BeforePresent && exists {
			return "OBJECT_BINDING_CHANGED"
		}
		if !p.BeforePresent && p.AfterPresent && s.Creation.Retired[b.OVSUUID] {
			return "CREATION_IDENTITY_CONSUMED"
		}
	}
	return ""
}
func sortBindings(bindings []Binding) {
	slices.SortFunc(bindings, func(a, b Binding) int {
		if a.OVSUUID < b.OVSUUID {
			return -1
		}
		if a.OVSUUID > b.OVSUUID {
			return 1
		}
		return 0
	})
}

func internalPortChecks(i StoredIntent, s Snapshot) []Gate {
	out := []Gate{}
	p := i.PortCreation
	if p == nil {
		return []Gate{gate("INVALID_INTERNAL_PORT", "blocked", i.ID)}
	}
	if !s.InternalPorts.Supported {
		out = append(out, gate("INTERNAL_PORT_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if !p.BeforePresent && p.AfterPresent && !s.InternalPorts.Capacity {
		out = append(out, gate("INTERNAL_PORT_INVENTORY_CAPACITY", "blocked", i.ID))
	}
	if s.Creation.Authority != "local-managed" || !s.InternalPorts.Targets[p.Bridge.ManagementID+":"+p.Name] {
		out = append(out, gate("INTERNAL_PORT_CREATION_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	if !s.InternalPorts.Parents[p.Bridge.ManagementID].Eligible {
		out = append(out, gate("INTERNAL_PORT_PARENT_UNSUPPORTED", "blocked", i.ID))
	}
	return append(out, gate("INTERNAL_ACCESS_PORT_NO_HOST_IP_CONFIGURATION", "allowed", i.ID))
}

func stageInternalPort(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != InternalPortCreate || !apitypes.ManagementID(cmd.Intents[0].ID) {
		return c, apitypes.Fail(422, "INTERNAL_PORT_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if !ValidBridgeName(in.Name) || in.VLANID < 1 || in.VLANID > 4094 || in.Object.Table != "Bridge" || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_INTERNAL_PORT")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	parent, ok := s.InternalPorts.Parents[in.Object.ManagementID]
	if !ok || parent.Binding != in.Object {
		return c, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if in.Name == parent.Name || len(parent.Members) == 0 || len(parent.Members) > MaxInternalPortParentMembers {
		return c, apitypes.Fail(422, "INTERNAL_PORT_PARENT_UNSUPPORTED")
	}
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
	}
	i := StoredIntent{ID: in.ID, Operation: InternalPortCreate, Object: bind("Port"), Schema: s.Schema, Dependency: parent.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		PortCreation: &InternalPortCreation{Name: in.Name, VLANID: in.VLANID, Root: s.Creation.Root, Bridge: parent.Binding, BridgeName: parent.Name, Interface: bind("Interface"), Members: append([]Binding{}, parent.Members...), LocalPort: parent.LocalPort, LocalInterface: parent.LocalInterface, AfterPresent: true}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != InternalPortCreate || prior.ID != in.ID || prior.PortCreation == nil || prior.PortCreation.Name != in.Name || prior.PortCreation.Bridge != in.Object {
			return c, apitypes.Fail(409, "CREATION_RESTAGE_REQUIRED")
		}
		i = prior
		i.PortCreation = cloneInternalPort(prior.PortCreation)
		i.PortCreation.VLANID = in.VLANID
	}
	if problem := internalPortProblem(i, s); problem != "" {
		return c, apitypes.Fail(409, problem)
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func internalPortDiff(i StoredIntent, s Snapshot) Diff {
	var after any
	current := "absent"
	if p := i.PortCreation; p != nil {
		after = map[string]any{"name": p.Name, "bridge": p.BridgeName, "bridge_id": p.Bridge.ManagementID, "interface_type": "internal", "vlan_mode": "access", "tag": p.VLANID, "interface": p.Interface}
		if s.Creation.Names[p.Name] {
			current = "name occupied"
		}
	}
	return Diff{Object: i.Object, Field: "internal_port_graph", Before: "absent", After: after, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: internalPortProblem(i, s) != ""}
}
