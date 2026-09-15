package inventory

import (
	"context"
	"encoding/json"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// ExecutionView binds a candidate projection to the same immutable native
// observation. It is private manager/provider data, never a public write DTO.
type ExecutionView struct {
	Candidate   candidate.Snapshot
	Observation Observation
}

func (s *Service) ExecutionView(ctx context.Context, bindings []candidate.Binding) (ExecutionView, error) {
	// CandidateSnapshot and the copy must describe exactly the same view. A
	// monitor update in between is a bounded retry, never a mixed read set.
	for attempt := 0; attempt < 3; attempt++ {
		s.mu.RLock()
		before := s.current
		s.mu.RUnlock()
		plan, err := s.CandidateSnapshot(ctx, bindings)
		if err != nil {
			return ExecutionView{}, err
		}
		s.mu.RLock()
		if s.current != before || s.failure != "" {
			s.mu.RUnlock()
			continue
		}
		value := ExecutionView{Candidate: plan, Observation: before.observation}
		data, err := json.Marshal(value)
		s.mu.RUnlock()
		if err != nil {
			return ExecutionView{}, err
		}
		var out ExecutionView
		if err = json.Unmarshal(data, &out); err != nil {
			return out, err
		}
		return out, nil
	}
	return ExecutionView{}, apitypes.Fail(409, "INVENTORY_CHANGED_DURING_PREFLIGHT")
}
