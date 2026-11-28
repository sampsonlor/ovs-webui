package inventory

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
)

const interfaceCounterPollInterval = 250 * time.Millisecond

type InterfaceCounterMember struct {
	Interface apitypes.Ref       `json:"interface_ref"`
	State     string             `json:"state"`
	Reason    string             `json:"reason_code,omitempty"`
	Deltas    map[string]*string `json:"deltas"`
}

type InterfaceCounterDiagnostic struct {
	Template           string                   `json:"template_id"`
	Target             apitypes.Ref             `json:"target_ref"`
	State              string                   `json:"state"`
	Reason             string                   `json:"reason_code,omitempty"`
	Source             string                   `json:"source"`
	Freshness          string                   `json:"freshness"`
	InstanceGeneration string                   `json:"instance_generation,omitempty"`
	FirstSampledAt     *time.Time               `json:"first_sampled_at,omitempty"`
	SecondSampledAt    *time.Time               `json:"second_sampled_at,omitempty"`
	Truncated          bool                     `json:"truncated"`
	Members            []InterfaceCounterMember `json:"members"`
}

// CheckInterfaceCounterTarget is the inexpensive admission fence. The worker
// independently binds the target again when its sampling window starts.
func (s *Service) CheckInterfaceCounterTarget(ctx context.Context, req diagnostics.InterfaceCounterRequest, claims authn.Claims) error {
	if err := req.Authorize(claims); err != nil {
		return err
	}
	if s == nil || !s.mu.TryRLock() {
		return apitypes.Fail(503, "DIAGNOSTIC_INVENTORY_UNAVAILABLE")
	}
	defer s.mu.RUnlock()
	if !statisticViewFresh(s.current, s.failure, s.now()) {
		return apitypes.Fail(503, "DIAGNOSTIC_INVENTORY_UNAVAILABLE")
	}
	_, err := bindInterfaceCounterScope(ctx, s.current, s.statisticsEpoch, req.Target.ID)
	return err
}

