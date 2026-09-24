package candidate

import (
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func bondFixture() (Envelope, Snapshot, Intent) {
	e, s, i := planFixture()
	p := s.Ports[i.Object.ManagementID]
	p.BondKnown, p.BondSupported, p.MemberKindsSupported = true, true, true
	p.BondAuthority, p.BondDependency = "local-managed", "native-member-bindings"
	p.Members = []string{repository.NewID(), repository.NewID()}
	slices.Sort(p.Members)
	s.Ports[i.Object.ManagementID] = p
	i.Operation, i.Value, i.Mode, i.LACP, i.Fallback, i.Members = "bond.configure", VLAN{}, "balance-tcp", "active", "enabled", append([]string{}, p.Members...)
	return e, s, i
}

func TestBondCandidatePreservesNativeDefaultsOriginalsAndExplicitRebase(t *testing.T) {
	e, s, i := bondFixture()
	e = staged(t, e, s, i)
	checks, diff := Checks(e.Candidate, s)
	if !Passed(checks) || len(diff) != 3 || !slices.Equal(Capabilities(e.Candidate), []string{"ovs.port.bond.write"}) {
		t.Fatal(checks, diff)
	}
	original := e.Candidate.Intents[0]
	if original.BeforeBond.Mode != nil || original.BeforeBond.LACP != nil || original.BeforeBond.Fallback != nil {
		t.Fatal("native absence normalized", original)
	}
	p := s.Ports[i.Object.ManagementID]
	p.Bond.LACP, p.Bond.Fallback = ptr("passive"), ptr("false")
	s.Ports[i.Object.ManagementID], s.Revision = p, "external-change"
	i.LACP, i.Fallback = "passive", "preserve"
	e = staged(t, e, s, i)
	if e.Candidate.Intents[0].BeforeBond.LACP != nil || *e.Candidate.Intents[0].Bond.Fallback != "true" {
		t.Fatal("edit silently recaptured originals or desired fallback")
	}
	v := Compare(e.Candidate, s)
	if v.State != "conflict" || !hasGate(v.Checks, "FIELD_CONFLICT") {
		t.Fatal(v)
	}
	command := Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: v.ConflictSnapshot}
	if _, err := Prepare(e, command, s); err == nil {
		t.Fatal("implicit conflict resolution")
	}
	command.Resolutions = []Resolution{{IntentID: i.ID, Choice: "keep-mine"}}
	rebased, err := Prepare(e, command, s)
	if err != nil {
		t.Fatal(err)
	}
	if *rebased.Candidate.Intents[0].BeforeBond.Fallback != "false" || *rebased.Candidate.Intents[0].Bond.Fallback != "true" {
		t.Fatal("lost reviewed fields")
	}
	copy := rebased.Candidate.Intents[0]
	Reverse(&copy)
	if *copy.Bond.Fallback != "false" || *copy.BeforeBond.Fallback != "true" {
		t.Fatal("incorrect field compensation")
	}
	AfterImage(&copy)
	if Digest(copy.Bond) != Digest(copy.BeforeBond) {
		t.Fatal("after-image mismatch")
	}
}

func TestLACPIsAPortPropertyWithoutMembershipChanges(t *testing.T) {
	e, s, i := bondFixture()
	p := s.Ports[i.Object.ManagementID]
	p.Members = p.Members[:1]
	s.Ports[i.Object.ManagementID] = p
	i.Operation, i.Mode, i.Members = "port.lacp.set", "", nil
	e = staged(t, e, s, i)
	checks, _ := Checks(e.Candidate, s)
	if !Passed(checks) || e.Candidate.Intents[0].Bond.Mode != nil {
		t.Fatal("single-Port LACP changed bond mode", checks)
	}
	e, s, i = bondFixture()
	i.Members[0] = repository.NewID()
	if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
		t.Fatal("stole/replaced member")
	}
	e, s, i = bondFixture()
	i.Members[1] = i.Members[0]
	if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
		t.Fatal("duplicate member accepted")
	}
}

