package inventory

import (
	"encoding/json"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// Deletion grants are independent from creation, VLAN and Bond authority.
func (s *Service) SetLocalInternalPortDeleteTargets(targets []string) error {
	allow, err := internalPortTargets(targets)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localInternalPortDeleteTargets = allow
	return nil
}

func projectInternalPortDeletion(v *view, allow map[string]bool, out *candidate.Snapshot) {
	s := candidate.InternalPortDeletionSnapshot{Targets: map[string]bool{}, Graphs: map[string]candidate.ManagedInternalPort{}}
	for key, value := range allow {
		s.Targets[key] = value
	}
	rows := v.observation.Rows
	encoded, encodingErr := json.Marshal(rows)
	count := 0
	for _, table := range rows {
		count += len(table)
	}
	for _, parent := range out.InternalPorts.Parents {
		if !parent.Eligible {
			continue
		}
		for _, portBinding := range parent.Members {
			port := rows["Port"][portBinding.OVSUUID]
			name, _ := port.Values["name"].(string)
			interfaces := refs(port.Values["interfaces"])
			vlan, ok := nativeVLAN(port)
			if !candidate.ValidBridgeName(name) || name == parent.Name || portBinding == parent.LocalPort || len(interfaces) != 1 || !ok || vlan.Mode == nil || *vlan.Mode != "access" || vlan.Tag == nil || *vlan.Tag < 1 || *vlan.Tag > 4094 || len(vlan.Trunks) != 0 || len(vlan.CVLANs) != 0 {
				continue
			}
			iface := rows["Interface"][interfaces[0]]
			ib, ok := out.Creation.Objects[iface.UUID]
			if !ok || ib.Table != "Interface" || iface.Values["name"] != name || iface.Values["type"] != "internal" {
				continue
			}
			marker, owned, bytesRemoved := "", true, 0
			for _, b := range []candidate.Binding{portBinding, ib} {
				private := v.decision.Bindings[Key(b.Table, b.OVSUUID)]
				row := rows[b.Table][b.OVSUUID]
				labels, _ := row.Values["external_ids"].(map[string]any)
				if private.State != "active" || len(private.CreationMarker) != 64 || len(labels) != 1 || labels[candidate.BridgeCreationMarker] != private.CreationMarker || marker != "" && marker != private.CreationMarker {
					owned = false
					break
				}
				marker = private.CreationMarker
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
			members := slices.DeleteFunc(append([]candidate.Binding{}, parent.Members...), func(b candidate.Binding) bool { return b == portBinding })
			graph := candidate.InternalPortGraph{Port: portBinding, Configuration: candidate.InternalPortCreation{Name: name, VLANID: *vlan.Tag, Root: out.Creation.Root, Bridge: parent.Binding, BridgeName: parent.Name, Interface: ib, Members: members, LocalPort: parent.LocalPort, LocalInterface: parent.LocalInterface}}
			dependency := Digest([]any{port.Values, InternalPortParentValues("Interface", iface)})
			s.Graphs[portBinding.ManagementID] = candidate.ManagedInternalPort{Graph: graph, Marker: marker, Dependency: dependency, RestoreCapacity: encodingErr == nil && count <= MaxRows && len(encoded)-bytesRemoved+2*MaxRowBytes <= MaxSnapshotBytes}
		}
	}
	out.PortDeletions = s
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, s})
}
