package inventory

import (
	"context"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

// This private root policy is independent of the preparation grant. There is
// deliberately no CLI flag or public execution operation in this prerequisite.
func (s *Service) SetLocalSpanningTreeExecutionBridges(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_SPANNING_TREE_EXECUTION_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_SPANNING_TREE_EXECUTION_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localSpanningTreeExecution = allow
	s.spanningTreeAuthorityRevision = repository.NewID()
	return nil
}

// SpanningTreeExecutionView admits one exact immutable binding to the private
// preflight scope. It neither makes a prepare intent executable nor reserves
// the graph. Callers must recheck it around I/O and at any future dispatch.
func (s *Service) SpanningTreeExecutionView(ctx context.Context, b candidate.Binding) (ExecutionView, error) {
	if b.Table != "Bridge" || !apitypes.ManagementID(b.ManagementID) || !apitypes.UUID(b.OVSUUID) || b.Generation == "" {
		return ExecutionView{}, apitypes.Fail(422, "INVALID_SPANNING_TREE_EXECUTION_SCOPE")
	}
	// Check root authority before projecting configuration, then recheck under
	// the lock. The independent policy is included in the returned fingerprint.
	s.mu.RLock()
	allowed := s.localSpanningTreeExecution[b.ManagementID]
	revision := s.spanningTreeAuthorityRevision
	s.mu.RUnlock()
	if !allowed {
		return ExecutionView{}, apitypes.Fail(403, "SPANNING_TREE_EXECUTION_AUTHORITY_REQUIRED")
	}
	v, err := s.ExecutionView(ctx, []candidate.Binding{b})
	if err != nil {
		return ExecutionView{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.localSpanningTreeExecution[b.ManagementID] {
		return ExecutionView{}, apitypes.Fail(403, "SPANNING_TREE_EXECUTION_AUTHORITY_REQUIRED")
	}
	if revision != s.spanningTreeAuthorityRevision {
		return ExecutionView{}, apitypes.Fail(409, "SPANNING_TREE_AUTHORITY_CHANGED")
	}
	p, ok := v.Candidate.SpanningTrees[b.ManagementID]
	if !ok || p.Binding != b || v.Candidate.Generation != b.Generation {
		return ExecutionView{}, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if code := candidate.TreePreparationProblem(p); code != "" {
		return ExecutionView{}, apitypes.Fail(409, code)
	}
	v.Candidate.Policy = Digest([]any{v.Candidate.Policy, s.localSpanningTreeExecution, revision})
	return v, nil
}
