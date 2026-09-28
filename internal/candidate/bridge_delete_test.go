package candidate

import (
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func deletionFixture(t *testing.T) (Envelope, Snapshot, Command) {
	t.Helper()
	s := bridgeSnapshot()
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
	}
	graph := BridgeGraph{Name: "br-new", Root: s.Creation.Root, Bridge: bind("Bridge"), Port: bind("Port"), Interface: bind("Interface")}
	s.Deletion = DeletionSnapshot{AllowedNames: map[string]bool{"br-new": true}, Graphs: map[string]ManagedBridge{
		graph.Bridge.ManagementID: {Graph: graph, Marker: strings.Repeat("a", 64), Dependency: "unchanged", RestoreCapacity: true},
	}}
	s.Creation.Names[graph.Name] = true
	for _, b := range graph.Bindings() {
		s.Creation.Objects[b.OVSUUID] = b
	}
	cmd := Command{Operation: "stage", Intents: []Intent{{ID: repository.NewID(), Operation: BridgeDelete, Object: graph.Bridge}}}
	e, err := Prepare(Envelope{Candidate: Candidate{ID: repository.NewID(), Revision: repository.NewID()}}, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	return e, s, cmd
}

func TestDeletionCapturesGraphAndFreshRecoveryIdentities(t *testing.T) {
	e, s, cmd := deletionFixture(t)
	i := e.Candidate.Intents[0]
	if !validDeletionGraphs(i.Deletion) || len(Bindings(e.Candidate)) != 6 {
		t.Fatal("incomplete distinct binding set")
	}
	if checks, _ := Checks(e.Candidate, s); !Passed(checks) {
		t.Fatal(checks)
	}
	again, err := Prepare(e, cmd, s)
	if err != nil || Digest(again.Candidate.Intents) != Digest(e.Candidate.Intents) {
		t.Fatal("restage changed captured identities", err)
	}
	for _, b := range i.Deletion.Source.Bindings() {
		delete(s.Creation.Objects, b.OVSUUID)
		s.Creation.Retired[b.OVSUUID] = true
	}
	delete(s.Creation.Names, "br-new")
	delete(s.Deletion.Graphs, i.Object.ManagementID)
	after := i
	AfterImage(&after)
	if p := deletionProblem(after, s); p != "" {
		t.Fatal(p)
	}
	restore := i
	Reverse(&restore)
	if i.Deletion.Restoring || !restore.Deletion.Restoring || restore.Object != i.Object {
		t.Fatal("original checkpoint was mutated")
	}
	if checks, _ := Checks(Candidate{Intents: []StoredIntent{restore}}, s); !Passed(checks) {
		t.Fatal(checks)
	}
	native, ok := BridgeGraphIntent(restore)
	if !ok || native.Object != i.Deletion.Replacement.Bridge || native.Creation.BeforePresent || !native.Creation.AfterPresent {
		t.Fatal(native)
	}
	for _, b := range i.Deletion.Replacement.Bindings() {
		s.Creation.Objects[b.OVSUUID] = b
	}
	s.Creation.Names["br-new"] = true
	AfterImage(&restore)
	if p := deletionProblem(restore, s); p != "" {
		t.Fatal(p)
	}
	// A new identity never changes the old object's identity or tombstone.
	delete(s.Creation.Retired, i.Object.OVSUUID)
	if p := deletionProblem(restore, s); p != "DELETED_IDENTITY_NOT_RETIRED" {
		t.Fatal(p)
	}
}

func TestDeletionRejectsAuthorityDriftCapacityAndForgedGraphs(t *testing.T) {
	for _, name := range []string{"authority", "schema", "capacity", "dependency", "provenance", "name", "reused-id", "wrong-table", "generation"} {
		t.Run(name, func(t *testing.T) {
			e, s, _ := deletionFixture(t)
			i := &e.Candidate.Intents[0]
			owned := s.Deletion.Graphs[i.Object.ManagementID]
			switch name {
			case "authority":
				s.Deletion.AllowedNames = nil
			case "schema":
				s.Creation.Supported = false
			case "capacity":
				owned.RestoreCapacity = false
			case "dependency":
				owned.Dependency = "changed"
			case "provenance":
				owned.Marker = strings.Repeat("b", 64)
			case "name":
				i.Deletion.Replacement.Name = "different"
			case "reused-id":
				i.Deletion.Replacement.Port.OVSUUID = i.Deletion.Source.Port.OVSUUID
			case "wrong-table":
				i.Deletion.Replacement.Port.Table = "Bridge"
			case "generation":
				i.Deletion.Replacement.Port.Generation = repository.NewID()
			}
			s.Deletion.Graphs[i.Object.ManagementID] = owned
			if checks, _ := Checks(e.Candidate, s); Passed(checks) {
				t.Fatal("unsafe deletion passed", name)
			}
		})
	}
	e, s, cmd := deletionFixture(t)
	cmd.Intents = append(cmd.Intents, Intent{ID: repository.NewID(), Operation: BridgeCreate, Name: "mixed"})
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("mixed operation accepted")
	}
	if _, err := Prepare(e, Command{Operation: "rebase"}, s); err == nil {
		t.Fatal("deletion silently rebased")
	}
	Reverse(&e.Candidate.Intents[0])
	for _, b := range e.Candidate.Intents[0].Deletion.Source.Bindings() {
		delete(s.Creation.Objects, b.OVSUUID)
		s.Creation.Retired[b.OVSUUID] = true
	}
	if p := deletionProblem(e.Candidate.Intents[0], s); p != "OBJECT_NAME_IN_USE" {
		t.Fatal("same-name object adopted", p)
	}
}