// ObserveInterfaceCounterDiagnostic waits for the next adjacent shared
// publication after a fresh baseline. Its only poll is of in-memory state
// already refreshed by RunStatistics. No native work is scheduled here.
func (s *Service) ObserveInterfaceCounterDiagnostic(ctx context.Context, req diagnostics.InterfaceCounterRequest, claims authn.Claims) (InterfaceCounterDiagnostic, error) {
	if err := req.Authorize(claims); err != nil {
		return InterfaceCounterDiagnostic{}, err
	}
	empty := InterfaceCounterDiagnostic{
		Template: diagnostics.InterfaceCounterTemplate, Target: req.Target,
		Source: "ovsdb-statistics", Freshness: "unknown", Members: []InterfaceCounterMember{},
	}
	out := empty
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.SampleSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(interfaceCounterPollInterval)
	defer ticker.Stop()
	var run interfaceCounterWindow
	for {
		if err := ctx.Err(); err != nil {
			out.State, out.Reason = "timed-out", "DIAGNOSTIC_COUNTER_WINDOW_EXPIRED"
			if errors.Is(err, context.Canceled) {
				out.State, out.Reason = "cancelled", "DIAGNOSTIC_CANCELLED"
			}
			return out, nil
		}
		done, err := s.interfaceCounterPoll(ctx, &run, &out)
		if ctx.Err() != nil {
			out = empty
			continue
		}
		if err != nil || done {
			return out, err
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

type interfaceCounterScope struct {
	key, generation string
	bindings        []Binding
	associations    map[string]string
	truncated       bool
}

type interfaceCounterWindow struct {
	view  *view
	epoch uint64
	scope interfaceCounterScope
	first time.Time
	base  map[string]statisticPoint
}

func (s *Service) interfaceCounterPoll(ctx context.Context, run *interfaceCounterWindow, out *InterfaceCounterDiagnostic) (bool, error) {
	if s == nil {
		out.State, out.Reason = "provider-unavailable", "DIAGNOSTIC_INVENTORY_UNAVAILABLE"
		return true, nil
	}
	if !s.mu.TryRLock() {
		return false, nil
	}
	defer s.mu.RUnlock()
	v, epoch, now := s.current, s.statisticsEpoch, s.now()
	if !statisticViewFresh(v, s.failure, now) {
		out.State, out.Reason = "provider-unavailable", "DIAGNOSTIC_INVENTORY_UNAVAILABLE"
		return true, nil
	}
	if run.view != v || run.epoch != epoch {
		scope, err := bindInterfaceCounterScope(ctx, v, epoch, out.Target.ID)
		if run.scope.key != "" && (err != nil || scope.key != run.scope.key) {
			out.State, out.Reason = "evidence-unavailable", "DIAGNOSTIC_COUNTER_SCOPE_CHANGED"
			return true, nil
		}
		if err != nil {
			return true, err
		}
		run.view, run.epoch, run.scope = v, epoch, scope
		out.InstanceGeneration, out.Truncated = scope.generation, scope.truncated
		if len(scope.bindings) == 0 {
			out.State, out.Reason = "no-data", "DIAGNOSTIC_COUNTER_NO_MEMBERS"
			return true, nil
		}
	}
	if !s.statisticsMu.TryLock() {
		return false, nil
	}
	defer s.statisticsMu.Unlock()
	cache := &s.statistics
	if cache.observer == nil || cache.failure != "" && (cache.failure != "STATISTICS_RESYNCING" || !run.first.IsZero()) {
		out.State, out.Reason = "provider-unavailable", "DIAGNOSTIC_COUNTER_SOURCE_UNAVAILABLE"
		return true, nil
	}
	if cache.observedAt.IsZero() || now.Sub(cache.observedAt) < 0 || now.Sub(cache.observedAt) > statisticsFreshFor {
		return false, nil
	}
	if run.first.IsZero() {
		for _, binding := range run.scope.bindings {
			if point, ok := interfaceCounterPoint(v, epoch, cache, binding, run.scope.associations[binding.UUID]); ok && point.valid != 0 {
				if run.base == nil {
					run.base = map[string]statisticPoint{}
				}
				run.base[binding.ManagementID] = point
			}
		}
		if len(run.base) > 0 {
			run.first = cache.observedAt
		}
		return false, nil
	}
	if cache.observedAt.Before(run.first) {
		out.State, out.Reason = "evidence-unavailable", "DIAGNOSTIC_COUNTER_CLOCK_CHANGED"
		return true, nil
	}
	if cache.observedAt.Equal(run.first) {
		return false, nil
	}
	finishInterfaceCounterWindow(v, epoch, cache, run, out)
	return true, nil
}

func bindInterfaceCounterScope(ctx context.Context, v *view, epoch uint64, id string) (interfaceCounterScope, error) {
	var out interfaceCounterScope
	roots := v.observation.Rows["Open_vSwitch"]
	root := roots[v.observation.Evidence.Root]
	if len(roots) != 1 || !apitypes.ManagementID(v.decision.Generation) || !apitypes.UUID(root.UUID) ||
		root.UUID != v.observation.Evidence.Root || v.observation.Schema.Digest == "" || v.observation.Evidence.Peer == "" {
		return out, apitypes.Fail(503, "DIAGNOSTIC_COUNTER_SCOPE_UNPROVEN")
	}
	row, err := linkLACPTarget(ctx, v, "Port", id)
	if err != nil {
		return out, err
	}
	port := v.decision.Bindings[Key("Port", row.UUID)]
	members, incomplete, err := linkLACPChildren(ctx, v, "Interface", row.Values["interfaces"])
	if err != nil {
		return out, err
	}
	if incomplete {
		return out, apitypes.Fail(503, "DIAGNOSTIC_COUNTER_SCOPE_UNPROVEN")
	}
	all := make([]Binding, 0, len(members))
	for _, child := range members {
		all = append(all, v.decision.Bindings[Key("Interface", child.row.UUID)])
	}
	selected := all[:min(len(all), diagnostics.MaxCounterInterfaces)]
	for _, binding := range selected {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		parentPort, bridge := statisticParents(v, binding)
		if parentPort != port || bridge.State != "active" || !slices.Contains(refs(root.Values["bridges"]), bridge.UUID) {
			return out, apitypes.Fail(503, "DIAGNOSTIC_COUNTER_SCOPE_UNPROVEN")
		}
	}
	associations := selectedStatisticAssociations(v, selected)
	out.generation, out.bindings, out.associations = v.decision.Generation, selected, associations
	out.truncated = len(selected) < len(all)
	out.key = Digest([]any{port, all, associations, v.decision.Generation, v.observation.Schema.Digest,
		v.observation.Evidence.Root, v.observation.Evidence.Peer, epoch})
	return out, nil
}

func interfaceCounterPoint(v *view, epoch uint64, cache *statisticsCache, binding Binding, association string) (statisticPoint, bool) {
	entry := cache.entries[binding.ManagementID]
	if !statisticEntryMatches(v, binding, entry, association) || entry.sourceEpoch != epoch || len(entry.samples) == 0 {
		return statisticPoint{}, false
	}
	point := entry.samples[len(entry.samples)-1]
	return point, point.at.Equal(cache.observedAt)
}

func finishInterfaceCounterWindow(v *view, epoch uint64, cache *statisticsCache, run *interfaceCounterWindow, out *InterfaceCounterDiagnostic) {
	partial, observed := false, false
	for _, binding := range run.scope.bindings {
		member := InterfaceCounterMember{
			Interface: apitypes.Ref{Kind: "interface", ID: binding.ManagementID},
			State:     "unavailable", Reason: "DIAGNOSTIC_COUNTER_SAMPLE_GAP", Deltas: emptyInterfaceCounterDeltas(),
		}
		before, haveBefore := run.base[binding.ManagementID]
		after, haveAfter := interfaceCounterPoint(v, epoch, cache, binding, run.scope.associations[binding.UUID])
		samples := cache.entries[binding.ManagementID].samples
		if haveBefore && haveAfter && len(samples) >= 2 && samples[len(samples)-2] == before &&
			after.at.After(before.at) && after.at.Sub(before.at) < 2*StatisticsInterval {
			projectInterfaceCounterDelta(before, after, &member)
		}
		observed = observed || member.State == "observed" || member.State == "partial"
		partial = partial || member.State != "observed"
		out.Members = append(out.Members, member)
	}
	first, second := run.first.UTC(), cache.observedAt.UTC()
	out.FirstSampledAt, out.SecondSampledAt = &first, &second
	if !observed {
		out.State, out.Reason = "no-data", "DIAGNOSTIC_COUNTER_SAMPLE_GAP"
		return
	}
	out.Freshness, out.State = "fresh", "complete"
	if partial {
		out.State, out.Reason = "partial", "DIAGNOSTIC_COUNTER_INCOMPLETE"
	}
	if out.Truncated {
		out.State, out.Reason = "truncated", "DIAGNOSTIC_COUNTER_MEMBER_LIMIT"
	}
}

func emptyInterfaceCounterDeltas() map[string]*string {
	deltas := make(map[string]*string, len(statisticCounters))
	for _, name := range statisticCounters {
		deltas[name] = nil
	}
	return deltas
}

func projectInterfaceCounterDelta(before, after statisticPoint, member *InterfaceCounterMember) {
	if after.reset || after.discontinuity == "counter-reset" {
		member.Reason = "DIAGNOSTIC_COUNTER_RESET"
		return
	}
	if after.coalesced || after.discontinuity != "" {
		return
	}
	valid := before.valid & after.valid
	if valid == 0 {
		member.Reason = "DIAGNOSTIC_COUNTER_FIELDS_UNKNOWN"
		return
	}
	for index := range statisticCounters {
		if valid&(1<<index) != 0 && after.values[index] < before.values[index] {
			member.Reason = "DIAGNOSTIC_COUNTER_RESET"
			return
		}
	}
	member.State, member.Reason = "observed", ""
	for index, name := range statisticCounters {
		if valid&(1<<index) == 0 {
			member.State, member.Reason = "partial", "DIAGNOSTIC_COUNTER_FIELDS_UNKNOWN"
			continue
		}
		value := strconv.FormatUint(after.values[index]-before.values[index], 10)
		member.Deltas[name] = &value
	}
}
