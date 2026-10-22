//go:build linux

package ovsdb

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestSpanningTreeCompilerGuardsWholeGraphAndWritesOnlyItsClosedFields(t *testing.T) {
	t.Run("closed-reasons-do-not-expose-native-details", func(t *testing.T) {
		private := "/private/example bearer=not-a-real-credential"
		for _, stage := range []string{"witness-pair", private} {
			err := &treeEvidenceFailure{stage: stage, cause: errors.New(private)}
			reason := treeDispatchReason(err)
			if strings.Contains(reason, "/private") || strings.Contains(reason, "credential") || strings.Contains(reason, "bearer") {
				t.Fatal("private details in public reason", reason)
			}
			if stage == private && reason != "spanning-tree-dispatch-unproven" {
				t.Fatal("unreviewed reason admitted", reason)
			}
		}
	})
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v := treeFenceFixture(t, version)
			var id string
			for key := range v.Observation.Rows["Bridge"] {
				id = key
			}
			b := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: "Bridge", Generation: v.Candidate.Generation}
			before := candidate.SpanningTreeConfig{Parameters: map[string]*string{}}
			after := candidate.SpanningTreeConfig{RSTP: true, Parameters: map[string]*string{}}
			for _, key := range candidate.SpanningTreeBasicKeys {
				before.Parameters[key] = nil
				after.Parameters[key] = nil
			}
			value := "4096"
			after.Parameters["rstp-priority"] = &value
			v.Candidate.SpanningTrees = map[string]candidate.SpanningTreeBridge{b.ManagementID: {Binding: b, Known: true, Supported: true, Executable: true, Authority: "local-managed", Configuration: before, Dependency: "graph", Bindings: []candidate.Binding{b}}}
			in := candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.SpanningTreeConfigure, Object: b, Schema: v.Candidate.Schema, Dependency: "graph", SpanningTree: &candidate.SpanningTreeChange{Before: before, After: after}}
			env := candidate.Envelope{Candidate: candidate.Candidate{Intents: []candidate.StoredIntent{in}}}
			p, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), env, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if json.Unmarshal(p.Native, &n) != nil || len(n.Operations) != 9 || len(n.CountIndexes) != 3 {
				t.Fatal("unexpected write plan", string(p.Native))
			}
			for j, table := range treeFenceTables {
				if n.Operations[j]["op"] != "wait" || n.Operations[j]["table"] != table || n.Operations[j]["timeout"] != float64(0) {
					t.Fatal("persistent graph guard missing", n.Operations[j])
				}
			}
			update := n.Operations[4]["row"].(map[string]any)
			if len(update) != 2 || update["stp_enable"] != false || update["rstp_enable"] != true {
				t.Fatal("broad flag write", update)
			}
			if n.Operations[5]["op"] != "mutate" || n.Operations[5]["table"] != "Bridge" || n.Operations[8]["durable"] != true {
				t.Fatal(n.Operations)
			}
			if !strings.Contains(string(p.Native), candidate.SpanningTreeMarker) || strings.Contains(string(p.Native), `"table":"STP"`) {
				t.Fatal("native field boundary", string(p.Native))
			}
			// Preparation cannot acquire this compiler, even with execution grants.
			env.Candidate.Intents[0].Operation = candidate.SpanningTreePrepare
			if _, err = compileExecution(p.ID, p.Marker, env, v, d); err == nil {
				t.Fatal("prepare intent reached writer")
			}
		})
	}
}
