package candidate

import (
	"slices"
	"testing"
)

func TestSpanningTreeExecutionIsIndependentAndKeepsOriginals(t *testing.T) {
	e, s, i := treeDraftFixture()
	i.Operation = SpanningTreeConfigure
	if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{i}}, s); err == nil {
		t.Fatal("prepare grant became execution grant")
	}
	p := s.SpanningTrees[i.Object.ManagementID]
	p.Executable = true
	s.SpanningTrees[i.Object.ManagementID] = p
	st := staged(t, e, s, i)
	checks, _ := Checks(st.Candidate, s)
	if !Passed(checks) || !slices.Contains(Capabilities(st.Candidate), SpanningTreeWriteCapability) || slices.Contains(Capabilities(st.Candidate), SpanningTreePrepareCapability) {
		t.Fatal(checks, Capabilities(st.Candidate))
	}
	original := Digest(st.Candidate.Intents[0])
	reverse := st.Candidate.Intents[0]
	Reverse(&reverse)
	if Digest(reverse.SpanningTree.Before) != Digest(st.Candidate.Intents[0].SpanningTree.After) || Digest(st.Candidate.Intents[0]) != original {
		t.Fatal("compensation mutated checkpoint")
	}
	Reverse(&reverse)
	if Digest(reverse) != original {
		t.Fatal("compensation did not preserve raw/unset originals")
	}
	prepared := i
	prepared.Operation = SpanningTreePrepare
	if _, err := Prepare(st, Command{Operation: "stage", Intents: []Intent{prepared}}, s); err == nil {
		t.Fatal("operation promoted by restaging")
	}
	p.Executable = false
	s.SpanningTrees[i.Object.ManagementID] = p
	checks, _ = Checks(st.Candidate, s)
	if Passed(checks) {
		t.Fatal("revoked root grant retained validation", checks)
	}
}
