package candidate

import (
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func portDeleteFixture(t *testing.T) (Snapshot, Envelope, Command) {
	s, e, create := internalPortFixture(t)
	staged, err := Prepare(e, create, s)
	if err != nil {
		t.Fatal(err)
	}
	i := staged.Candidate.Intents[0]
	p := *cloneInternalPort(i.PortCreation)
	p.BeforePresent, p.AfterPresent = false, false
	parent := s.InternalPorts.Parents[p.Bridge.ManagementID]
	parent.Members = append(parent.Members, i.Object)
	sortBindings(parent.Members)
	s.InternalPorts.Parents[p.Bridge.ManagementID] = parent
	for _, b := range CreationBindings(i) {
		s.Creation.Objects[b.OVSUUID] = b
	}
	s.Creation.Names[p.Name] = true
	s.PortDeletions = InternalPortDeletionSnapshot{Targets: map[string]bool{p.Bridge.ManagementID + ":" + p.Name: true}, Graphs: map[string]ManagedInternalPort{i.Object.ManagementID: {Graph: InternalPortGraph{Port: i.Object, Configuration: p}, Marker: strings.Repeat("a", 64), Dependency: "child-original", RestoreCapacity: true}}}
	return s, e, Command{Operation: "stage", Intents: []Intent{{ID: repository.NewID(), Operation: InternalPortDelete, Object: i.Object}}}
}

func TestInternalPortDeletionFreshCompensationAndImmutableOriginal(t *testing.T) {
	s, e, cmd := portDeleteFixture(t)
	e, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	i := e.Candidate.Intents[0]
	d := i.PortDeletion
	original := Digest(i)
	if checks, _ := Checks(e.Candidate, s); !Passed(checks) {
		t.Fatal(checks)
	}
	if !slices.Equal(Capabilities(e.Candidate), []string{"ovs.port.internal.delete"}) {
		t.Fatal(Capabilities(e.Candidate))
	}
	again, err := Prepare(e, cmd, s)
	if err != nil || Digest(again.Candidate.Intents[0]) != original {
		t.Fatal("restage replaced captured identities", err)
	}
	for _, b := range d.Source.Bindings() {
		delete(s.Creation.Objects, b.OVSUUID)
		s.Creation.Retired[b.OVSUUID] = true
	}
	delete(s.PortDeletions.Graphs, i.Object.ManagementID)
	delete(s.Creation.Names, d.Source.Configuration.Name)
	parent := s.InternalPorts.Parents[d.Source.Configuration.Bridge.ManagementID]
	parent.Members = append([]Binding{}, d.Source.Configuration.Members...)
	s.InternalPorts.Parents[parent.Binding.ManagementID] = parent
	r := i
	AfterImage(&r)
	if checks, _ := Checks(Candidate{Intents: []StoredIntent{r}}, s); !Passed(checks) {
		t.Fatal("delete after image", checks)
	}
	Reverse(&r)
	graph, ok := InternalPortGraphIntent(r)
	if !ok || graph.Object != d.Replacement.Port || graph.PortCreation.Interface != d.Replacement.Configuration.Interface || graph.PortCreation.BeforePresent || !graph.PortCreation.AfterPresent {
		t.Fatal(graph)
	}
	if checks, _ := Checks(Candidate{Intents: []StoredIntent{r}}, s); !Passed(checks) {
		t.Fatal("restoration preflight", checks)
	}
	for _, b := range d.Replacement.Bindings() {
		s.Creation.Objects[b.OVSUUID] = b
	}
	parent.Members = append(parent.Members, d.Replacement.Port)
	sortBindings(parent.Members)
	s.InternalPorts.Parents[parent.Binding.ManagementID] = parent
	s.Creation.Names[d.Source.Configuration.Name] = true
	AfterImage(&r)
	if checks, _ := Checks(Candidate{Intents: []StoredIntent{r}}, s); !Passed(checks) {
		t.Fatal("restored after image", checks)
	}
	if Digest(i) != original {
		t.Fatal("derived compensation mutated original")
	}
}

func TestInternalPortDeletionRefusesDriftAndImplicitAuthority(t *testing.T) {
	for _, change := range []string{"ownership", "marker", "child-config", "parent-config", "parent-members", "root-grant", "parent-grant", "schema", "generation", "capacity", "reused-identity"} {
		t.Run(change, func(t *testing.T) {
			s, e, cmd := portDeleteFixture(t)
			e, err := Prepare(e, cmd, s)
			if err != nil {
				t.Fatal(err)
			}
			i := e.Candidate.Intents[0]
			owned := s.PortDeletions.Graphs[i.Object.ManagementID]
			parent := s.InternalPorts.Parents[owned.Graph.Configuration.Bridge.ManagementID]
			switch change {
			case "ownership":
				owned.Graph.Port.ManagementID = repository.NewID()
			case "marker":
				owned.Marker = strings.Repeat("b", 64)
			case "child-config":
				owned.Dependency = "changed"
			case "parent-config":
				parent.Dependency = "changed"
			case "parent-members":
				parent.Members = append(parent.Members, Binding{OVSUUID: repository.NewID()})
			case "root-grant":
				s.PortDeletions.Targets = nil
			case "parent-grant":
				parent.Eligible = false
			case "schema":
				s.Schema = "changed"
			case "generation":
				s.Generation = repository.NewID()
			case "capacity":
				owned.RestoreCapacity = false
			case "reused-identity":
				e.Candidate.Intents[0].PortDeletion.Replacement.Port = i.Object
			}
			s.PortDeletions.Graphs[i.Object.ManagementID] = owned
			s.InternalPorts.Parents[parent.Binding.ManagementID] = parent
			if checks, _ := Checks(e.Candidate, s); Passed(checks) {
				t.Fatal("unsafe deletion passed")
			}
		})
	}
	s, e, cmd := portDeleteFixture(t)
	cmd.Intents[0].VLANID = 20
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("write field accepted")
	}
	cmd.Intents[0].VLANID = 0
	e, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(e, Command{Operation: "rebase"}, s); err == nil {
		t.Fatal("deletion rebased")
	}
}
