package candidate

import (
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"slices"
	"testing"
)

func internalPortFixture(t *testing.T) (Snapshot, Envelope, Command) {
	t.Helper()
	s := bridgeSnapshot()
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Generation: s.Generation, Table: table}
	}
	p := InternalPortParent{Binding: bind("Bridge"), Name: "br-parent", Dependency: "parent-original", LocalPort: bind("Port"), LocalInterface: bind("Interface"), Eligible: true}
	p.Members = []Binding{p.LocalPort}
	s.InternalPorts = InternalPortSnapshot{Supported: true, Capacity: true, Targets: map[string]bool{p.Binding.ManagementID + ":pi-new": true}, Parents: map[string]InternalPortParent{p.Binding.ManagementID: p}}
	e := Envelope{Candidate: Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []StoredIntent{}}}
	cmd := Command{Operation: "stage", Intents: []Intent{{ID: repository.NewID(), Operation: InternalPortCreate, Object: p.Binding, Name: "pi-new", VLANID: 20}}}
	return s, e, cmd
}

func TestInternalPortIdentityRestageAndCompensation(t *testing.T) {
	s, e, cmd := internalPortFixture(t)
	staged, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	checks, diff := Checks(staged.Candidate, s)
	if !Passed(checks) || len(diff) != 1 || !slices.Equal(Capabilities(staged.Candidate), []string{"ovs.port.internal.create"}) {
		t.Fatal(checks, diff)
	}
	i := staged.Candidate.Intents[0]
	if len(CreationBindings(i)) != 2 || i.Object.Table != "Port" || i.PortCreation.Interface.Table != "Interface" {
		t.Fatal(i)
	}
	cmd.Intents[0].VLANID = 21
	again, err := Prepare(staged, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(CreationBindings(i), CreationBindings(again.Candidate.Intents[0])) || i.PortCreation.VLANID != 20 || again.Candidate.Intents[0].PortCreation.VLANID != 21 {
		t.Fatal("restage changed identity/original")
	}
	p := s.InternalPorts.Parents[i.PortCreation.Bridge.ManagementID]
	p.Members = append(p.Members, i.Object)
	sortBindings(p.Members)
	s.InternalPorts.Parents[p.Binding.ManagementID] = p
	for _, b := range CreationBindings(i) {
		s.Creation.Objects[b.OVSUUID] = b
	}
	s.Creation.Names[i.PortCreation.Name] = true
	r := i
	Reverse(&r)
	s.InternalPorts.Capacity = false
	if checks, _ := Checks(Candidate{Intents: []StoredIntent{r}}, s); !Passed(checks) {
		t.Fatal("cleanup blocked", checks)
	}
	if i.PortCreation.BeforePresent || !i.PortCreation.AfterPresent {
		t.Fatal("reverse modified original")
	}
	AfterImage(&r)
	if r.PortCreation.BeforePresent || r.PortCreation.AfterPresent {
		t.Fatal("wrong cleanup image")
	}
}

func TestInternalPortRejectsDriftAdoptionAndImplicitAuthority(t *testing.T) {
	for _, change := range []string{"name", "parent-id", "members", "local-id", "dependency", "generation", "schema", "retired", "root-grant", "ownership", "capacity", "schema-support", "parent-policy"} {
		t.Run(change, func(t *testing.T) {
			s, e, cmd := internalPortFixture(t)
			e, err := Prepare(e, cmd, s)
			if err != nil {
				t.Fatal(err)
			}
			i := e.Candidate.Intents[0]
			p := s.InternalPorts.Parents[i.PortCreation.Bridge.ManagementID]
			switch change {
			case "name":
				s.Creation.Names["pi-new"] = true
			case "parent-id":
				p.Binding.ManagementID = repository.NewID()
			case "members":
				p.Members = append(p.Members, Binding{OVSUUID: repository.NewID()})
			case "local-id":
				p.LocalInterface.ManagementID = repository.NewID()
			case "dependency":
				p.Dependency = "changed"
			case "generation":
				s.Generation = repository.NewID()
			case "schema":
				s.Schema = "changed"
			case "retired":
				s.Creation.Retired[i.Object.OVSUUID] = true
			case "root-grant":
				s.InternalPorts.Targets = nil
			case "ownership":
				s.Creation.Authority = "externally-controlled"
			case "capacity":
				s.InternalPorts.Capacity = false
			case "schema-support":
				s.InternalPorts.Supported = false
			case "parent-policy":
				p.Eligible = false
			}
			s.InternalPorts.Parents[i.PortCreation.Bridge.ManagementID] = p
			if checks, _ := Checks(e.Candidate, s); Passed(checks) {
				t.Fatal("unsafe draft passed", change)
			}
		})
	}
	for _, value := range []int{0, 4095, -1} {
		s, e, cmd := internalPortFixture(t)
		cmd.Intents[0].VLANID = value
		if _, err := Prepare(e, cmd, s); err == nil {
			t.Fatal("invalid VLAN")
		}
	}
	s, e, cmd := internalPortFixture(t)
	cmd.Intents[0].Name = "br-parent"
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("local-port adoption")
	}
	s, e, cmd = internalPortFixture(t)
	e, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Intents[0].Name = "renamed"
	if _, err = Prepare(e, cmd, s); err == nil {
		t.Fatal("silent rename")
	}
}
