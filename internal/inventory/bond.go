package inventory

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// This independent, root-owned policy never derives bond authority from VLAN
// authority, a UI mode, a display name or a client-supplied claim.
func (s *Service) SetLocalBondPorts(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_BOND_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_BOND_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localBond = allow
	return nil
}

func optionalString(row Row, name string) (*string, bool) {
	v, exists := row.Values[name]
	if !exists {
		return nil, false
	}
	if list, ok := v.([]any); ok {
		if len(list) == 0 {
			return nil, true
		}
		if len(list) != 1 {
			return nil, false
		}
		v = list[0]
	}
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	return &s, true
}

func projectBond(v *view, row, bridge Row, local bool, p *candidate.Port) {
	p.BondAuthority = "unknown"
	if local {
		p.BondAuthority = "local-managed"
	}
	if p.Authority == "externally-controlled" {
		p.BondAuthority = "externally-controlled"
	}
	var lacpOK, modeOK bool
	p.Bond.LACP, lacpOK = optionalString(row, "lacp")
	p.Bond.Mode, modeOK = optionalString(row, "bond_mode")
	config, mapOK := row.Values["other_config"].(map[string]any)
	fallbackOK := true
	if value, exists := config["lacp-fallback-ab"]; exists {
		text, ok := value.(string)
		fallbackOK = ok
		if ok {
			p.Bond.Fallback = &text
		}
	}
	p.BondKnown = lacpOK && modeOK && mapOK && fallbackOK && bridge.UUID != ""
	p.MemberKindsSupported = true
	p.Members = []string{}
	members := map[string]any{}
	for _, id := range refs(row.Values["interfaces"]) {
		member, exists := v.observation.Rows["Interface"][id]
		binding := v.decision.Bindings[Key("Interface", id)]
		parents := 0
		for _, port := range v.observation.Rows["Port"] {
			if slices.Contains(refs(port.Values["interfaces"]), id) {
				parents++
			}
		}
		if !exists || binding.State != "active" || parents != 1 {
			p.BondKnown = false
		}
		p.Members = append(p.Members, binding.ManagementID)
		kind, known := member.Values["type"].(string)
		if !known || !slices.Contains([]string{"", "system", "dummy"}, kind) {
			p.MemberKindsSupported = false
		}
		if kind == "internal" {
			p.LocalPort = true
		}
		members[id] = []any{binding.ManagementID, member.Values["name"], kind, member.Values["options"], parents}
	}
	slices.Sort(p.Members)
	parents := 0
	for _, b := range v.observation.Rows["Bridge"] {
		if slices.Contains(refs(b.Values["ports"]), row.UUID) {
			parents++
		}
	}
	if parents != 1 || len(p.Members) == 0 || len(slices.Compact(append([]string{}, p.Members...))) != len(p.Members) {
		p.BondKnown = false
	}
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	if !slices.Contains(refs(root.Values["bridges"]), bridge.UUID) {
		p.BondKnown = false
	}
	if row.Values["name"] == bridge.Values["name"] {
		p.LocalPort = true
	}
	var stpOK, rstpOK, floodOK bool
	p.STP, stpOK = bridge.Values["stp_enable"].(bool)
	p.RSTP, rstpOK = bridge.Values["rstp_enable"].(bool)
	p.FloodVLANs, floodOK = integers(bridge.Values["flood_vlans"])
	p.BondKnown = p.BondKnown && stpOK && rstpOK && floodOK
	compatible := 0
	for _, table := range v.observation.Schema.Tables {
		if table.Name == "Port" {
			for _, c := range table.Columns {
				if c.Monitored && c.BondCompatible {
					compatible++
				}
			}
		}
	}
	p.BondSupported = compatible == 3
	p.BondDependency = Digest([]any{root.UUID, bridge.UUID, bridge.Values["name"], bridge.Values["datapath_type"], p.STP, p.RSTP, p.FloodVLANs, row.Values["name"], members, p.BondAuthority})
}
