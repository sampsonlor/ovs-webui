package candidate

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func mtuFixture() (Envelope, Snapshot, Command) {
	g := repository.NewID()
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: g}
	}
	p := InterfaceMTU{Binding: bind("Interface"), Port: bind("Port"), Bridge: bind("Bridge"), Requested: MTUPointer(1500), Known: true, Supported: true, Eligible: true, Authority: "local-managed", Dependency: "attachment"}
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
	p.Requested = MTUPointer(1800)
	s.Interfaces[p.Binding.ManagementID] = p
	cmd.Intents[0].MTURequest = 2200
	edited, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if *edited.Candidate.Intents[0].MTU.Before != 1500 || *e.Candidate.Intents[0].MTU.After != 2000 || Compare(edited.Candidate, s).Diff[0].Current != 1800 || Compare(edited.Candidate, s).State != "reconciliation-required" {
		t.Fatal("original replaced")
	}
	i := e.Candidate.Intents[0]
	Reverse(&i)
	if *i.MTU.Before != 2000 || *i.MTU.After != 1500 || *e.Candidate.Intents[0].MTU.Before != 1500 {
		t.Fatal("compensation changed original")
	}
	AfterImage(&i)
	if *i.MTU.Before != 1500 {
		t.Fatal("after-image incorrect")
	}
	if _, err := Prepare(e, Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: Compare(e.Candidate, s).ConflictSnapshot}, s); err == nil {
		t.Fatal("MTU silently rebased")
	}
}

func TestMTUDefaultTransitionsPreserveNativeEmptyRequestAndSealedDependencies(t *testing.T) {
	for _, clear := range []bool{false, true} {
		e, s, cmd := mtuFixture()
		p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
		p.Default = &MTUDefault{MTU: 1800, Dependency: "proven-peers", Bindings: []Binding{p.Port}}
		p.Observed = 1500
		if clear {
			cmd.Intents[0].Operation = InterfaceMTUClear
			cmd.Intents[0].MTURequest = 0
		} else {
			p.Requested = nil
			p.Observed = 1800
		}
		s.Interfaces[p.Binding.ManagementID] = p
		staged, err := Prepare(e, cmd, s)
		if err != nil {
			t.Fatal(err)
		}
		checks, diff := Checks(staged.Candidate, s)
		if !Passed(checks) || len(diff) != 2 || clear && diff[0].After != nil || !clear && diff[0].Before != nil {
			t.Fatal(checks, diff)
		}
		i := staged.Candidate.Intents[0]
		Reverse(&i)
		if clear {
			if i.Operation != InterfaceMTUSet || i.MTU.Before != nil || *i.MTU.After != 1500 {
				t.Fatal(i)
			}
		} else if i.Operation != InterfaceMTUClear || i.MTU.After != nil || ExpectedMTU(i.MTU) != 1800 {
			t.Fatal(i)
		}
		context := *p.Default
		context.Dependency = "external-peer-change"
		p.Default = &context
		s.Interfaces[p.Binding.ManagementID] = p
		checks, _ = Checks(staged.Candidate, s)
		if Passed(checks) {
			t.Fatal("peer drift inherited old validation")
		}
		if staged.Candidate.Intents[0].MTU.Default.Dependency != "proven-peers" {
			t.Fatal("sealed context replaced")
		}
	}
}

func TestMTUDefaultsRequireProvenStableValuesAndModeEditsCannotReplaceOriginal(t *testing.T) {
	for _, kind := range []string{"unproven-empty", "unstable-empty", "clear-unproven", "clear-unapplied", "zero-not-empty"} {
		e, s, cmd := mtuFixture()
		p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
		p.Default = &MTUDefault{MTU: 1800, Dependency: "peers", Bindings: []Binding{p.Port}}
		p.Observed = 1500
		switch kind {
		case "unproven-empty":
			p.Requested = nil
			p.Default = nil
		case "unstable-empty":
			p.Requested = nil
		case "clear-unproven":
			cmd.Intents[0].Operation = InterfaceMTUClear
			cmd.Intents[0].MTURequest = 0
			p.Default = nil
		case "clear-unapplied":
			cmd.Intents[0].Operation = InterfaceMTUClear
			cmd.Intents[0].MTURequest = 0
			p.Observed = 1400
		case "zero-not-empty":
			p.Requested = MTUPointer(0)
		}
		s.Interfaces[p.Binding.ManagementID] = p
		if _, err := Prepare(e, cmd, s); err == nil {
			t.Fatal(kind)
		}
	}
	e, s, cmd := mtuFixture()
	staged, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
	p.Default = &MTUDefault{MTU: 1800, Dependency: "new-peers", Bindings: []Binding{p.Port}}
	p.Observed = 1500
	s.Interfaces[p.Binding.ManagementID] = p
	cmd.Intents[0].Operation = InterfaceMTUClear
	cmd.Intents[0].MTURequest = 0
	if _, err = Prepare(staged, cmd, s); err == nil {
		t.Fatal("draft edit silently attached new default context")
	}
}

