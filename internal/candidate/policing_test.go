package candidate

import (
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func policingFixture() (Envelope, Snapshot, Command) {
	e, s, cmd := mtuFixture()
	m := s.Interfaces[cmd.Intents[0].Object.ManagementID]
	s.Policings = map[string]InterfacePolicing{m.Binding.ManagementID: {Binding: m.Binding, Port: m.Port, Bridge: m.Bridge, Name: "synthetic-pi", Type: "internal", IfIndex: 7, Known: true, Supported: true, Eligible: true, Authority: "local-exclusive", Dependency: "ingress-attachment"}}
	cmd.Intents[0].Operation, cmd.Intents[0].MTURequest = InterfacePolicingSet, 0
	cmd.Intents[0].Policing = &PolicingRequest{Mode: "bandwidth", Rate: 1000}
	return e, s, cmd
}
func TestPolicingSealsOriginalDependenciesAndIndependentCapability(t *testing.T) {
	e, s, cmd := policingFixture()
	staged, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	checks, diff := Checks(staged.Candidate, s)
	if !Passed(checks) || len(diff) != 4 || !slices.Equal(Capabilities(staged.Candidate), []string{"ovs.interface.policing.write"}) || len(Bindings(staged.Candidate)) != 3 {
		t.Fatal(checks, diff)
	}
	original := staged.Candidate.Intents[0].Policing
	p := s.Policings[cmd.Intents[0].Object.ManagementID]
	p.Configuration.Rate = 1800
	s.Policings[p.Binding.ManagementID] = p
	cmd.Intents[0].Policing = &PolicingRequest{Mode: "packets", Rate: 10}
	edited, err := Prepare(staged, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Candidate.Intents[0].Policing.Before != (PolicingConfig{}) || original.After.Rate != 1000 || Compare(edited.Candidate, s).State != "reconciliation-required" {
		t.Fatal("original replaced")
	}
	if _, err = Prepare(staged, Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: Compare(staged.Candidate, s).ConflictSnapshot}, s); err == nil {
		t.Fatal("silently rebased")
	}
	i := staged.Candidate.Intents[0]
	Reverse(&i)
	if !i.Policing.Compensating || i.Policing.Before.Rate != 1000 || i.Policing.After != (PolicingConfig{}) || original.Compensating {
		t.Fatal(i)
	}
	AfterImage(&i)
	if i.Policing.Before != (PolicingConfig{}) {
		t.Fatal(i)
	}
}
func TestPolicingModesBoundsAndMixedInputsFailClosed(t *testing.T) {
	for _, request := range []PolicingRequest{{"disabled", 0}, {"bandwidth", 1}, {"bandwidth", 1000000}, {"packets", 1}, {"packets", 1000}} {
		e, s, cmd := policingFixture()
		cmd.Intents[0].Policing = &request
		if _, err := Prepare(e, cmd, s); err != nil {
			t.Fatal(request, err)
		}
	}
	for _, request := range []*PolicingRequest{nil, {"disabled", 1}, {"bandwidth", 0}, {"bandwidth", 1000001}, {"packets", 1001}, {"packets", -1}, {"combined", 10}} {
		e, s, cmd := policingFixture()
		cmd.Intents[0].Policing = request
		if _, err := Prepare(e, cmd, s); err == nil {
			t.Fatal(request)
		}
	}
	for _, kind := range []string{"mtu", "native-vlan", "name", "multiple", "other-operation"} {
		e, s, cmd := policingFixture()
		switch kind {
		case "mtu":
			cmd.Intents[0].MTURequest = 1500
		case "native-vlan":
			n := 20
			cmd.Intents[0].Value.Tag = &n
		case "name":
			cmd.Intents[0].Name = "host-device"
		case "multiple":
			cmd.Intents = append(cmd.Intents, cmd.Intents[0])
		case "other-operation":
			cmd.Intents[0].Operation = InterfaceMTUSet
		}
		if _, err := Prepare(e, cmd, s); err == nil {
			t.Fatal(kind)
		}
	}
}
func TestPolicingReadOnlyContextsAndDriftInvalidateValidation(t *testing.T) {
	for _, kind := range []string{"unknown", "schema", "local-member", "authority", "custom-burst", "both-rates", "ifindex", "parents", "generation", "dependency"} {
		e, s, cmd := policingFixture()
		staged, err := Prepare(e, cmd, s)
		if err != nil {
			t.Fatal(err)
		}
		p := s.Policings[cmd.Intents[0].Object.ManagementID]
		switch kind {
		case "unknown":
			p.Known = false
		case "schema":
			p.Supported = false
		case "local-member":
			p.Eligible = false
		case "authority":
			p.Authority = "externally-controlled"
		case "custom-burst":
			p.Configuration.Burst = 8000
		case "both-rates":
			p.Configuration.Rate = 100
			p.Configuration.PacketRate = 1
		case "ifindex":
			p.IfIndex++
		case "parents":
			p.Port.OVSUUID = repository.NewID()
		case "generation":
			s.Generation = repository.NewID()
		case "dependency":
			p.Dependency = "changed"
		}
		s.Policings[p.Binding.ManagementID] = p
		checks, _ := Checks(staged.Candidate, s)
		if Passed(checks) {
			t.Fatal(kind, checks)
		}
	}
}
