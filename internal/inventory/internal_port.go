package inventory

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"slices"
	"strings"
)

// Each root-owned grant binds one existing immutable Bridge ID to one NEW
// internal-port name. A name-recreated Bridge does not inherit this authority.
func internalPortTargets(targets []string) (map[string]bool, error) {
	if len(targets) > 32 {
		return nil, apitypes.Fail(422, "INVALID_INTERNAL_PORT_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, target := range targets {
		id, name, ok := strings.Cut(target, ":")
		if !ok || !apitypes.ManagementID(id) || !candidate.ValidBridgeName(name) || allow[target] {
			return nil, apitypes.Fail(422, "INVALID_INTERNAL_PORT_AUTHORITY")
		}
		allow[target] = true
	}
	return allow, nil
}
func (s *Service) SetLocalInternalPortTargets(targets []string) error {
	allow, err := internalPortTargets(targets)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localInternalPortTargets = allow
	return nil
}

// Parent configuration used both by sealed dependency capture and native
// dispatch guards. Ports membership is checked separately. Interface daemon
// status must not invalidate a pending draft.
func InternalPortParentValues(table string, row Row) map[string]any {
	out := map[string]any{}
	for name, value := range row.Values {
		if table == "Bridge" && name == "ports" {
			continue
		}
		if table == "Interface" && !slices.Contains([]string{"name", "type", "options", "external_ids"}, name) {
			continue
		}
		out[name] = value
	}
	return out
}

func projectInternalPorts(v *view, allow map[string]bool, out *candidate.Snapshot) {
	s := candidate.InternalPortSnapshot{Supported: out.Creation.Supported && v.observation.Schema.InternalPortCreation, Targets: map[string]bool{}, Parents: map[string]candidate.InternalPortParent{}}
	for key, enabled := range allow {
		s.Targets[key] = enabled
	}
	rows := v.observation.Rows
	root := rows["Open_vSwitch"][out.Creation.Root]
	count := 0
	for _, table := range rows {
		count += len(table)
	}
	encoded, err := json.Marshal(rows)
	s.Capacity = err == nil && count+2 <= MaxRows && len(encoded)+2*MaxRowBytes <= MaxSnapshotBytes
	for id, bridge := range rows["Bridge"] {
		b, ok := out.Creation.Objects[id]
		if !ok || b.Table != "Bridge" {
			continue
		}
		name, _ := bridge.Values["name"].(string)
		p := candidate.InternalPortParent{Binding: b, Name: name, Members: []candidate.Binding{}, Eligible: true}
		if !slices.Contains(refs(root.Values["bridges"]), id) || bridge.Values["datapath_type"] != "system" || len(refs(bridge.Values["controller"])) != 0 || bridge.Values["stp_enable"] != false || bridge.Values["rstp_enable"] != false || externalControl(bridge.Values["external_ids"]) {
			p.Eligible = false
		}
		localCount := 0
		var localPort, localInterface Row
		for _, portID := range refs(bridge.Values["ports"]) {
			member, ok := out.Creation.Objects[portID]
			if !ok || member.Table != "Port" {
				p.Eligible = false
				continue
			}
			p.Members = append(p.Members, member)
			port := rows["Port"][portID]
			if port.Values["name"] != name {
				continue
			}
			localCount++
			interfaces := refs(port.Values["interfaces"])
			if len(interfaces) != 1 {
				p.Eligible = false
				continue
			}
			iface := rows["Interface"][interfaces[0]]
			ib, ok := out.Creation.Objects[iface.UUID]
			if !ok || ib.Table != "Interface" || iface.Values["name"] != name || iface.Values["type"] != "internal" || externalControl(port.Values["external_ids"]) || externalControl(iface.Values["external_ids"]) {
				p.Eligible = false
				continue
			}
			p.LocalPort, p.LocalInterface = member, ib
			localPort, localInterface = port, iface
		}
		if localCount != 1 || p.LocalInterface.ManagementID == "" || len(p.Members) > candidate.MaxInternalPortParentMembers+1 {
			p.Eligible = false
		}
		slices.SortFunc(p.Members, func(a, b candidate.Binding) int { return strings.Compare(a.OVSUUID, b.OVSUUID) })
		p.Dependency = Digest([]any{root.UUID, b, p.LocalPort, p.LocalInterface, InternalPortParentValues("Bridge", bridge), InternalPortParentValues("Port", localPort), InternalPortParentValues("Interface", localInterface)})
		s.Parents[b.ManagementID] = p
	}
	out.InternalPorts = s
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, s})
}
