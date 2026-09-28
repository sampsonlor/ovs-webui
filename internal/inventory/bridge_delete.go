package inventory

import (
	"encoding/json"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// This independent root gate never adopts an external/same-name Bridge.
// Ownership comes from the private durable registry, not OVS external_ids.
func (s *Service) SetLocalBridgeDeleteNames(names []string) error {
	if len(names) > 32 {
		return apitypes.Fail(422, "INVALID_BRIDGE_DELETION_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, name := range names {
		if !candidate.ValidBridgeName(name) || allow[name] {
			return apitypes.Fail(422, "INVALID_BRIDGE_DELETION_AUTHORITY")
		}
		allow[name] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localBridgeDeleteNames = allow
	return nil
}

func projectDeletion(v *view, allow map[string]bool, out *candidate.Snapshot) {
	s := candidate.DeletionSnapshot{AllowedNames: map[string]bool{}, Graphs: map[string]candidate.ManagedBridge{}}
	for name, enabled := range allow {
		s.AllowedNames[name] = enabled
	}
	rows := v.observation.Rows
	root := rows["Open_vSwitch"][out.Creation.Root]
	encoded, encodingErr := json.Marshal(rows)
	count := 0
	for _, table := range rows {
		count += len(table)
	}
	for id, bridge := range rows["Bridge"] {
		name, _ := bridge.Values["name"].(string)
		ports := refs(bridge.Values["ports"])
		if !candidate.ValidBridgeName(name) || len(ports) != 1 || !slices.Contains(refs(root.Values["bridges"]), id) ||
			bridge.Values["datapath_type"] != "system" {
			continue
		}
		port := rows["Port"][ports[0]]
		interfaces := refs(port.Values["interfaces"])
		if port.Values["name"] != name || len(interfaces) != 1 {
			continue
		}
		iface := rows["Interface"][interfaces[0]]
		if iface.Values["name"] != name || iface.Values["type"] != "internal" {
			continue
		}
		graph := candidate.BridgeGraph{Name: name, Root: root.UUID}
		bindings := []*candidate.Binding{&graph.Bridge, &graph.Port, &graph.Interface}
		tables, ids := []string{"Bridge", "Port", "Interface"}, []string{id, port.UUID, iface.UUID}
		marker, owned, bytesRemoved := "", true, 0
		for k, table := range tables {
			b := v.decision.Bindings[Key(table, ids[k])]
			row := rows[table][ids[k]]
			labels, _ := row.Values["external_ids"].(map[string]any)
			if b.State != "active" || len(b.CreationMarker) != 64 || len(labels) != 1 ||
				labels[candidate.BridgeCreationMarker] != b.CreationMarker || marker != "" && marker != b.CreationMarker {
				owned = false
				break
			}
			marker = b.CreationMarker
			*bindings[k] = candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: table, Generation: out.Generation}
			bits, err := json.Marshal(row)
			if err != nil {
				owned = false
				break
			}
			bytesRemoved += len(bits)
		}
		if !owned {
			continue
		}
		// Daemon status changes do not rebase deletion intent. All configuration
		// columns, including unmonitored ones, get exact native guards at dispatch.
		dependency := Digest([]any{bridge.Values, port.Values, map[string]any{
			"name": iface.Values["name"], "type": iface.Values["type"], "options": iface.Values["options"],
			"external_ids": iface.Values["external_ids"]}})
		s.Graphs[graph.Bridge.ManagementID] = candidate.ManagedBridge{Graph: graph, Marker: marker, Dependency: dependency,
			RestoreCapacity: encodingErr == nil && count <= MaxRows && len(encoded)-bytesRemoved+3*MaxRowBytes <= MaxSnapshotBytes}
	}
	out.Deletion = s
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, s})
}
