package candidate

import (
	"bytes"
	"testing"
)

func qinqFixture() (Envelope, Snapshot, Intent) {
	e, s, i := planFixture()
	p := s.Ports[i.Object.ManagementID]
	p.QinQSupported = true
	p.QinQ = &QinQContext{Dependency: "qinq-native-dependencies"}
	s.Ports[i.Object.ManagementID] = p
	i.Value = VLAN{Mode: ptr("dot1q-tunnel"), Tag: ptr(200), Trunks: []int{}, CVLANs: []int{30, 20}}
	return e, s, i
}

func TestQinQStageRestageProofAndCompensation(t *testing.T) {
	e, s, i := qinqFixture()
	e = staged(t, e, s, i)
	checks, diff := Checks(e.Candidate, s)
	if !Passed(checks) || len(diff) != 5 || !hasGate(checks, "QINQ_SERVICE_TAG_AND_PRESERVED_TPID_REVIEW") {
		t.Fatal(checks, diff)
	}
	before := e.Candidate.Intents[0]
	i.Value.CVLANs = []int{}
	e = staged(t, e, s, i)
	if Digest(e.Candidate.Intents[0].Before) != Digest(before.Before) || Digest(e.Candidate.Intents[0].QinQ) != Digest(before.QinQ) {
		t.Fatal("restage changed original or context")
	}
	checks, _ = Checks(e.Candidate, s)
	if !Passed(checks) || !hasGate(checks, "EMPTY_CVLANS_MEANS_ALL_CUSTOMER_VLANS") {
		t.Fatal(checks)
	}
	p := s.Ports[i.Object.ManagementID]
	p.VLAN = e.Candidate.Intents[0].Value
	s.Ports[i.Object.ManagementID] = p
	Reverse(&e.Candidate.Intents[0])
	checks, _ = Checks(e.Candidate, s)
	if !Passed(checks) || *e.Candidate.Intents[0].Value.Mode != "access" {
		t.Fatal(checks)
	}
	p.VLAN = e.Candidate.Intents[0].Value
	s.Ports[i.Object.ManagementID] = p
	AfterImage(&e.Candidate.Intents[0])
	if !UsesQinQ(e.Candidate.Intents[0]) {
		t.Fatal("proof discarded QinQ dependency")
	}
	checks, _ = Checks(e.Candidate, s)
	if !Passed(checks) {
		t.Fatal(checks)
	}
	p.QinQ = &QinQContext{Dependency: "external-change", EtherType: ptr("802.1q")}
	s.Ports[i.Object.ManagementID] = p
	checks, _ = Checks(e.Candidate, s)
	if Passed(checks) || !hasGate(checks, "QINQ_DEPENDENCY_CHANGED") {
		t.Fatal(checks)
	}
}

func TestQinQUnprovenInputsAndContextsFailClosed(t *testing.T) {
	for _, scenario := range []string{"ineligible", "no-context", "missing-schema-mode", "unknown-tpid", "non-qinq-cvlans", "reserved-original", "ignored-trunks", "default-original", "original-qinq"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, i := qinqFixture()
			p := s.Ports[i.Object.ManagementID]
			switch scenario {
			case "ineligible":
				p.QinQSupported = false
			case "no-context":
				p.QinQ = nil
			case "missing-schema-mode":
				p.Modes = []string{"access"}
			case "unknown-tpid":
				p.QinQSupported = false
				p.QinQ.EtherType = ptr("future")
			case "non-qinq-cvlans":
				p.VLAN.CVLANs = []int{20}
			case "reserved-original":
				p.VLAN.Tag = ptr(0)
			case "ignored-trunks":
				p.VLAN.Trunks = []int{40}
			case "default-original":
				p.VLAN.Mode = nil
			case "original-qinq":
				p.VLAN = i.Value
				i.Value = VLAN{Mode: ptr("trunk"), Trunks: []int{}, CVLANs: []int{}}
			}
			s.Ports[i.Object.ManagementID] = p
			e = staged(t, e, s, i)
			checks, _ := Checks(e.Candidate, s)
			if Passed(checks) != (scenario == "default-original" || scenario == "original-qinq") {
				t.Fatal(checks)
			}
		})
	}
	for _, value := range []VLAN{
		{Mode: ptr("dot1q-tunnel"), Tag: ptr(200), Trunks: []int{30}},
		{Mode: ptr("dot1q-tunnel"), CVLANs: []int{30}},
		{Mode: ptr("dot1q-tunnel"), Tag: ptr(200), CVLANs: []int{30, 30}},
		{Mode: ptr("dot1q-tunnel"), Tag: ptr(200), CVLANs: []int{4095}},
	} {
		e, s, i := qinqFixture()
		i.Value = value
		if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
			t.Fatal("invalid QinQ accepted", value)
		}
	}
}

func TestQinQContextSealAndExplicitRebase(t *testing.T) {
	e, s, i := qinqFixture()
	e = staged(t, e, s, i)
	key := bytes.Repeat([]byte{6}, 32)
	e.Sign(key)
	e.Candidate.Intents[0].QinQ = &QinQContext{Dependency: "forged"}
	if e.Verify(key, e.Owner) == nil {
		t.Fatal("forged TPID context accepted")
	}
	e = staged(t, Envelope{Owner: e.Owner, Epoch: e.Epoch, Candidate: Candidate{ID: e.Candidate.ID, Revision: e.Candidate.Revision}}, s, i)
	p := s.Ports[i.Object.ManagementID]
	p.QinQ = &QinQContext{Dependency: "new-tpid", EtherType: ptr("802.1q")}
	s.Ports[i.Object.ManagementID] = p
	v := Compare(e.Candidate, s)
	if v.State != "conflict" {
		t.Fatal(v)
	}
	e, err := Prepare(e, Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: v.ConflictSnapshot, Resolutions: []Resolution{{IntentID: i.ID, Choice: "keep-mine"}}}, s)
	if err != nil {
		t.Fatal(err)
	}
	checks, _ := Checks(e.Candidate, s)
	if !Passed(checks) || *e.Candidate.Intents[0].QinQ.EtherType != "802.1q" {
		t.Fatal(checks)
	}
}
