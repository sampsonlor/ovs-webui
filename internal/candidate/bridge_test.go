package candidate

import (
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func bridgeSnapshot() Snapshot {
	return Snapshot{Generation: repository.NewID(), Schema: "schema", Revision: "revision", Creation: CreationSnapshot{Root: repository.NewID(), Authority: "local-managed", Supported: true, AllowedNames: map[string]bool{"br-new": true}, Names: map[string]bool{}, Objects: map[string]Binding{}, Retired: map[string]bool{}}}
}
func TestIsolatedBridgeDraftIdentityAndNoAdoption(t *testing.T) {
	s := bridgeSnapshot()
	e := Envelope{Candidate: Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []StoredIntent{}}}
	cmd := Command{Operation: "stage", Intents: []Intent{{ID: repository.NewID(), Operation: BridgeCreate, Name: "br-new"}}}
	next, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	checks, diff := Checks(next.Candidate, s)
	if !Passed(checks) || len(diff) != 1 {
		t.Fatal(checks, diff)
	}
	old := next.Candidate.Intents[0]
	for _, b := range CreationBindings(old) {
		if b.Generation != s.Generation || b.ManagementID == b.OVSUUID {
			t.Fatal(b)
		}
	}
	again, err := Prepare(next, cmd, s)
	if err != nil || Digest(again.Candidate.Intents) != Digest(next.Candidate.Intents) {
		t.Fatal("edit replaced identity", err)
	}
	cmd.Intents[0].Name = "renamed"
	if _, err = Prepare(next, cmd, s); err == nil {
		t.Fatal("silent rename")
	}
	s.Creation.Names["br-new"] = true
	if _, err = Prepare(e, Command{Operation: "stage", Intents: []Intent{{ID: repository.NewID(), Operation: BridgeCreate, Name: "br-new"}}}, s); err == nil {
		t.Fatal("adopted name")
	}
	delete(s.Creation.Names, "br-new")
	s.Creation.Retired[old.Object.OVSUUID] = true
	checks, _ = Checks(next.Candidate, s)
	if Passed(checks) {
		t.Fatal("resurrected retired identity")
	}
}

func TestIsolatedBridgeChecksAndCompensationCopies(t *testing.T) {
	s := bridgeSnapshot()
	i, err := newBridgeIntent(Intent{Operation: BridgeCreate, ID: repository.NewID(), Name: "br-new"}, s)
	if err != nil {
		t.Fatal(err)
	}
	c := Candidate{Intents: []StoredIntent{i}}
	s.Creation.AllowedNames = nil
	if checks, _ := Checks(c, s); Passed(checks) {
		t.Fatal("root authority missing")
	}
	s.Creation.AllowedNames = map[string]bool{"br-new": true}
	s.Creation.Supported = false
	if checks, _ := Checks(c, s); Passed(checks) {
		t.Fatal("unsupported schema")
	}
	s.Creation.Supported = true
	for _, b := range CreationBindings(i) {
		s.Creation.Objects[b.OVSUUID] = b
	}
	r := i
	Reverse(&r)
	if i.Creation.BeforePresent || !i.Creation.AfterPresent || !r.Creation.BeforePresent || r.Creation.AfterPresent {
		t.Fatal("mutated durable original")
	}
	if p := creationProblem(r, s); p != "" {
		t.Fatal(p)
	}
	b := s.Creation.Objects[i.Object.OVSUUID]
	b.ManagementID = repository.NewID()
	s.Creation.Objects[i.Object.OVSUUID] = b
	if p := creationProblem(r, s); p != "OBJECT_BINDING_CHANGED" {
		t.Fatal("rebound identity", p)
	}
}
