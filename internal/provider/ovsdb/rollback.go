package ovsdb

import (
	"context"
	"encoding/json"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
)

// PrepareRollback derives compensation only from the durable original plan.
// The compiler puts current == our_after waits and touched-field restoration
// in ONE native transaction. Its normal identity/dependency guards still apply.
func (e *Executor) PrepareRollback(ctx context.Context, original execution.Plan, marker string) (execution.Plan, error) {
	var n nativePlan
	if json.Unmarshal(original.Native, &n) != nil {
		return execution.Plan{}, apitypes.Fail(503, "ROLLBACK_CHECKPOINT_INVALID")
	}
	view, err := e.inventory.ExecutionView(ctx, candidate.Bindings(original.Envelope.Candidate))
	if err != nil {
		return execution.Plan{}, err
	}
	if view.Candidate.Generation != original.Generation || view.Candidate.Schema != original.Schema || view.Observation.Evidence.Root != original.Root {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_GENERATION_CHANGED")
	}
	conn, _, d, identity, pid, err := e.connect(ctx)
	if err != nil {
		return execution.Plan{}, err
	}
	defer conn.Close()
	if identity != n.Evidence.Peer || d.public.Digest != original.Schema || !sameExecutionFile(e.provider.options, pid, n.Evidence) {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_GENERATION_CHANGED")
	}
	envelope := original.Envelope
	envelope.Candidate.Intents = append([]candidate.StoredIntent{}, envelope.Candidate.Intents...)
	for i := range envelope.Candidate.Intents {
		intent := &envelope.Candidate.Intents[i]
		labels, _ := view.Observation.Rows["Port"][intent.Object.OVSUUID].Values["external_ids"].(map[string]any)
		if labels[execution.MarkerKey] != original.Marker {
			return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
		}
		candidate.Reverse(intent)
	}
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	// This private derived envelope is never accepted as a user Candidate or
	// validation. Commit checks its current before-image again before sending.
	return compileExecution(original.ID, marker, envelope, view, d)
}
