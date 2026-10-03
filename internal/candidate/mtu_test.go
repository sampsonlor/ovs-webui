package candidate

import (
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func mtuFixture() (Envelope, Snapshot, Command) {
	g := repository.NewID()
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: g}
	}
	p := InterfaceMTU{Binding: bind("Interface"), Port: bind("Port"), Bridge: bind("Bridge"), Requested: 1500, Known: true, Supported: true, Eligible: true, Authority: "local-managed", Dependency: "attachment"}
	s := Snapshot{Generation: g, Revision: "current", Schema: "schema", Interfaces: map[string]InterfaceMTU{p.Binding.ManagementID: p}}
	e := Envelope{Candidate: Candidate{ID: repository.NewID(), Revision: repository.NewID(), State: "empty", Intents: []StoredIntent{}}}
	cmd := Command{Operation: "stage", Intents: []Intent{{ID: repository.NewID(), Operation: InterfaceMTUSet, Object: p.Binding, MTURequest: 2000}}}
	return e, s, cmd
}
func TestInterfaceMTUSealsOriginalAndReversesExactRequest(t *testing.T) {
	e, s, cmd := mtuFixture()
	e, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	checks, diff := Checks(e.Candidate, s)
	if !Passed(checks) || len(diff) != 1 || diff[0].Before != 1500 || diff[0].After != 2000 || !slices.Equal(Capabilities(e.Candidate), []string{"ovs.interface.mtu.write"}) {
		t.Fatal(checks, diff)
	}
	p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
	p.Requested = 1800
	s.Interfaces[p.Binding.ManagementID] = p
	cmd.Intents[0].MTURequest = 2200
	edited, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Candidate.Intents[0].MTU.Before != 1500 || e.Candidate.Intents[0].MTU.After != 2000 || Compare(edited.Candidate, s).Diff[0].Current != 1800 || Compare(edited.Candidate, s).State != "reconciliation-required" {
		t.Fatal("original replaced")
	}
	i := e.Candidate.Intents[0]
	Reverse(&i)
	if i.MTU.Before != 2000 || i.MTU.After != 1500 || e.Candidate.Intents[0].MTU.Before != 1500 {
		t.Fatal("compensation changed original")
	}
	AfterImage(&i)
	if i.MTU.Before != 1500 {
		t.Fatal("after-image incorrect")
	}
	if _, err := Prepare(e, Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: Compare(e.Candidate, s).ConflictSnapshot}, s); err == nil {
		t.Fatal("MTU silently rebased")
	}
}
func TestInterfaceMTURejectsDefaultsMixedIntentsAndChangedBindings(t *testing.T) {
	for _, kind := range []string{"empty-request", "range", "unknown", "schema", "ownership", "external", "local-or-bond", "generation", "binding", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			e, s, cmd := mtuFixture()
			p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
			switch kind {
			case "empty-request":
				p.Requested = 0
			case "range":
				cmd.Intents[0].MTURequest = 65536
			case "unknown":
				p.Known = false
			case "schema":
				p.Supported = false
			case "ownership":
				p.Authority = "unknown"
			case "external":
				p.Authority = "externally-controlled"
			case "local-or-bond":
				p.Eligible = false
			case "generation":
				cmd.Intents[0].Object.Generation = repository.NewID()
			case "binding":
				cmd.Intents[0].Object.OVSUUID = repository.NewID()
			case "mixed":
				cmd.Intents = append(cmd.Intents, Intent{ID: repository.NewID(), Operation: "port.vlan.set"})
			}
			s.Interfaces[p.Binding.ManagementID] = p
			if _, err := Prepare(e, cmd, s); err == nil {
				t.Fatal("unsupported MTU admitted")
			}
		})
	}
	for _, kind := range []string{"attachment", "field", "generation", "schema", "authority"} {
		t.Run("sealed-"+kind, func(t *testing.T) {
			e, s, cmd := mtuFixture()
			e, err := Prepare(e, cmd, s)
			if err != nil {
				t.Fatal(err)
			}
			p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
			switch kind {
			case "attachment":
				p.Port.ManagementID = repository.NewID()
			case "field":
				p.Requested = 1800
			case "generation":
				s.Generation = repository.NewID()
			case "schema":
				s.Schema = "changed"
			case "authority":
				p.Authority = "unknown"
			}
			s.Interfaces[p.Binding.ManagementID] = p
			checks, _ := Checks(e.Candidate, s)
			if Passed(checks) {
				t.Fatal("drift admitted")
			}
		})
	}
}
