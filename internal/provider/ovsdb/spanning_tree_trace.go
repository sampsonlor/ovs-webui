package ovsdb

import (
	"context"
	"errors"
)

type treeEvidenceTraceKey struct{}
type treeEvidenceTrace struct{ stage string }
type treeEvidenceFailure struct {
	stage string
	cause error
}

func (e *treeEvidenceFailure) Error() string { return "SPANNING_TREE_EXECUTION_EVIDENCE_UNPROVEN" }
func (e *treeEvidenceFailure) Unwrap() error { return e.cause }

// Closed phase names only. Paths, process details, credentials and native frames
// never become public execution reasons or diagnostic strings.
func treeEvidenceStep(ctx context.Context, stage string) {
	if trace, ok := ctx.Value(treeEvidenceTraceKey{}).(*treeEvidenceTrace); ok {
		trace.stage = stage
	}
}
func treeDispatchReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "spanning-tree-dispatch-deadline"
	}
	var failure *treeEvidenceFailure
	if errors.As(err, &failure) {
		switch failure.stage {
		case "authorization", "execution-view", "database-source", "schema", "fence-before", "fence-after", "control-source", "witness-source", "witness-build", "witness-reply", "witness-authorization", "witness-pair", "final-scope", "transition":
			return "spanning-tree-dispatch-" + failure.stage
		}
	}
	return "spanning-tree-dispatch-unproven"
}
