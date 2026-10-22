//go:build !linux

package ovsdb

import (
	"context"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func (e *Executor) prepareTreeExecution(ctx context.Context, p execution.Plan) (execution.Plan, error) {
	if _, ok := spanningTreeIntent(p.Envelope.Candidate); ok {
		return execution.Plan{}, errors.New("SPANNING_TREE_LINUX_REQUIRED")
	}
	return p, nil
}
func (e *Executor) treeDispatchCheck(ctx context.Context, p execution.Plan, n nativePlan) error {
	if _, ok := spanningTreeIntent(p.Envelope.Candidate); ok {
		return errors.New("SPANNING_TREE_LINUX_REQUIRED")
	}
	return nil
}
func (e *Executor) treeAppliedProof(ctx context.Context, p execution.Plan, v inventory.ExecutionView, target *string) error {
	if _, ok := spanningTreeIntent(p.Envelope.Candidate); ok {
		return errors.New("SPANNING_TREE_LINUX_REQUIRED")
	}
	return nil
}
