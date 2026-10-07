package ovsdb

import (
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestPolicingCompilationGuardsFourValuesAndWritesOnlyTwoRates(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		d, v, e := executionFixture(t, version)
		for _, root := range v.Observation.Rows["Open_vSwitch"] {
			root.Values["other_config"] = map[string]any{}
		}
		prior := e.Candidate.Intents[0]
		port := v.Observation.Rows["Port"][prior.Object.OVSUUID]
		id := nativeRefs(port.Values["interfaces"])[0]
		iface := v.Observation.Rows["Interface"][id]
		empty := true
		iface.InterfaceOptionsEmpty = &empty
		iface.Values["ifindex"], iface.Values["ofport"], iface.Values["error"] = []any{"7"}, []any{"2"}, []any{}
		for _, name := range []string{"ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"} {
			iface.Values[name] = "0"
		}
		v.Observation.Rows["Interface"][id] = iface
		m := &candidate.PolicingChange{After: candidate.PolicingConfig{Rate: 1000}, Port: prior.Object, Name: "synthetic-pi", IfIndex: 7}
		for uuid := range v.Observation.Rows["Bridge"] {
			m.Bridge = candidate.Binding{OVSUUID: uuid}
		}
		e.Candidate.Intents = []candidate.StoredIntent{{Operation: candidate.InterfacePolicingSet, Object: candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: "Interface", Generation: prior.Object.Generation}, Policing: m}}
		p, err := compileExecution(repository.NewID(), "marker", e, v, d)
		if err != nil {
			t.Fatal(version, err)
		}
		var n nativePlan
		if json.Unmarshal(p.Native, &n) != nil {
			t.Fatal("native plan")
		}
		updates, guarded := 0, false
		for _, op := range n.Operations {
			if op["op"] == "update" {
				updates++
				row := op["row"].(map[string]any)
				if op["table"] != "Interface" || len(row) != 2 || row["ingress_policing_rate"] != float64(1000) || row["ingress_policing_kpkts_rate"] != float64(0) {
					t.Fatal(op)
				}
			}
			if op["op"] == "wait" && op["table"] == "Interface" {
				data, _ := json.Marshal(op)
				var cols struct {
					Columns []string `json:"columns"`
				}
				_ = json.Unmarshal(data, &cols)
				count := 0
				for _, c := range cols.Columns {
					if c == "ingress_policing_rate" || c == "ingress_policing_burst" || c == "ingress_policing_kpkts_rate" || c == "ingress_policing_kpkts_burst" || c == "ifindex" || c == "options" {
						count++
					}
				}
				guarded = guarded || count == 6
			}
		}
		if updates != 1 || !guarded {
			t.Fatal("broad write or missing CAS", n)
		}
		iface.Values["ingress_policing_rate"] = "1000"
		ops, err := appliedProofOperations(p, v, d)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range ops {
			o := op.(map[string]any)
			if o["op"] != "wait" && o["op"] != "select" {
				t.Fatal("Applied proof mutation", o)
			}
		}
		empty = false
		if _, err = compileExecution(repository.NewID(), "marker", e, v, d); err == nil {
			t.Fatal("unproven raw options")
		}
	}
}
