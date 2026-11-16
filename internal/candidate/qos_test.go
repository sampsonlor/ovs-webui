package candidate

import (
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func qosCandidateFixture() (Envelope, Snapshot, Command) {
	e, snapshot, cmd := mtuFixture()
	i := cmd.Intents[0]
	p := snapshot.Interfaces[i.Object.ManagementID]
	object := Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "QoS", Generation: snapshot.Generation}
	port := QoSPort{Binding: p.Port, Interface: p.Binding, Bridge: p.Bridge, QoS: object, MTU: 1500, Dependency: "exclusive-qos-test",
		Rates: QoSRates{Type: "linux-htb", Maximum: 8000000, Queues: []QoSQueueRate{{ID: 7, Minimum: 1000000, Maximum: 3000000}}}}
	snapshot.QoSPorts = map[string]QoSPort{port.Binding.ManagementID: port}
	cmd.Intents = []Intent{{ID: i.ID, Object: port.Binding, Operation: QoSConfigure, QoSType: "linux-htb", QoSMaximum: 12000000,
		QoSQueues: []QoSQueueRate{{ID: 7, Minimum: 2000000, Maximum: 4000000}}}}
	return e, snapshot, cmd
}

func TestQoSCandidateCapturesOriginalAndRejectsDrift(t *testing.T) {
	e, s, cmd := qosCandidateFixture()
	staged, err := Prepare(e, cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	checks, diff := Checks(staged.Candidate, s)
	if !Passed(checks) || len(diff) != 3 || !slices.Equal(Capabilities(staged.Candidate), []string{QoSCapability}) {
		t.Fatal(checks, diff)
	}
	i := staged.Candidate.Intents[0]
	p := s.QoSPorts[i.Object.ManagementID]
	p.Rates = i.QoS.After
	s.QoSPorts[i.Object.ManagementID] = p
	if Compare(staged.Candidate, s).State != "reconciliation-required" {
		t.Fatal("replaced a drifted original")
	}
	Reverse(&i)
	if !i.QoS.Compensating || i.QoS.After.Maximum != 8000000 || i.QoS.Before.Maximum != 12000000 || staged.Candidate.Intents[0].QoS.Compensating {
		t.Fatal("compensation mutated the original")
	}
	checked := staged.Candidate
	checked.Intents = []StoredIntent{i}
	gates, _ := Checks(checked, s)
	if !Passed(gates) {
		t.Fatal(gates)
	}
}

func TestQoSRatesRejectDifferentLayoutAndUnrepresentableRates(t *testing.T) {
	for _, mutate := range []func(*Intent){
		func(i *Intent) { i.QoSType = "linux-hfsc" },
		func(i *Intent) { i.QoSQueues[0].ID = 8 },
		func(i *Intent) { i.QoSQueues = append(i.QoSQueues, i.QoSQueues[0]) },
		func(i *Intent) { i.QoSQueues[0].Minimum = 8 },
		func(i *Intent) { i.QoSMaximum++ },
		func(i *Intent) { i.QoSMaximum = 4_294_967_296 },
		func(i *Intent) { i.QoSQueues[0].Maximum = i.QoSMaximum + 8 },
	} {
		e, s, cmd := qosCandidateFixture()
		mutate(&cmd.Intents[0])
		if _, err := Prepare(e, cmd, s); err == nil {
			t.Fatalf("admitted invalid intent: %+v", cmd.Intents[0])
		}
	}
	e, s, cmd := qosCandidateFixture()
	s.QoSPorts = nil
	if _, err := Prepare(e, cmd, s); err == nil {
		t.Fatal("stage was available without independent root authority")
	}
}
