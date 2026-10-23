//go:build linux

package ovsdb

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestIsolationPlanUsesExactGraphAndOnlyOneNativeField(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v := treeFenceFixture(t, version)
			var uuid string
			for id := range v.Observation.Rows["Port"] {
				uuid = id
			}
			b := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: uuid, Table: "Port", Generation: v.Candidate.Generation}
			v.Candidate.Isolations = map[string]candidate.IsolationPort{b.ManagementID: {Binding: b, Known: true, Supported: true, Authority: "local-managed", Dependency: "whole-bridge", Bindings: []candidate.Binding{b}}}
			i := candidate.StoredIntent{ID: repository.NewID(), Object: b, Operation: candidate.PortIsolationSet, Schema: v.Candidate.Schema, Dependency: "whole-bridge", Isolation: &candidate.IsolationChange{Before: false, After: true}}
			e := candidate.Envelope{Candidate: candidate.Candidate{Intents: []candidate.StoredIntent{i}}}
			p, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if json.Unmarshal(p.Native, &n) != nil || len(n.Operations) != 9 || len(n.CountIndexes) != 3 || len(p.GraphBindings) != 1 {
				t.Fatal(string(p.Native))
			}
			for j, table := range treeFenceTables {
				if n.Operations[j]["op"] != "wait" || n.Operations[j]["table"] != table || n.Operations[j]["timeout"] != float64(0) {
					t.Fatal("whole graph omitted")
				}
			}
			row := n.Operations[4]["row"].(map[string]any)
			if len(row) != 1 || row["protected"] != true || n.Operations[4]["table"] != "Port" || n.Operations[8]["durable"] != true {
				t.Fatal("broad or non-durable write", n.Operations)
			}
			port := v.Candidate.Isolations[b.ManagementID]
			port.Protected = true
			v.Candidate.Isolations[b.ManagementID] = port
			if _, err := compileExecution(p.ID, p.Marker, e, v, d); err == nil {
				t.Fatal("changed original admitted")
			}
			candidate.AfterImage(&e.Candidate.Intents[0])
			if _, err := compileExecution(p.ID, p.Marker, e, v, d); err != nil {
				t.Fatal("matching after-image rejected", err)
			}
		})
	}
}