func TestMTUOriginalDeviceDriftBlocksForwardButPreservesUnappliedCompensation(t *testing.T) {
	for _, clear := range []bool{false, true} {
		e, s, cmd := mtuFixture()
		p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
		p.Default = &MTUDefault{MTU: 1800, Dependency: "stable-peers", Bindings: []Binding{p.Port}}
		p.Observed = 1500
		if clear {
			cmd.Intents[0].Operation, cmd.Intents[0].MTURequest = InterfaceMTUClear, 0
		} else {
			p.Requested, p.Observed = nil, 1800
		}
		s.Interfaces[p.Binding.ManagementID] = p
		staged, err := Prepare(e, cmd, s)
		if err != nil {
			t.Fatal(err)
		}
		checks, _ := Checks(staged.Candidate, s)
		if !Passed(checks) {
			t.Fatal(checks)
		}
		p.Observed = 1400
		s.Interfaces[p.Binding.ManagementID] = p
		checks, _ = Checks(staged.Candidate, s)
		if Passed(checks) || mtuProblem(staged.Candidate.Intents[0], s) != "MTU_ORIGINAL_DEVICE_UNPROVEN" || Compare(staged.Candidate, s).State != "reconciliation-required" {
			t.Fatal("original device drift inherited validation", checks)
		}
		forward := staged.Candidate.Intents[0]
		compensation := forward
		Reverse(&compensation)
		data, err := json.Marshal(compensation.MTU)
		if err != nil {
			t.Fatal(err)
		}
		var restored MTUChange
		if json.Unmarshal(data, &restored) != nil || !restored.Compensating || forward.MTU.Compensating {
			t.Fatal("private compensation checkpoint changed original", string(data))
		}
		compensation.MTU = &restored
		p.Requested = forward.MTU.After
		s.Interfaces[p.Binding.ManagementID] = p
		derived := staged.Candidate
		derived.Intents = []StoredIntent{compensation}
		checks, _ = Checks(derived, s)
		if !Passed(checks) || MTUOriginalDeviceRequired(compensation.MTU) {
			t.Fatal("unapplied device blocked exact original restoration", checks)
		}
		AfterImage(&forward)
		derived.Intents = []StoredIntent{forward}
		checks, _ = Checks(derived, s)
		if !Passed(checks) || MTUOriginalDeviceRequired(forward.MTU) {
			t.Fatal("after-image compared to original device", checks)
		}
	}
}

func TestLegacyExplicitMTUJournalRemainsNumericAndCompensatable(t *testing.T) {
	var m MTUChange
	if err := json.Unmarshal([]byte(`{"before":1500,"after":2000,"port":{},"bridge":{}}`), &m); err != nil {
		t.Fatal(err)
	}
	i := StoredIntent{Operation: InterfaceMTUSet, MTU: &m}
	Reverse(&i)
	if i.Operation != InterfaceMTUSet || *i.MTU.Before != 2000 || *i.MTU.After != 1500 || ExpectedMTU(i.MTU) != 1500 || i.MTU.Default != nil {
		t.Fatal(i)
	}
	data, err := json.Marshal(i.MTU)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil || fields["before"] != float64(2000) || fields["after"] != float64(1500) {
		t.Fatal(string(data))
	}
}
func TestInterfaceMTURejectsDefaultsMixedIntentsAndChangedBindings(t *testing.T) {
	for _, kind := range []string{"empty-request", "range", "unknown", "schema", "ownership", "external", "local-or-bond", "generation", "binding", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			e, s, cmd := mtuFixture()
			p := s.Interfaces[cmd.Intents[0].Object.ManagementID]
			switch kind {
			case "empty-request":
				p.Requested = nil
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
				p.Requested = MTUPointer(1800)
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
