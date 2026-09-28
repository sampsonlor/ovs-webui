package candidate

import (
	"encoding/hex"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const BridgeDelete = "bridge.delete-isolated"

// BridgeGraph has immutable bindings. A compensated deletion creates a NEW
// graph, including new OVS UUIDs; it never resurrects a tombstoned identity.
type BridgeGraph struct {
	Name      string  `json:"name"`
	Root      string  `json:"root_uuid"`
	Bridge    Binding `json:"bridge"`
	Port      Binding `json:"local_port"`
	Interface Binding `json:"local_interface"`
}

func (g BridgeGraph) Bindings() []Binding { return []Binding{g.Bridge, g.Port, g.Interface} }

type BridgeDeletion struct {
	Source       BridgeGraph `json:"source"`
	Replacement  BridgeGraph `json:"replacement"`
	SourceMarker string      `json:"source_marker"`
	// Only private Applied/rollback derivation sets these flags. Public stage
	// accepts a binding, not captured graphs, markers or compensation identities.
	Restoring bool `json:"restoring,omitempty"`
	Observed  bool `json:"observed,omitempty"`
}

type ManagedBridge struct {
	Graph              BridgeGraph
	Marker, Dependency string
	RestoreCapacity    bool
}

type DeletionSnapshot struct {
	AllowedNames map[string]bool
	Graphs       map[string]ManagedBridge
}

func IsBridgeOperation(operation string) bool {
	return operation == BridgeCreate || operation == BridgeDelete
}

func hasDeletion(intents []StoredIntent) bool {
	for _, i := range intents {
		if i.Operation == BridgeDelete {
			return true
		}
	}
	return false
}

// BridgeGraphIntent selects the graph actually affected by a native operation.
// The original deletion intent itself retains the old public object binding.
func BridgeGraphIntent(i StoredIntent) (StoredIntent, bool) {
	if i.Operation == BridgeCreate && i.Creation != nil && i.Deletion == nil {
		return i, true
	}
	if i.Operation != BridgeDelete || i.Deletion == nil || i.Creation != nil {
		return StoredIntent{}, false
	}
	d := i.Deletion
	g := d.Source
	if d.Restoring {
		g = d.Replacement
	}
	before, after := !d.Restoring, d.Restoring
	if d.Observed {
		before = after
	}
	return StoredIntent{ID: i.ID, Operation: i.Operation, Object: g.Bridge,
		Creation: &BridgeCreation{Name: g.Name, Root: g.Root, Port: g.Port, Interface: g.Interface,
			BeforePresent: before, AfterPresent: after}}, true
}

func cloneDeletion(d *BridgeDeletion) *BridgeDeletion {
	if d == nil {
		return nil
	}
	copy := *d
	return &copy
}

func stageDeletion(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != BridgeDelete {
		return c, apitypes.Fail(422, "ISOLATED_BRIDGE_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if !apitypes.ManagementID(in.ID) || in.Object.Table != "Bridge" || in.Name != "" || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_ISOLATED_BRIDGE_DELETION")
	}
	if c.Generation != nil && *c.Generation != s.Generation || in.Object.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	owned, ok := s.Deletion.Graphs[in.Object.ManagementID]
	if !ok || owned.Graph.Bridge != in.Object {
		return c, apitypes.Fail(409, "MANAGED_ISOLATED_BRIDGE_REQUIRED")
	}
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
	}
	i := StoredIntent{ID: in.ID, Operation: BridgeDelete, Object: in.Object, Schema: s.Schema,
		Dependency: owned.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		Deletion: &BridgeDeletion{Source: owned.Graph, SourceMarker: owned.Marker,
			Replacement: BridgeGraph{Name: owned.Graph.Name, Root: owned.Graph.Root, Bridge: bind("Bridge"), Port: bind("Port"), Interface: bind("Interface")}}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != BridgeDelete || prior.ID != i.ID || prior.Object != i.Object || prior.Deletion == nil {
			return c, apitypes.Fail(409, "DELETION_RESTAGE_REQUIRED")
		}
		i = prior
	}
	if problem := deletionProblem(i, s); problem != "" {
		return c, apitypes.Fail(409, problem)
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func deletionProblem(i StoredIntent, s Snapshot) string {
	d := i.Deletion
	if d == nil || i.Operation != BridgeDelete || i.Creation != nil || i.Object != d.Source.Bridge ||
		d.Source.Name != d.Replacement.Name || d.Source.Root != d.Replacement.Root || !validDeletionGraphs(d) {
		return "INVALID_ISOLATED_BRIDGE_DELETION"
	}
	if i.Object.Generation != s.Generation || d.Source.Root != s.Creation.Root {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if !d.Restoring && !d.Observed {
		current, ok := s.Deletion.Graphs[i.Object.ManagementID]
		if !ok || current.Graph != d.Source || current.Marker != d.SourceMarker {
			return "MANAGED_ISOLATED_BRIDGE_REQUIRED"
		}
		if current.Dependency != i.Dependency {
			return "BRIDGE_GRAPH_CHANGED"
		}
	} else {
		for _, b := range d.Source.Bindings() {
			if _, exists := s.Creation.Objects[b.OVSUUID]; exists || !s.Creation.Retired[b.OVSUUID] {
				return "DELETED_IDENTITY_NOT_RETIRED"
			}
		}
		if !(d.Restoring && d.Observed) && s.Creation.Names[d.Source.Name] {
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
	return ""
}

func validDeletionGraphs(d *BridgeDeletion) bool {
	marker, err := hex.DecodeString(d.SourceMarker)
	if err != nil || len(marker) != 32 || !ValidBridgeName(d.Source.Name) || !apitypes.ManagementID(d.Source.Root) {
		return false
	}
	ids := map[string]bool{d.Source.Root: true}
	for _, graph := range []BridgeGraph{d.Source, d.Replacement} {
		for k, b := range graph.Bindings() {
			if b.Table != []string{"Bridge", "Port", "Interface"}[k] || b.Generation != d.Source.Bridge.Generation {
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

func deletionChecks(i StoredIntent, s Snapshot) []Gate {
	out := []Gate{}
	if !s.Creation.Supported {
		out = append(out, gate("BRIDGE_LIFECYCLE_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	d := i.Deletion
	if d == nil || s.Creation.Authority != "local-managed" || !s.Deletion.AllowedNames[d.Source.Name] {
		out = append(out, gate("BRIDGE_DELETION_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	if d != nil && !d.Observed {
		capacity := s.Creation.Capacity
		if !d.Restoring {
			capacity = s.Deletion.Graphs[i.Object.ManagementID].RestoreCapacity
		}
		if !capacity {
			out = append(out, gate("BRIDGE_RESTORATION_CAPACITY_REQUIRED", "blocked", i.ID))
		}
	}
	return append(out, gate("ROLLBACK_RECREATES_WITH_NEW_IDENTITIES", "allowed", i.ID),
		gate("EXACT_ISOLATED_GRAPH_REQUIRED_AT_DISPATCH", "allowed", i.ID))
}

func deletionDiff(i StoredIntent, s Snapshot) Diff {
	var before, after any
	current := "unavailable"
	if d := i.Deletion; d != nil {
		before = d.Source
		after = map[string]any{"state": "absent", "rollback": "recreate with new identities", "replacement": d.Replacement}
		if _, ok := s.Deletion.Graphs[i.Object.ManagementID]; ok {
			current = "managed isolated graph"
		} else if !s.Creation.Names[d.Source.Name] {
			current = "absent"
		} else {
			current = "graph changed or name occupied"
		}
	}
	return Diff{Object: i.Object, Field: "isolated_bridge_deletion", Before: before, After: after,
		Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID,
		Conflict: deletionProblem(i, s) != ""}
}
