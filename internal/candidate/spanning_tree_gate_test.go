package candidate

import (
	"strings"
	"testing"
)

func TestSpanningTreeParameterChecksDoNotOpenCandidateExecution(t *testing.T) {
	e, snapshot, cmd := mtuFixture()
	cmd.Intents[0].Operation = "spanning_tree.configure"
	cmd.Intents[0].Object = snapshot.Interfaces[cmd.Intents[0].Object.ManagementID].Bridge
	cmd.Intents[0].MTURequest = 0
	before := Digest(e)
	returned, err := Prepare(e, cmd, snapshot)
	if err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_CONFIGURATION") || Digest(e) != before || Digest(returned) != before {
		t.Fatal("read-only parameter checks admitted a configuration intent", returned, err)
	}
	c := Candidate{Intents: []StoredIntent{{Operation: "spanning_tree.configure", Object: cmd.Intents[0].Object}}}
	if caps := Capabilities(c); len(caps) != 1 || caps[0] != "unsupported-intent" {
		t.Fatal("pending intent acquired a field capability", caps)
	}
	if checks, _ := Checks(c, snapshot); Passed(checks) {
		t.Fatal("pending operation passed Candidate validation")
	}
}
