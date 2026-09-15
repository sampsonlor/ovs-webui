package candidate

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func ptr[T any](v T) *T { return &v }
func planFixture() (Envelope, Snapshot, Intent) {
	b := Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: repository.NewID()}
	p := Port{Binding: b, VLAN: VLAN{Mode: ptr("access"), Tag: ptr(10), Trunks: []int{}, CVLANs: []int{}}, Known: true, SchemaSupported: true, Modes: []string{"access", "trunk", "native-tagged", "native-untagged", "dot1q-tunnel"}, Dependency: "bridge-member-dependency", Authority: "local-managed"}
	s := Snapshot{Generation: b.Generation, Revision: "observed-1", Schema: "schema-1", Policy: "local-port-policy", Ports: map[string]Port{b.ManagementID: p}}
	e := Envelope{Owner: repository.NewID(), Epoch: repository.NewID(), Candidate: Candidate{ID: repository.NewID(), Revision: repository.NewID(), State: "empty", Intents: []StoredIntent{}}}
	i := Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: b, Value: VLAN{Mode: ptr("native-untagged"), Tag: ptr(20), Trunks: []int{}, CVLANs: []int{}}}
	return e, s, i
}
func staged(t *testing.T, e Envelope, s Snapshot, i Intent) Envelope {
	t.Helper()
	out, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func hasGate(gs []Gate, code string) bool {
	return slices.ContainsFunc(gs, func(g Gate) bool { return g.Code == code })
}
func TestStagePreservesOriginalsAndThreeWayRebaseRequiresExactChoices(t *testing.T) {
	e, s, i := planFixture()
	e = staged(t, e, s, i)
	p := s.Ports[i.Object.ManagementID]
	p.VLAN.Tag = ptr(15)
	s.Ports[i.Object.ManagementID] = p
	s.Revision = "observed-2"
	i.Value.Tag = ptr(25)
	e = staged(t, e, s, i)
	if *e.Candidate.Intents[0].Before.Tag != 10 {
		t.Fatal("edit silently replaced original")
	}
	v := Compare(e.Candidate, s)
	if v.State != "conflict" || len(v.Diff) != 4 || *v.Diff[1].Current.(*int) != 15 || *v.Diff[1].After.(*int) != 25 {
		t.Fatal("three-way values missing", v)
	}
	cmd := Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: v.ConflictSnapshot}
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("conflict silently resolved")
	}
	cmd.Resolutions = []Resolution{{IntentID: i.ID, Choice: "keep-mine"}}
	s.Revision = "observed-3"
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("stale conflict snapshot accepted")
	}
	s.Revision = cmd.ConfigRevision
	rebased, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if *rebased.Candidate.Intents[0].Before.Tag != 15 || *rebased.Candidate.Intents[0].Value.Tag != 25 || rebased.Candidate.Revision == e.Candidate.Revision {
		t.Fatal("rebase lost explicit intent")
	}
	cmd.Resolutions[0].Choice = "keep-current"
	dropped, err := Prepare(e, cmd, s)
	if err != nil || len(dropped.Candidate.Intents) != 0 || dropped.Candidate.Generation != nil {
		t.Fatal("keep-current must drop that intent", err)
	}
	if *s.Ports[i.Object.ManagementID].VLAN.Tag != 15 {
		t.Fatal("candidate changed live snapshot")
	}
}
func TestValidationNativeModesAuthorityAndUnknownFieldsFailClosed(t *testing.T) {
	for _, mode := range []string{"access", "trunk", "native-tagged", "native-untagged"} {
		t.Run(mode, func(t *testing.T) {
			e, s, i := planFixture()
			i.Value.Mode = ptr(mode)
			if mode == "trunk" {
				i.Value.Tag = nil
			}
			e = staged(t, e, s, i)
			checks, diff := Checks(e.Candidate, s)
			if !Passed(checks) || len(diff) != 4 {
				t.Fatal(checks)
			}
			if mode != "access" && !hasGate(checks, "EMPTY_TRUNKS_MEANS_ALL_VLANS") {
				t.Fatal("native all-VLAN scope hidden")
			}
		})
	}
	for _, scenario := range []string{"external", "unknown", "schema", "qinq", "reserved", "native-unknown", "cvlans", "default"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, i := planFixture()
			p := s.Ports[i.Object.ManagementID]
			switch scenario {
			case "external":
				p.Authority = "externally-controlled"
			case "unknown":
				p.Authority = "unknown"
			case "schema":
				p.SchemaSupported = false
			case "qinq":
				i.Value.Mode = ptr("dot1q-tunnel")
			case "reserved":
				p.VLAN.Tag = ptr(4095)
			case "native-unknown":
				p.VLAN.Mode = ptr("future-mode")
			case "cvlans":
				p.VLAN.CVLANs = []int{77}
			case "default":
				p.VLAN.Mode = nil
			}
			s.Ports[i.Object.ManagementID] = p
			e = staged(t, e, s, i)
			checks, _ := Checks(e.Candidate, s)
			if Passed(checks) != (scenario == "default") {
				t.Fatal("unproven semantics accepted or known default normalized", checks)
			}
			if scenario == "default" && (e.Candidate.Intents[0].Before.Mode != nil || !hasGate(checks, "NATIVE_DEFAULT_MODE_PRESERVED")) {
				t.Fatal("default original lost")
			}
		})
	}
}
func TestDependencyAndGenerationChangesNeverUseGlobalRevisionAsConflictGate(t *testing.T) {
	e, s, i := planFixture()
	e = staged(t, e, s, i)
	s.Revision = "unrelated-config-change"
	checks, _ := Checks(e.Candidate, s)
	if !Passed(checks) {
		t.Fatal("unrelated change blocked", checks)
	}
	p := s.Ports[i.Object.ManagementID]
	p.Dependency = "member-or-authority-changed"
	s.Ports[i.Object.ManagementID] = p
	checks, _ = Checks(e.Candidate, s)
	if !hasGate(checks, "DEPENDENCY_CHANGED") {
		t.Fatal("dependency not protected")
	}
	s.Generation = repository.NewID()
	v := Compare(e.Candidate, s)
	if v.State != "reconciliation-required" {
		t.Fatal(v)
	}
	cmd := Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: v.ConflictSnapshot, Resolutions: []Resolution{{IntentID: i.ID, Choice: "keep-mine"}}}
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("rebase relinked old OVS identity")
	}
	t.Run("identity_loss_dominates_field_conflict_in_either_order", func(t *testing.T) {
		e, s, i := planFixture()
		other := i
		other.ID = repository.NewID()
		other.Object.ManagementID = repository.NewID()
		other.Object.OVSUUID = repository.NewID()
		p := s.Ports[i.Object.ManagementID]
		p.Binding = other.Object
		s.Ports[other.Object.ManagementID] = p
		e = staged(t, staged(t, e, s, i), s, other)
		delete(s.Ports, i.Object.ManagementID)
		p.VLAN.Tag = ptr(39)
		s.Ports[other.Object.ManagementID] = p
		for range 2 {
			v := Compare(e.Candidate, s)
			if v.State != "reconciliation-required" || !hasGate(v.Checks, "OBJECT_BINDING_CHANGED") || !hasGate(v.Checks, "FIELD_CONFLICT") {
				t.Fatal("later field conflict hid the missing native identity", v)
			}
			e.Candidate.Intents[0], e.Candidate.Intents[1] = e.Candidate.Intents[1], e.Candidate.Intents[0]
		}
	})
}
func TestSignedOriginalsRejectTamperingPrincipalSwitchAndDuplicateTargets(t *testing.T) {
	e, s, i := planFixture()
	e = staged(t, e, s, i)
	key := bytes.Repeat([]byte{8}, 32)
	e.Sign(key)
	if err := e.Verify(key, e.Owner); err != nil {
		t.Fatal(err)
	}
	if err := e.Verify(key, repository.NewID()); err == nil {
		t.Fatal("owner switch accepted")
	}
	var changed Envelope
	b, _ := json.Marshal(e)
	_ = json.Unmarshal(b, &changed)
	changed.Candidate.Intents[0].Before.Tag = ptr(11)
	if err := changed.Verify(key, e.Owner); err == nil {
		t.Fatal("forged original accepted")
	}
	i.ID = repository.NewID()
	if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
		t.Fatal("two intents write the same field group")
	}
	if !apitypes.ManagementID(ConflictID(e.Candidate, s)) {
		t.Fatal("invalid conflict snapshot")
	}
}
func TestCandidateIntentAndRepresentationBudgetsPreservePriorDraft(t *testing.T) {
	e, s, i := planFixture()
	original := Digest(e)
	intents := make([]Intent, MaxIntents+1)
	for n := range intents {
		intents[n] = i
	}
	if _, err := Prepare(e, Command{Operation: "stage", Intents: intents}, s); err == nil {
		t.Fatal("intent budget absent")
	}
	if err := Budget(string(bytes.Repeat([]byte{'x'}, MaxDocument+1))); err == nil {
		t.Fatal("document budget absent")
	}
	if Digest(e) != original {
		t.Fatal("failed preparation changed draft")
	}
}

