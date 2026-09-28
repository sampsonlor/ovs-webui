package candidate

import (
	"encoding/hex"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const InternalPortDelete = "port.delete-internal"

// Parent configuration is preserved. Only the two child identities differ
// between a deleted graph and its pre-reserved compensation graph.
type InternalPortGraph struct {
	Port          Binding              `json:"port"`
	Configuration InternalPortCreation `json:"configuration"`
}

func (g InternalPortGraph) Bindings() []Binding { return []Binding{g.Port, g.Configuration.Interface} }

type InternalPortDeletion struct {
	Source           InternalPortGraph `json:"source"`
	Replacement      InternalPortGraph `json:"replacement"`
	SourceMarker     string            `json:"source_marker"`
	SourceDependency string            `json:"source_dependency"`
	Restoring        bool              `json:"restoring,omitempty"`
	Observed         bool              `json:"observed,omitempty"`
}
type ManagedInternalPort struct {
	Graph              InternalPortGraph
	Marker, Dependency string
	RestoreCapacity    bool
}
type InternalPortDeletionSnapshot struct {
	Targets map[string]bool
	Graphs  map[string]ManagedInternalPort
}

func clonePortDeletion(d *InternalPortDeletion) *InternalPortDeletion {
	if d == nil {
		return nil
	}
	c := *d
	c.Source.Configuration = *cloneInternalPort(&d.Source.Configuration)
	c.Replacement.Configuration = *cloneInternalPort(&d.Replacement.Configuration)
	return &c
}

// Select the child pair for native execution without changing the historical
// object binding on the public deletion intent.
func InternalPortGraphIntent(i StoredIntent) (StoredIntent, bool) {
	if i.Operation == InternalPortCreate && i.PortCreation != nil && i.PortDeletion == nil {
		return i, true
	}
	if i.Operation != InternalPortDelete || i.PortDeletion == nil || i.PortCreation != nil {
		return StoredIntent{}, false
	}
	d := i.PortDeletion
	g := d.Source
	if d.Restoring {
		g = d.Replacement
	}
	p := cloneInternalPort(&g.Configuration)
	p.BeforePresent, p.AfterPresent = !d.Restoring, d.Restoring
	if d.Observed {
		p.BeforePresent = p.AfterPresent
	}
	return StoredIntent{ID: i.ID, Operation: InternalPortCreate, Object: g.Port, Schema: i.Schema, Dependency: i.Dependency, PortCreation: p}, true
}

func stagePortDeletion(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != InternalPortDelete {
		return c, apitypes.Fail(422, "INTERNAL_PORT_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if !apitypes.ManagementID(in.ID) || in.Object.Table != "Port" || in.Name != "" || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_INTERNAL_PORT_DELETION")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	owned, ok := s.PortDeletions.Graphs[in.Object.ManagementID]
	if !ok || owned.Graph.Port != in.Object {
		return c, apitypes.Fail(409, "MANAGED_INTERNAL_PORT_REQUIRED")
	}
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
	}
	replacement := owned.Graph
	replacement.Port = bind("Port")
	replacement.Configuration = *cloneInternalPort(&owned.Graph.Configuration)
	replacement.Configuration.Interface = bind("Interface")
	i := StoredIntent{ID: in.ID, Operation: InternalPortDelete, Object: in.Object, Schema: s.Schema,
		Dependency: s.InternalPorts.Parents[owned.Graph.Configuration.Bridge.ManagementID].Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		PortDeletion: &InternalPortDeletion{Source: owned.Graph, Replacement: replacement, SourceMarker: owned.Marker, SourceDependency: owned.Dependency}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != InternalPortDelete || prior.ID != i.ID || prior.Object != i.Object || prior.PortDeletion == nil {
			return c, apitypes.Fail(409, "DELETION_RESTAGE_REQUIRED")
		}
		i = prior
	}
	if problem := portDeletionProblem(i, s); problem != "" {
		return c, apitypes.Fail(409, problem)
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func validPortDeletion(d *InternalPortDeletion) bool {
	marker, err := hex.DecodeString(d.SourceMarker)
	p := d.Source.Configuration
	if err != nil || len(marker) != 32 || !ValidBridgeName(p.Name) || p.Name == p.BridgeName || p.VLANID < 1 || p.VLANID > 4094 || !apitypes.ManagementID(p.Root) || len(p.Members) == 0 || len(p.Members) > MaxInternalPortParentMembers {
		return false
	}
	want := *cloneInternalPort(&p)
	want.Interface = d.Replacement.Configuration.Interface
	if Digest(want) != Digest(d.Replacement.Configuration) || p.BeforePresent || p.AfterPresent {
		return false
	}
	ids := map[string]bool{p.Root: true}
	for _, b := range append([]Binding{p.Bridge, p.LocalPort, p.LocalInterface}, p.Members...) {
		ids[b.ManagementID], ids[b.OVSUUID] = true, true
	}
	for _, graph := range []InternalPortGraph{d.Source, d.Replacement} {
		for k, b := range graph.Bindings() {
			if b.Table != []string{"Port", "Interface"}[k] || b.Generation != p.Bridge.Generation {
				return false
			}
			for _, id := range []string{b.ManagementID, b.OVSUUID} {
				if !apitypes.ManagementID(id) || ids[id] {
					return false
				}
				ids[id] = true
			}
		}
	}
	return true
}

func portDeletionProblem(i StoredIntent, s Snapshot) string {
	d := i.PortDeletion
	if d == nil || i.Operation != InternalPortDelete || i.PortCreation != nil || i.Creation != nil || i.Deletion != nil || i.Object != d.Source.Port || !validPortDeletion(d) {
		return "INVALID_INTERNAL_PORT_DELETION"
	}
	if !d.Restoring && !d.Observed {
		owned, ok := s.PortDeletions.Graphs[i.Object.ManagementID]
		if !ok || Digest(owned.Graph) != Digest(d.Source) || owned.Marker != d.SourceMarker {
			return "MANAGED_INTERNAL_PORT_REQUIRED"
		}
		if owned.Dependency != d.SourceDependency {
			return "INTERNAL_PORT_GRAPH_CHANGED"
		}
	} else {
		for _, b := range d.Source.Bindings() {
			if _, exists := s.Creation.Objects[b.OVSUUID]; exists || !s.Creation.Retired[b.OVSUUID] {
				return "DELETED_IDENTITY_NOT_RETIRED"
			}
		}
		if !(d.Restoring && d.Observed) && s.Creation.Names[d.Source.Configuration.Name] {
			return "OBJECT_NAME_IN_USE"
		}
	}
	for _, b := range d.Replacement.Bindings() {
		current, exists := s.Creation.Objects[b.OVSUUID]
		if d.Restoring && d.Observed {
			if !exists || current != b {
				return "OBJECT_BINDING_CHANGED"
			}
		} else if exists || s.Creation.Retired[b.OVSUUID] {
			return "CREATION_IDENTITY_CONSUMED"
		}
	}
	graph, _ := InternalPortGraphIntent(i)
	return internalPortProblem(graph, s)
}

func portDeletionChecks(i StoredIntent, s Snapshot) []Gate {
	d := i.PortDeletion
	if d == nil {
		return []Gate{gate("INVALID_INTERNAL_PORT_DELETION", "blocked", i.ID)}
	}
	p := d.Source.Configuration
	out := []Gate{}
	if !s.InternalPorts.Supported {
		out = append(out, gate("INTERNAL_PORT_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if s.Creation.Authority != "local-managed" || !s.PortDeletions.Targets[p.Bridge.ManagementID+":"+p.Name] {
		out = append(out, gate("INTERNAL_PORT_DELETION_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	if !s.InternalPorts.Parents[p.Bridge.ManagementID].Eligible {
		out = append(out, gate("INTERNAL_PORT_PARENT_UNSUPPORTED", "blocked", i.ID))
	}
	if !d.Observed {
		capacity := s.InternalPorts.Capacity
		if !d.Restoring {
			capacity = s.PortDeletions.Graphs[i.Object.ManagementID].RestoreCapacity
		}
		if !capacity {
			out = append(out, gate("INTERNAL_PORT_RESTORATION_CAPACITY_REQUIRED", "blocked", i.ID))
		}
	}
	return append(out, gate("ROLLBACK_RECREATES_WITH_NEW_IDENTITIES", "allowed", i.ID), gate("EXACT_INTERNAL_PORT_GRAPH_REQUIRED_AT_DISPATCH", "allowed", i.ID))
}

func portDeletionDiff(i StoredIntent, s Snapshot) Diff {
	var before, after any
	current := "unavailable"
	if d := i.PortDeletion; d != nil {
		before = d.Source
		after = map[string]any{"state": "absent", "rollback": "recreate with new identities", "replacement": d.Replacement}
		if _, ok := s.PortDeletions.Graphs[i.Object.ManagementID]; ok {
			current = "managed internal access Port"
		} else if !s.Creation.Names[d.Source.Configuration.Name] {
			current = "absent"
		} else {
			current = "graph changed or name occupied"
		}
	}
	return Diff{Object: i.Object, Field: "internal_port_deletion", Before: before, After: after, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: portDeletionProblem(i, s) != ""}
}

func hasPortDeletion(intents []StoredIntent) bool {
	return slices.ContainsFunc(intents, func(i StoredIntent) bool { return i.Operation == InternalPortDelete })
}
