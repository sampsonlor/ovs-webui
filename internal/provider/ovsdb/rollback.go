package ovsdb

import (
	"context"
	"encoding/json"
	"errors"

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
	conn, reader, d, identity, pid, err := e.connect(ctx)
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
		if candidate.IsGraphOperation(intent.Operation) {
			labels, _ := view.Observation.Rows["Open_vSwitch"][original.Root].Values["external_ids"].(map[string]any)
			if labels[bridgeMarkerKey] != original.Marker {
				return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
			}
			candidate.Reverse(intent)
			continue
		}
		table := "Port"
		if candidate.IsMTUOperation(intent.Operation) || intent.Operation == candidate.InterfacePolicingSet {
			table = "Interface"
		}
		labels, _ := view.Observation.Rows[table][intent.Object.OVSUUID].Values["external_ids"].(map[string]any)
		if labels[execution.MarkerKey] != original.Marker {
			return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
		}
		candidate.Reverse(intent)
	}
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	if _, ok := topologyIntent(envelope.Candidate); ok {
		if topologyHostCheck(envelope.Candidate) != nil {
			return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
		}
		compiled, err := compileTopologyExecution(original.ID, marker, envelope, view, d, n.TopologyBefore)
		if err != nil {
			return execution.Plan{}, err
		}
		return prepareTopologyHost(compiled, view, &n)
	}
	if _, ok := internalPortIntent(envelope.Candidate); ok {
		if bridgeHostCheck(envelope.Candidate) != nil {
			return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
		}
		creationMarker := n.CreationMarker
		if envelope.Candidate.Intents[0].PortDeletion != nil {
			creationMarker = marker
			if err = verifyPortDeletionBefore(ctx, conn, reader, d, envelope.Candidate, view, creationMarker); err != nil {
				var problem *apitypes.Problem
				if !errors.As(err, &problem) || problem.Code != "INTERNAL_PORT_GRAPH_CHANGED" {
					return execution.Plan{}, err
				}
				return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
			}
		}
		return compileInternalPortExecution(original.ID, marker, creationMarker, envelope, view, d)
	}
	if _, ok := bridgeIntent(envelope.Candidate); ok {
		if bridgeHostCheck(envelope.Candidate) != nil {
			return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
		}
		creationMarker := n.CreationMarker
		if envelope.Candidate.Intents[0].Deletion != nil {
			creationMarker = marker
			if err = verifyDeletionBefore(ctx, conn, reader, d, envelope.Candidate, creationMarker); err != nil {
				var problem *apitypes.Problem
				if !errors.As(err, &problem) || problem.Code != "ISOLATED_BRIDGE_GRAPH_CHANGED" {
					return execution.Plan{}, err
				}
				return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
			}
		}
		return compileBridgeExecution(original.ID, marker, creationMarker, envelope, view, d)
	}
	// This private derived envelope is never accepted as a user Candidate or
	// validation. Commit checks its current before-image again before sending.
	compiled, err := compileExecution(original.ID, marker, envelope, view, d)
	if err != nil {
		return compiled, err
	}
	prepared, err := e.preparePolicingKernel(ctx, compiled, view, &n)
	var problem *apitypes.Problem
	if errors.As(err, &problem) && (problem.Code == "POLICING_KERNEL_CONFLICT" || problem.Code == "POLICING_KERNEL_CONFIGURATION_CONFLICT") {
		return execution.Plan{}, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	return prepared, err
}