func TestOversizedCurrentDiffKeepsDraftReadableAndPreventsUnreviewedRebase(t *testing.T) {
	e, s, i := planFixture()
	e = staged(t, e, s, i)
	// Many independently changed native ports can exceed the response budget
	// without the originally saved user intent exceeding its own budget.
	for n := 0; n < 20; n++ {
		copy := e.Candidate.Intents[0]
		copy.ID = repository.NewID()
		copy.Object.ManagementID = repository.NewID()
		copy.Object.OVSUUID = repository.NewID()
		port := s.Ports[i.Object.ManagementID]
		port.Binding = copy.Object
		port.VLAN.Trunks = make([]int, 1000)
		for j := range port.VLAN.Trunks {
			port.VLAN.Trunks[j] = j + 1
		}
		s.Ports[copy.Object.ManagementID] = port
		e.Candidate.Intents = append(e.Candidate.Intents, copy)
	}
	if err := Budget(e); err != nil {
		t.Fatal("test draft must fit", err)
	}
	v := Review(e.Candidate, s)
	if !v.DiffTruncated || v.State != "review-limited" || v.ConflictSnapshot != nil || len(v.Intents) != 21 || !hasGate(v.Checks, "DIFF_BUDGET_EXCEEDED") {
		t.Fatal("partial diff represented as complete", v)
	}
	if _, err := Prepare(e, Command{Operation: "rebase"}, s); err == nil {
		t.Fatal("unreviewed oversized rebase allowed")
	}
	if _, err := Prepare(e, Command{Operation: "discard"}, Snapshot{}); err != nil {
		t.Fatal("draft cannot be discarded", err)
	}
}
