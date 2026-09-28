package ovsdb

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestInternalPortNativePlanAndGuards(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			bind := func(table, id string) candidate.Binding {
				return candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: table, Generation: v.Candidate.Generation}
			}
			root := v.Observation.Evidence.Root
			parent := candidate.InternalPortParent{Dependency: "captured", Eligible: true}
			for id := range v.Observation.Rows["Bridge"] {
				parent.Binding = bind("Bridge", id)
				parent.Name = "br-test"
			}
			for id := range v.Observation.Rows["Port"] {
				parent.LocalPort = bind("Port", id)
			}
			for id := range v.Observation.Rows["Interface"] {
				parent.LocalInterface = bind("Interface", id)
			}
			parent.Members = []candidate.Binding{parent.LocalPort}
			v.Candidate.InternalPorts.Parents = map[string]candidate.InternalPortParent{parent.Binding.ManagementID: parent}
			i := candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.InternalPortCreate, Object: bind("Port", repository.NewID()), Dependency: parent.Dependency, PortCreation: &candidate.InternalPortCreation{Name: "pi-new", VLANID: 20, Root: root, Bridge: parent.Binding, BridgeName: parent.Name, Members: parent.Members, LocalPort: parent.LocalPort, LocalInterface: parent.LocalInterface, Interface: bind("Interface", repository.NewID()), AfterPresent: true}}
			e.Candidate.Intents = []candidate.StoredIntent{i}
			p, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if err = json.Unmarshal(p.Native, &n); err != nil {
				t.Fatal(err)
			}
			if len(n.InsertUUIDs) != 2 || len(n.CountIndexes) != 2 {
				t.Fatal("wrong write set")
			}
			for _, op := range n.Operations {
				if op["op"] == "update" || op["op"] == "delete" || op["op"] == "insert" && op["table"] == "Bridge" {
					t.Fatal("existing graph overwritten", op)
				}
			}
			candidate.Reverse(&e.Candidate.Intents[0])
			r, err := compileInternalPortExecution(p.ID, strings.Repeat("b", 64), p.Marker, e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range []string{"Mirror", "qos", "ingress_policing_rate", "mtu_request", "other_config", parent.LocalPort.OVSUUID, parent.LocalInterface.OVSUUID} {
				if !strings.Contains(string(r.Native), needle) {
					t.Fatal("missing guard", needle)
				}
			}
			if strings.Contains(string(r.Native), `"op":"insert"`) || strings.Contains(string(r.Native), `"op":"delete"`) {
				t.Fatal("rollback must only detach new pair")
			}
			if !d.public.InternalPortCreation {
				t.Fatal("native schema unsupported")
			}
		})
	}
}
func TestInternalPortAppliedRequiresRegularOfport(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{{"1", true}, {"65279", true}, {"65534", false}, {"-1", false}, {"0", false}, {[]any{}, false}, {"65280", false}} {
		row := inventory.Row{Values: map[string]any{"error": []any{}, "ofport": tc.value}}
		if internalInterfaceHealthy(row) != tc.want {
			t.Fatal(tc)
		}
		row.Values["error"] = []any{"synthetic failure"}
		if internalInterfaceHealthy(row) {
			t.Fatal("failed interface applied")
		}
	}
}
