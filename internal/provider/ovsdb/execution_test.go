package ovsdb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func executionFixture(t *testing.T, version string) (discovered, inventory.ExecutionView, candidate.Envelope) {
	t.Helper()
	d := schema(t, version)
	root, bridge, port, member, generation := repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID()
	mode, tag := "access", 20
	before := candidate.VLAN{Mode: &mode, Tag: &tag, Trunks: []int{}, CVLANs: []int{}}
	ref := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: port, Table: "Port", Generation: generation}
	rows := inventory.Rows{
		"Open_vSwitch": {root: {UUID: root, Values: map[string]any{"bridges": []any{bridge}, "external_ids": map[string]any{"synthetic-owner": "keep"}, "next_cfg": "9007199254740993", "cur_cfg": "9007199254740992"}}},
		"Bridge":       {bridge: {UUID: bridge, Values: map[string]any{"name": "br-test", "ports": []any{port}, "datapath_type": "dummy", "external_ids": map[string]any{}}}},
		"Port":         {port: {UUID: port, Values: map[string]any{"name": "p-test", "interfaces": []any{member}, "vlan_mode": []any{mode}, "tag": []any{"20"}, "trunks": []any{}, "cvlans": []any{}, "external_ids": map[string]any{"unrelated": "keep"}, "other_config": map[string]any{"unchanged": "yes"}}}},
		"Interface":    {member: {UUID: member, Values: map[string]any{"name": "i-test", "type": "dummy", "options": map[string]any{}, "external_ids": map[string]any{}, "error": []any{}}}},
	}
	s := candidate.Snapshot{Generation: generation, Schema: d.public.Digest, Revision: "snapshot", Ports: map[string]candidate.Port{ref.ManagementID: {Binding: ref, VLAN: before, Known: true, SchemaSupported: true, Authority: "local-managed", Modes: []string{"access", "trunk"}, Dependency: "captured-dependency"}}}
	target := 21
	e := candidate.Envelope{Owner: repository.NewID(), Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Generation: &generation, State: "ready", Intents: []candidate.StoredIntent{{ID: repository.NewID(), Object: ref, Operation: "port.vlan.set", Before: before, Value: candidate.VLAN{Mode: &mode, Tag: &target, Trunks: []int{}, CVLANs: []int{}}, Schema: d.public.Digest, Dependency: "captured-dependency"}}}}
	return d, inventory.ExecutionView{Candidate: s, Observation: inventory.Observation{Schema: d.public, Rows: rows, Evidence: inventory.Evidence{Root: root}}}, e
}
func TestFieldPlanUsesNarrowWaitsAndDurableCommit(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			p, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if json.Unmarshal(p.Native, &n) != nil {
				t.Fatal("plan")
			}
			updates, mutations := 0, 0
			for _, op := range n.Operations {
				switch op["op"] {
				case "update":
					updates++
					row := op["row"].(map[string]any)
					if len(row) != 4 || row["other_config"] != nil || row["external_ids"] != nil {
						t.Fatal("broad update", op)
					}
				case "mutate":
					mutations++
				case "wait":
					if op["timeout"] != float64(0) {
						t.Fatal("blocking wait")
					}
					for _, col := range op["columns"].([]any) {
						if col == "_version" || col == "next_cfg" || col == "cur_cfg" || col == "ports" || col == "statistics" {
							t.Fatal("unrelated/global CAS", op)
						}
					}
				}
			}
			if updates != 1 || mutations != 2 || n.Operations[len(n.Operations)-1]["durable"] != true {
				t.Fatal(n)
			}
			if !strings.Contains(string(p.Native), "9007199254740993") {
				t.Fatal("int64 precision loss")
			}
		})
	}
}
func TestExecutionResultDoesNotInventCommitOrApplied(t *testing.T) {
	n := nativePlan{Operations: make([]map[string]any, 3), CountIndexes: []int{0}, TargetIndex: 1}
	p := execution.Plan{Root: repository.NewID(), NextFloor: "9007199254740993"}
	good := `[{"count":1},{"rows":[{"_uuid":["uuid","` + p.Root + `"],"next_cfg":9007199254740994}]},{}]`
	out := decodeCommit(n, p, []byte(good))
	if out.Commit != "committed" || out.Applied != "pending" || *out.Target != "9007199254740994" {
		t.Fatal(out)
	}
	for _, body := range []string{`[]`, `[{"error":"timed out"}]`, strings.Replace(good, `"count":1`, `"count":0`, 1), strings.Replace(good, "9007199254740994", "9007199254740993", 1), `[{"count":1},null,{}]`} {
		if got := decodeCommit(n, p, []byte(body)); got.Commit != "unknown" || got.Applied != "unknown" {
			t.Fatal(body, got)
		}
	}
	out = decodeCommit(n, p, []byte(`[{"error":"timed out"},null,null]`))
	if out.Commit != "rejected" || out.Applied != "not-applied" {
		t.Fatal(out)
	}
}
func TestExecutionRefusesUnknownDependencyAndMarkerCapacity(t *testing.T) {
	d, v, e := executionFixture(t, "3.3.9")
	port := v.Observation.Rows["Port"][e.Candidate.Intents[0].Object.OVSUUID]
	delete(port.Values, "interfaces")
	if _, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d); err == nil {
		t.Fatal("unknown membership admitted")
	}
	d, v, e = executionFixture(t, "3.3.9")
	root := v.Observation.Rows["Open_vSwitch"][v.Observation.Evidence.Root]
	root.Values["next_cfg"] = "9223372036854775807"
	if _, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d); err == nil {
		t.Fatal("counter overflow admitted")
	}
}
