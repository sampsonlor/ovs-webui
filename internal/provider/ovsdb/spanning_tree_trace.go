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
		case "authorization", "execution-view", "database-source", "journal", "schema", "fence-before", "fence-after", "fence-snapshot-changed", "control-source", "witness-source", "witness-build", "witness-reply", "witness-authorization", "witness-pair", "witness-pair-changed", "final-scope", "transition":
			return "spanning-tree-dispatch-" + failure.stage
		}
	}
	return "spanning-tree-dispatch-unproven"
}

// SpanningTreeExecutionEvidenceReason returns the same closed diagnostic phase
// used at dispatch. It never exposes native frames, process details or paths,
// and does not authorize a retry or change an unproven outcome.
func SpanningTreeExecutionEvidenceReason(err error) string { return treeDispatchReason(err) }
