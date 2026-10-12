package candidate

import (
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func treeDraftFixture() (Envelope, Snapshot, Intent) {
	e, s, _ := planFixture()
	b := Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Bridge", Generation: s.Generation}
	params := map[string]*string{}
	for _, k := range SpanningTreeBasicKeys {
		params[k] = nil
	}
	params["stp-priority"] = ptr("0032768")
	p := SpanningTreeBridge{Binding: b, Configuration: SpanningTreeConfig{Parameters: params}, Known: true, Supported: true, Authority: "local-managed", Dependency: "private-native-graph-digest"}
	s.SpanningTrees = map[string]SpanningTreeBridge{b.ManagementID: p}
	i := Intent{ID: repository.NewID(), Operation: SpanningTreePrepare, Object: b, SpanningTree: &SpanningTreeRequest{Protocol: "rstp", Parameters: map[string]*string{"rstp-priority": ptr("4096")}}}
	return e, s, i
}

func TestSpanningTreeDraftPreservesUnsetInactiveSettingsAndSealedOriginals(t *testing.T) {
	e, s, i := treeDraftFixture()
	st := staged(t, e, s, i)
	change := st.Candidate.Intents[0].SpanningTree
	if change.Before.STP || change.Before.RSTP || !change.After.RSTP || *change.After.Parameters["stp-priority"] != "0032768" || change.Before.Parameters["rstp-priority"] != nil || *change.After.Parameters["rstp-priority"] != "4096" {
		t.Fatal(change)
	}
	if len(Compare(st.Candidate, s).Diff) != 9 || Compare(st.Candidate, s).State != "dirty" {
		t.Fatal(Compare(st.Candidate, s))
	}
	key := []byte(strings.Repeat("s", 32))
	st.Sign(key)
	before := Digest(st)
	i.SpanningTree.Parameters["rstp-priority"] = nil
	updated := staged(t, st, s, i)
	if Digest(st) != before || updated.Candidate.Intents[0].SpanningTree.After.Parameters["rstp-priority"] != nil || Digest(updated.Candidate.Intents[0].SpanningTree.Before) != Digest(change.Before) {
		t.Fatal("restage changed originals or alias-mutated the prior envelope")
	}
	updated.Sign(key)
	*updated.Candidate.Intents[0].SpanningTree.After.Parameters["stp-priority"] = "1"
	if updated.Verify(key, updated.Owner) == nil {
		t.Fatal("modified stored image retained its seal")
	}
}

func TestSpanningTreeDraftRejectsAmbiguousPayloadsAndMixedOperations(t *testing.T) {
	for _, scenario := range []string{"legacy", "inactive-key", "rstp-hello", "fraction", "signed", "empty", "disabled-parameters", "wrong-table", "foreign-fields", "mixed"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, i := treeDraftFixture()
			cmd := Command{Operation: "stage", Intents: []Intent{i}}
			switch scenario {
			case "legacy":
				cmd.Intents[0].Operation = "spanning_tree.configure"
			case "inactive-key":
				i.SpanningTree.Parameters["stp-priority"] = ptr("4096")
			case "rstp-hello":
				i.SpanningTree.Parameters["rstp-hello-time"] = ptr("2")
			case "fraction":
				i.SpanningTree.Parameters["rstp-priority"] = ptr("1.5")
			case "signed":
				i.SpanningTree.Parameters["rstp-priority"] = ptr("+4096")
			case "empty":
				i.SpanningTree.Parameters["rstp-priority"] = ptr("")
			case "disabled-parameters":
				i.SpanningTree.Protocol = "disabled"
			case "wrong-table":
				cmd.Intents[0].Object.Table = "Port"
			case "foreign-fields":
				cmd.Intents[0].MTURequest = 1500
			case "mixed":
				cmd.Intents = append(cmd.Intents, Intent{ID: repository.NewID(), Operation: "port.vlan.set"})
			}
			before := Digest(e)
			out, err := Prepare(e, cmd, s)
			if err == nil || Digest(e) != before || Digest(out) != before {
				t.Fatal("invalid draft mutated Candidate", scenario, err)
			}
		})
	}
}

func TestSpanningTreeValidationKeepsNativeRulesAndExecutionClosed(t *testing.T) {
	for _, scenario := range []string{"valid", "rounding", "hello-unit", "inactive-invalid", "forged"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, i := treeDraftFixture()
			code := ""
			switch scenario {
			case "rounding":
				i.SpanningTree.Parameters["rstp-priority"] = ptr("4097")
				code = "RSTP_PRIORITY_MULTIPLE_4096"
			case "hello-unit":
				i.SpanningTree.Protocol = "stp"
				i.SpanningTree.Parameters = map[string]*string{"stp-hello-time": ptr("2")}
				code = "STP_HELLO_TIME_NATIVE_UNIT_CAVEAT"
			case "inactive-invalid":
				p := s.SpanningTrees[i.Object.ManagementID]
				p.Configuration.Parameters["stp-hello-time"] = ptr("10")
				s.SpanningTrees[i.Object.ManagementID] = p
				code = "STP_MAX_AGE_HELLO_RELATION"
			}
			out := staged(t, e, s, i)
			if scenario == "forged" {
				out.Candidate.Intents[0].SpanningTree = nil
			}
			checks, _ := Checks(out.Candidate, s)
			if Passed(checks) || !hasGate(checks, "SPANNING_TREE_EXECUTION_PENDING") || code != "" && !hasGate(checks, code) {
				t.Fatal(checks)
			}
			caps := Capabilities(out.Candidate)
			if len(caps) != 1 || caps[0] != SpanningTreePrepareCapability {
				t.Fatal(caps)
			}
		})
	}
}

func TestSpanningTreeDriftAndRevocationRequireRestage(t *testing.T) {
	for _, scenario := range []string{"field", "dependency", "authority", "schema", "generation", "identity", "graph"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, i := treeDraftFixture()
			out := staged(t, e, s, i)
			original := Digest(out)
			p := s.SpanningTrees[i.Object.ManagementID]
			switch scenario {
			case "field":
				p.Configuration = cloneTreeConfig(p.Configuration)
				p.Configuration.STP = true
			case "dependency":
				p.Dependency = "changed-attachment"
			case "authority":
				p.Authority = "unknown"
			case "schema":
				s.Schema = "new-schema"
			case "generation":
				s.Generation = repository.NewID()
			case "identity":
				p.Binding.OVSUUID = repository.NewID()
			case "graph":
				p.GraphGate = "SPANNING_TREE_EXTERNAL_CONTROLLER"
			}
			s.SpanningTrees[i.Object.ManagementID] = p
			if Compare(out.Candidate, s).State != "reconciliation-required" {
				t.Fatal(scenario, Compare(out.Candidate, s))
			}
			if _, err := Prepare(out, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
				t.Fatal("restage silently replaced captured original", scenario)
			}
			id := ConflictID(out.Candidate, s)
			_, err := Prepare(out, Command{Operation: "rebase", Generation: s.Generation, ConfigRevision: s.Revision, ConflictSnapshot: &id}, s)
			if err == nil || Digest(out) != original {
				t.Fatal("rebase replaced original", scenario, err)
			}
		})
	}
}
