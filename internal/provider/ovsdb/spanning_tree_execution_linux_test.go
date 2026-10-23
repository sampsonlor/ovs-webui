//go:build linux

package ovsdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"golang.org/x/sys/unix"
)

func TestSpanningTreeCompilerGuardsWholeGraphAndWritesOnlyItsClosedFields(t *testing.T) {
	t.Run("journal-rejects-restoration-without-monitor-notification", func(t *testing.T) {
		for _, fault := range []string{"append", "truncate-restore", "replacement"} {
			t.Run(fault, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "conf.db")
				original := []byte("OVSDB JSON journal-test\n")
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				j := &treeJournal{file: f}
				if unix.Fstat(int(f.Fd()), &j.before) != nil || unix.Stat("/proc/self/exe", &j.executable) != nil {
					t.Fatal("fixture stat")
				}
				e := &Executor{provider: &Provider{options: Options{DatabaseFile: path}}, spanningTree: &SpanningTreeExecutionOptions{DatabaseExecutable: "/proc/self/exe"}}
				evidence := inventory.Evidence{File: witness(path, os.Getpid(), inventory.FileWitness{})}
				if err := j.check(e, os.Getpid(), evidence); err != nil {
					t.Fatal("unchanged held journal rejected", err)
				}
				switch fault {
				case "append":
					out, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, err = out.WriteString("persistent transaction\n")
					if err != nil {
						t.Fatal(err)
					}
					if err = out.Close(); err != nil {
						t.Fatal(err)
					}
				case "truncate-restore":
					if err := os.WriteFile(path, original, 0600); err != nil {
						t.Fatal(err)
					}
				case "replacement":
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, original, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if j.check(e, os.Getpid(), evidence) == nil {
					t.Fatal("journal change escaped independent ABA fence", fault)
				}
			})
		}
	})
	t.Run("closed-reasons-do-not-expose-native-details", func(t *testing.T) {
		private := "/private/example bearer=not-a-real-credential"
		for _, stage := range []string{"witness-pair", "witness-source", private} {
			err := &treeEvidenceFailure{stage: stage, cause: errors.New(private)}
			reason := SpanningTreeExecutionEvidenceReason(err)
			if strings.Contains(reason, "/private") || strings.Contains(reason, "credential") || strings.Contains(reason, "bearer") {
				t.Fatal("private details in public reason", reason)
			}
			if stage == private && reason != "spanning-tree-dispatch-unproven" {
				t.Fatal("unreviewed reason admitted", reason)
			}
		}
		if reason := SpanningTreeExecutionEvidenceReason(&treeEvidenceFailure{stage: "witness-source", cause: errors.New("SPANNING_TREE_SOURCE_PROCESS_FDS_UNPROVEN")}); reason != "spanning-tree-dispatch-witness-source-process-fds" {
			t.Fatal("closed source phase missing", reason)
		}
		for _, cause := range []error{errors.New(private), context.DeadlineExceeded} {
			wrapped := &treePreparationFailure{public: apitypes.Fail(409, "SPANNING_TREE_EXECUTION_EVIDENCE_UNPROVEN"), cause: &treeEvidenceFailure{stage: "witness-source", cause: cause}}
			var problem *apitypes.Problem
			if !errors.As(wrapped, &problem) || problem.Status != 409 || problem.Code != "SPANNING_TREE_EXECUTION_EVIDENCE_UNPROVEN" || wrapped.Error() != problem.Code {
				t.Fatal("public preparation refusal changed")
			}
			if cause == context.DeadlineExceeded && (!errors.Is(wrapped, cause) || SpanningTreeExecutionEvidenceReason(wrapped) != "spanning-tree-dispatch-deadline") {
				t.Fatal("private deadline cause lost")
			}
		}
	})
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v := treeFenceFixture(t, version)
			for table, request := range treeExecutionMonitorRequests(d) {
				r := request.([]any)[0].(map[string]any)
				columns := r["columns"].([]string)
				for name, c := range d.native.Tables[table].Columns {
					want := graphConfigurationColumn(table, name, c) || table == "Open_vSwitch" && (name == "cur_cfg" || name == "next_cfg") || table == "Interface" && name == "ofport"
					if slices.Contains(columns, name) != want {
						t.Fatal("monitor omitted configuration or admitted unrelated status", table, name)
					}
				}
				selects := r["select"].(map[string]bool)
				if selects["initial"] || !selects["insert"] || !selects["delete"] || !selects["modify"] {
					t.Fatal("monitor does not detect persistent ABA", selects)
				}
			}
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
