package inventory

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// Names here authorize only fresh isolated graphs, never adoption of existing
// objects. This is root configuration, independent of VLAN/Bond authority.
func (s *Service) SetLocalBridgeNames(names []string) error {
	if len(names) > 32 {
		return apitypes.Fail(422, "INVALID_BRIDGE_CREATION_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, name := range names {
		if !candidate.ValidBridgeName(name) || allow[name] {
			return apitypes.Fail(422, "INVALID_BRIDGE_CREATION_AUTHORITY")
		}
		allow[name] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localBridgeNames = allow
	return nil
}

func projectCreation(v *view, allow map[string]bool, out *candidate.Snapshot) {
	c := candidate.CreationSnapshot{Root: v.observation.Evidence.Root, Supported: v.observation.Schema.BridgeCreation, Authority: "local-managed", AllowedNames: map[string]bool{}, Names: map[string]bool{}, Objects: map[string]candidate.Binding{}, Retired: map[string]bool{}}
	for name, enabled := range allow {
		c.AllowedNames[name] = enabled
	}
	for id, retired := range v.decision.Retired {
		c.Retired[id] = retired
	}
	root := v.observation.Rows["Open_vSwitch"][c.Root]
	if len(v.observation.Rows["Open_vSwitch"]) != 1 || root.UUID == "" {
		c.Supported = false
	}
	if externalControl(root.Values["external_ids"]) {
		c.Authority = "externally-controlled"
	}
	for _, table := range []string{"Bridge", "Port", "Interface"} {
		for id, row := range v.observation.Rows[table] {
			name, ok := row.Values["name"].(string)
			if !ok {
				c.Supported = false
			}
			c.Names[name] = true
			b := v.decision.Bindings[Key(table, id)]
			if b.State != "active" {
				c.Supported = false
				continue
			}
			c.Objects[id] = candidate.Binding{ManagementID: b.ManagementID, OVSUUID: id, Table: table, Generation: out.Generation}
		}
	}
	out.Creation = c
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, c})
}
