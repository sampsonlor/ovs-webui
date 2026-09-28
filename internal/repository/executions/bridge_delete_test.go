package executions

import (
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

func TestReplacementProjectionRequiresSettledProof(t *testing.T) {
	r := execution.Record{Plan: execution.Plan{Envelope: candidate.Envelope{Candidate: candidate.Candidate{Intents: []candidate.StoredIntent{
		{Operation: candidate.BridgeDelete, Deletion: &candidate.BridgeDeletion{}},
	}}}}}
	now := time.Now()
	for _, tc := range []struct {
		state, commit, applied, want string
		proof                        bool
	}{
		{"awaiting-confirmation", "", "", "reserved", false},
		{"confirmed", "", "", "not-used", false},
		{"not-committed", "", "", "not-used", false},
		{"recovery-required", "committed", "unknown", "unverified", true},
		{"rollback-conflict", "rejected", "not-applied", "unverified", true},
		{"rolled-back", "committed", "unknown", "unverified", true},
		{"rolled-back", "committed", "applied", "unverified", false},
		{"rolled-back", "committed", "applied", "restored", true},
	} {
		s := safety.Record{State: tc.state, Outcome: execution.Outcome{Commit: tc.commit, Applied: tc.applied}}
		if tc.proof {
			s.HealthyAt = &now
			s.Rollback = &execution.Plan{}
		}
		v := identityReplacements(r, s)
		if len(v) != 3 || v[0]["state"] != tc.want {
			t.Fatal(tc, v)
		}
	}
}
