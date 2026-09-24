package candidate

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

// Bond is a field group on a native Port, not a separately owned OVS object.
// Nil preserves an absent native value, including the absent fallback map key.
type Bond struct {
	LACP     *string `json:"lacp"`
	Mode     *string `json:"bond_mode"`
	Fallback *string `json:"lacp_fallback_ab"`
}

func IsBondOperation(op string) bool { return op == "bond.configure" || op == "port.lacp.set" }

func Capabilities(c Candidate) []string {
	out := []string{}
	for _, i := range c.Intents {
		cap := "unsupported-intent"
		if i.Operation == "port.vlan.set" {
			cap = "ovs.port.vlan.write"
		}
		if IsBondOperation(i.Operation) {
			cap = "ovs.port.bond.write"
		}
		if !slices.Contains(out, cap) {
			out = append(out, cap)
		}
	}
	slices.Sort(out)
	return out
}

func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
func CloneBond(b *Bond) *Bond {
	if b == nil {
		return nil
	}
	return &Bond{copyString(b.LACP), copyString(b.Mode), copyString(b.Fallback)}
}
func nativeChoice(value *string, allowed ...string) bool {
	return value == nil || slices.Contains(allowed, *value)
}
func bondNativeKnown(b *Bond) bool {
	return b != nil && nativeChoice(b.LACP, "off", "active", "passive") && nativeChoice(b.Mode, "active-backup", "balance-slb", "balance-tcp") && nativeChoice(b.Fallback, "true", "false")
}

func bondModes(b *Bond) (lacp, mode string, fallback bool) {
	lacp, mode = "off", "active-backup"
	if b.LACP != nil {
		lacp = *b.LACP
	}
	if b.Mode != nil {
		mode = *b.Mode
	}
	if b.Fallback != nil {
		fallback = *b.Fallback == "true"
	}
	return
}
func bondDesired(in Intent, p Port) (*Bond, error) {
	if !slices.Contains([]string{"off", "active", "passive"}, in.LACP) || !slices.Contains([]string{"", "preserve", "enabled", "disabled", "default"}, in.Fallback) || Digest(in.Value) != Digest(VLAN{}) {
		return nil, apitypes.Fail(422, "UNSUPPORTED_CONFIGURATION")
	}
	if !p.BondKnown {
		return nil, apitypes.Fail(409, "NATIVE_CONFIGURATION_UNKNOWN")
	}
	if in.Operation == "bond.configure" {
		if !slices.Contains([]string{"active-backup", "balance-slb", "balance-tcp"}, in.Mode) || len(in.Members) < 2 || len(in.Members) > 32 {
			return nil, apitypes.Fail(422, "INVALID_BOND_CONFIGURATION")
		}
		members := append([]string{}, in.Members...)
		slices.Sort(members)
		if len(slices.Compact(append([]string{}, members...))) != len(members) || !slices.Equal(members, p.Members) {
			return nil, apitypes.Fail(409, "BOND_MEMBERSHIP_CHANGE_UNAVAILABLE")
		}
	} else if in.Mode != "" || len(in.Members) != 0 {
		return nil, apitypes.Fail(422, "INVALID_LACP_CONFIGURATION")
	}
	after := CloneBond(&p.Bond)
	after.LACP = copyString(&in.LACP)
	if in.Operation == "bond.configure" {
		after.Mode = copyString(&in.Mode)
	}
	switch in.Fallback {
	case "enabled":
		value := "true"
		after.Fallback = &value
	case "disabled":
		value := "false"
		after.Fallback = &value
	case "default":
		after.Fallback = nil
	}
	return after, nil
}

func bondChecks(i StoredIntent, p Port) []Gate {
	out := []Gate{}
	block := func(code string) { out = append(out, gate(code, "blocked", i.ID)) }
	if !p.BondSupported {
		block("BOND_SCHEMA_UNSUPPORTED")
	}
	if p.BondAuthority != "local-managed" {
		if p.BondAuthority == "externally-controlled" {
			block("EXTERNALLY_CONTROLLED")
		} else {
			block("OWNERSHIP_UNKNOWN")
		}
	}
	if !bondNativeKnown(i.BeforeBond) || !bondNativeKnown(i.Bond) {
		block("NATIVE_BOND_SEMANTICS_UNPROVEN")
		return out
	}
	oldLACP, oldMode, oldFallback := bondModes(i.BeforeBond)
	if oldFallback && oldLACP == "off" || len(p.Members) > 1 && (oldMode == "balance-tcp" && oldLACP == "off" || oldMode == "balance-slb" && len(p.FloodVLANs) != 0) {
		block("ORIGINAL_BOND_CONFIGURATION_UNPROVEN")
	}
	if p.LocalPort {
		block("LOCAL_INTERNAL_PORT_PROTECTED")
	}
	if !p.MemberKindsSupported {
		block("BOND_MEMBER_TYPE_UNSUPPORTED")
	}
	if len(p.Members) == 0 || i.Operation == "bond.configure" && len(p.Members) < 2 {
		block("BOND_MEMBER_COUNT_CHANGED")
	}
	lacp, mode, fallback := bondModes(i.Bond)
	if len(p.Members) > 1 && mode == "balance-tcp" && lacp == "off" {
		block("BALANCE_TCP_REQUIRES_LACP")
	}
	if fallback && lacp == "off" {
		block("LACP_FALLBACK_REQUIRES_LACP")
	}
	if len(p.Members) > 1 && mode == "balance-slb" && len(p.FloodVLANs) != 0 {
		block("SLB_FLOOD_VLANS_INCOMPATIBLE")
	}
	// OVS spanning-tree support excludes bonded ports. Do not invent a safe
	// effective configuration while that incompatible bridge policy is enabled.
	if len(p.Members) > 1 && (p.STP || p.RSTP) {
		block("BOND_SPANNING_TREE_UNSUPPORTED")
	}
	if lacp != "off" {
		out = append(out, gate("LACP_PARTNER_AND_MANAGEMENT_PATH_REVIEW_REQUIRED", "allowed", i.ID))
	}
	return out
}

// Inventory hints use the same native safety boundary as validation. An
// unsupported original must remain observable without advertising edit access.
func BondFieldsEditable(p Port) bool {
	return p.BondKnown && Passed(bondChecks(StoredIntent{Operation: "port.lacp.set", BeforeBond: &p.Bond, Bond: &p.Bond}, p))
}

func currentOriginal(i *StoredIntent, p Port) {
	if IsBondOperation(i.Operation) {
		i.BeforeBond = CloneBond(&p.Bond)
		i.Before = normalize(VLAN{})
		i.Dependency = p.BondDependency
	} else {
		i.Before = normalize(p.VLAN)
		i.Dependency = p.Dependency
	}
}

// AfterImage and Reverse are also used by read-only Applied proof and guarded
// compensation; neither can change an object binding or member set.
func AfterImage(i *StoredIntent) {
	if IsBondOperation(i.Operation) {
		i.BeforeBond = CloneBond(i.Bond)
	} else {
		i.Before = i.Value
	}
}
func Reverse(i *StoredIntent) {
	if IsBondOperation(i.Operation) {
		i.BeforeBond, i.Bond = CloneBond(i.Bond), CloneBond(i.BeforeBond)
	} else {
		i.Before, i.Value = i.Value, i.Before
	}
}