func TestBondValidationRejectsUnprovenNativeAndUnsafeCombinations(t *testing.T) {
	tests := []struct {
		name, code string
		mutate     func(*Port, *Intent)
	}{
		{"tcp without LACP", "BALANCE_TCP_REQUIRES_LACP", func(p *Port, i *Intent) { i.LACP, i.Fallback = "off", "disabled" }},
		{"fallback without LACP", "LACP_FALLBACK_REQUIRES_LACP", func(p *Port, i *Intent) { i.Mode, i.LACP = "active-backup", "off" }},
		{"SLB flood VLANs", "SLB_FLOOD_VLANS_INCOMPATIBLE", func(p *Port, i *Intent) {
			i.Mode, i.LACP, i.Fallback, p.FloodVLANs = "balance-slb", "off", "disabled", []int{20}
		}},
		{"STP", "BOND_SPANNING_TREE_UNSUPPORTED", func(p *Port, i *Intent) { p.STP = true }},
		{"RSTP", "BOND_SPANNING_TREE_UNSUPPORTED", func(p *Port, i *Intent) { p.RSTP = true }},
		{"internal", "LOCAL_INTERNAL_PORT_PROTECTED", func(p *Port, i *Intent) { p.LocalPort = true }},
		{"advanced member", "BOND_MEMBER_TYPE_UNSUPPORTED", func(p *Port, i *Intent) { p.MemberKindsSupported = false }},
		{"ownership", "OWNERSHIP_UNKNOWN", func(p *Port, i *Intent) { p.BondAuthority = "unknown" }},
		{"external", "EXTERNALLY_CONTROLLED", func(p *Port, i *Intent) { p.BondAuthority = "externally-controlled" }},
		{"schema", "BOND_SCHEMA_UNSUPPORTED", func(p *Port, i *Intent) { p.BondSupported = false }},
		{"future native mode", "NATIVE_BOND_SEMANTICS_UNPROVEN", func(p *Port, i *Intent) { p.Bond.Mode = ptr("future-mode") }},
		{"future native fallback", "NATIVE_BOND_SEMANTICS_UNPROVEN", func(p *Port, i *Intent) { p.Bond.Fallback = ptr("future-value") }},
		{"unsafe original TCP", "ORIGINAL_BOND_CONFIGURATION_UNPROVEN", func(p *Port, i *Intent) { p.Bond.Mode = ptr("balance-tcp") }},
		{"unsafe original fallback", "ORIGINAL_BOND_CONFIGURATION_UNPROVEN", func(p *Port, i *Intent) { p.Bond.Fallback = ptr("true") }},
		{"unsafe original SLB", "ORIGINAL_BOND_CONFIGURATION_UNPROVEN", func(p *Port, i *Intent) { p.Bond.Mode, p.FloodVLANs = ptr("balance-slb"), []int{20} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e, s, i := bondFixture()
			p := s.Ports[i.Object.ManagementID]
			test.mutate(&p, &i)
			s.Ports[i.Object.ManagementID] = p
			e = staged(t, e, s, i)
			checks, _ := Checks(e.Candidate, s)
			if Passed(checks) || !hasGate(checks, test.code) {
				t.Fatal(checks)
			}
		})
	}
	e, s, i := bondFixture()
	e = staged(t, e, s, i)
	p := s.Ports[i.Object.ManagementID]
	p.BondDependency = "member-replaced-with-same-name"
	s.Ports[i.Object.ManagementID] = p
	checks, _ := Checks(e.Candidate, s)
	if !hasGate(checks, "DEPENDENCY_CHANGED") {
		t.Fatal(checks)
	}
	p.Members = append([]string{}, p.Members...)
	p.Members[0] = repository.NewID()
	slices.Sort(p.Members)
	s.Ports[i.Object.ManagementID] = p
	v := Compare(e.Candidate, s)
	if !hasGate(v.Checks, "BOND_MEMBER_BINDINGS_CHANGED") {
		t.Fatal(v)
	}
	_, err := Prepare(e, Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: v.ConflictSnapshot, Resolutions: []Resolution{{IntentID: i.ID, Choice: "keep-mine"}}}, s)
	if err == nil {
		t.Fatal("field rebase silently rebound different members")
	}
	p.BondKnown = false
	s.Ports[i.Object.ManagementID] = p
	if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
		t.Fatal("unknown native fields accepted")
	}
}
