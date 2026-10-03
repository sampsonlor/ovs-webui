package ovsdb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestQinQPlanGuardsPreservedTPIDAndWritesOnlyVLANFields(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			i := &e.Candidate.Intents[0]
			mode := "dot1q-tunnel"
			i.Value.Mode = &mode
			i.Value.CVLANs = []int{20, 30}
			i.QinQ = &candidate.QinQContext{Dependency: "captured-qinq"}
			p, err := compileExecution(repository.NewID(), strings.Repeat("b", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if json.Unmarshal(p.Native, &n) != nil {
				t.Fatal("plan")
			}
			mapGuard, memberGuard, writes := false, false, 0
			for _, op := range n.Operations {
				if op["op"] == "update" {
					writes++
					row := op["row"].(map[string]any)
					if op["table"] != "Port" || len(row) != 4 || row["cvlans"] == nil || row["other_config"] != nil {
						t.Fatal("broad update", op)
					}
				}
				if op["op"] == "wait" && op["table"] == "Port" {
					b, _ := json.Marshal(op)
					mapGuard = mapGuard || strings.Contains(string(b), "other_config")
					memberGuard = memberGuard || strings.Contains(string(b), `"includes"`)
				}
			}
			if !mapGuard || !memberGuard || writes != 1 {
				t.Fatal("missing atomic dependency guard", n)
			}
			ops, err := appliedProofOperations(p, v, d)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range ops {
				name := op.(map[string]any)["op"]
				if name != "wait" && name != "select" {
					t.Fatal("mutating proof", op)
				}
			}
		})
	}
}
