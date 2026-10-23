package candidate

import (
	"slices"
	"testing"
)

func TestIsolationKeepsIndependentAuthoritySealedOriginalAndSafeCompensation(t *testing.T) {
	e, s, in := planFixture()
	in.Operation, in.Value, in.Protected = PortIsolationSet, VLAN{}, ptr(true)
	p := IsolationPort{Binding: in.Object, Known: true, Supported: true, Authority: "local-managed", Dependency: "whole-bridge"}
	s.Isolations = map[string]IsolationPort{in.Object.ManagementID: p}
	st := staged(t, e, s, in)
	checks, diff := Checks(st.Candidate, s)
	if !Passed(checks) || len(diff) != 1 || diff[0].Field != "protected" || !slices.Equal(Capabilities(st.Candidate), []string{PortIsolationCapability}) {
		t.Fatal(checks, diff, Capabilities(st.Candidate))
	}
	original := Digest(st.Candidate.Intents[0])
	reverse := st.Candidate.Intents[0]
	Reverse(&reverse)
	if reverse.Isolation.Before != true || reverse.Isolation.After != false || Digest(st.Candidate.Intents[0]) != original {
		t.Fatal("compensation changed immutable original")
	}
	Reverse(&reverse)
	if Digest(reverse) != original {
		t.Fatal("round trip")
	}
	after := st.Candidate.Intents[0]
	AfterImage(&after)
	if !after.Isolation.Before || Digest(st.Candidate.Intents[0]) != original {
		t.Fatal("after-image mutated checkpoint")
	}
	for _, fault := range []string{"root-grant", "schema", "unknown", "graph", "dependency", "field", "binding", "generation"} {
		t.Run(fault, func(t *testing.T) {
			changed := s
			p2 := p
			switch fault {
			case "root-grant":
				p2.Authority = "unknown"
			case "schema":
				p2.Supported = false
			case "unknown":
				p2.Known = false
			case "graph":
				p2.Gate = "ISOLATION_FOREIGN_DOMAIN_UNQUALIFIED"
			case "dependency":
				p2.Dependency = "changed"
			case "field":
				p2.Protected = true
			case "binding":
				p2.Binding.OVSUUID = "retired"
			case "generation":
				changed.Generation = "changed"
			}
			changed.Isolations = map[string]IsolationPort{in.Object.ManagementID: p2}
			checks, _ := Checks(st.Candidate, changed)
			if Passed(checks) {
				t.Fatal("fault retained validation", fault)
			}
		})
	}
	p.Protected = true
	s.Isolations[in.Object.ManagementID] = p
	in.Protected = ptr(false)
	edited := staged(t, st, s, in)
	if edited.Candidate.Intents[0].Isolation.Before {
		t.Fatal("edit replaced original")
	}
	if Passed(Compare(edited.Candidate, s).Checks) {
		t.Fatal("conflict hidden")
	}
	for _, bad := range []Intent{func() Intent { i := in; i.Protected = nil; return i }(), func() Intent { i := in; i.Value = VLAN{Tag: ptr(10)}; return i }(), func() Intent { i := in; i.Operation = "isolation.configure"; return i }()} {
		if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{bad}}, s); err == nil {
			t.Fatal("invalid input admitted")
		}
	}
}
