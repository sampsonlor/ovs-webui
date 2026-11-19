package inventory

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

func (s *Service) readStatistics(op string, path map[string]string, q url.Values, claims authn.Claims) (any, error) {
	if !slices.Contains(claims.Capabilities, "state.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	s.mu.RLock()
	v, failure := s.current, s.failure
	s.mu.RUnlock()
	if v == nil {
		return nil, apitypes.Fail(503, "OVSDB_RESYNCING")
	}
	if v.decision.State != "confirmed" {
		return nil, apitypes.Fail(503, "INSTANCE_RECONCILIATION_REQUIRED")
	}
	now := s.now()
	s.statisticsMu.Lock()
	defer s.statisticsMu.Unlock()
	cache := &s.statistics
	if op == "readStatistic" {
		for _, binding := range v.decision.Bindings {
			if binding.ManagementID == path["statistic_id"] && binding.Table == "Interface" && binding.State == "active" {
				return statisticResource(v, failure, binding, *cache, now, true), nil
			}
		}
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	limit := 100
	var err error
	if q.Get("limit") != "" {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			return nil, apitypes.Fail(422, "INVALID_LIMIT")
		}
	}
	if len(q.Get("filter")) > 1024 {
		return nil, apitypes.Fail(422, "INVALID_FILTER")
	}
	for key, table := range map[string]string{"bridge_id": "Bridge", "port_id": "Port", "interface_id": "Interface"} {
		id := q.Get(key)
		if id == "" {
			continue
		}
		if !apitypes.ManagementID(id) {
			return nil, apitypes.Fail(422, "INVALID_STATISTICS_SELECTION")
		}
		found := false
		for _, binding := range v.decision.Bindings {
			found = found || binding.ManagementID == id && binding.Table == table && binding.State == "active"
		}
		if !found {
			return nil, apitypes.Fail(404, "NOT_FOUND")
		}
	}
	selection := []string{q.Get("bridge_id"), q.Get("port_id"), q.Get("interface_id")}
	cur := cursor{Principal: claims.PrincipalID, Permission: claims.Revision, Operation: op, Filter: q.Get("filter"),
		Selection: Digest(selection), Generation: v.decision.Generation, Snapshot: v.id, Limit: limit,
		Expires: now.Add(30 * time.Second).UnixMilli(), Order: "statistics-name-natural-v1"}
	if token := q.Get("cursor"); token != "" {
		if len(token) > 4096 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		blob, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if decodeErr != nil {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		plain, decodeErr := authn.Unseal(s.key, blob, []byte("statistics-page-v1"))
		var old cursor
		if decodeErr != nil || json.Unmarshal(plain, &old) != nil || old.Principal != cur.Principal || old.Permission != cur.Permission ||
			old.Operation != cur.Operation || old.Filter != cur.Filter || old.Selection != cur.Selection || old.Generation != cur.Generation ||
			old.Snapshot != cur.Snapshot || old.Order != cur.Order || old.Limit != cur.Limit || old.Expires <= now.UnixMilli() {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		cur = old
	}
	bindings := []Binding{}
	for _, binding := range v.decision.Bindings {
		if binding.Table != "Interface" || binding.State != "active" {
			continue
		}
		name := textValue(v.observation.Rows["Interface"][binding.UUID].Values["name"])
		if !strings.Contains(strings.ToLower(name), strings.ToLower(cur.Filter)) || selection[2] != "" && selection[2] != binding.ManagementID {
			continue
		}
		port, bridge := statisticParents(v, binding)
		if selection[0] != "" && selection[0] != bridge.ManagementID || selection[1] != "" && selection[1] != port.ManagementID {
			continue
		}
		bindings = append(bindings, binding)
	}
	slices.SortFunc(bindings, func(a, b Binding) int {
		left := textValue(v.observation.Rows["Interface"][a.UUID].Values["name"])
		right := textValue(v.observation.Rows["Interface"][b.UUID].Values["name"])
		if compared := naturalNameCompare(left, right); compared != 0 {
			return compared
		}
		return strings.Compare(a.ManagementID, b.ManagementID)
	})
	start := 0
	if cur.After != "" {
		at := slices.IndexFunc(bindings, func(b Binding) bool { return b.ManagementID == cur.After })
		if at < 0 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		start = at + 1
	}
	items := []map[string]any{}
	size := 0
	for _, binding := range bindings[start:] {
		item := statisticResource(v, failure, binding, *cache, now, false)
		blob, _ := json.Marshal(item)
		if len(items) >= limit || len(items) >= 50 || len(items) > 0 && size+len(blob) > 40<<10 {
			break
		}
		items, size = append(items, item), size+len(blob)
	}
	more := start+len(items) < len(bindings)
	var next any
	if more {
		cur.After = items[len(items)-1]["id"].(string)
		blob, _ := json.Marshal(cur)
		sealed, sealErr := authn.Seal(s.key, blob, []byte("statistics-page-v1"))
		if sealErr != nil {
			return nil, sealErr
		}
		next = base64.RawURLEncoding.EncodeToString(sealed)
	}
	fresh, reason := "fresh", cache.failure
	if !statisticViewFresh(v, failure, now) || reason != "" {
		fresh = "stale"
	}
	availability := "complete"
	if fresh != "fresh" {
		availability = "degraded"
	}
	var observed any
	if !cache.observedAt.IsZero() {
		observed = cache.observedAt
	}
	for _, item := range items {
		stats := item["statistics"].(map[string]any)
		if stats["state"] != "observed" || stats["source"].(map[string]any)["freshness"] != "fresh" {
			availability = "degraded"
			fresh = "stale"
		}
		if at, ok := stats["sampled_at"].(time.Time); ok {
			if old, ok := observed.(time.Time); !ok || at.Before(old) {
				observed = at
			}
		}
	}
	if availability == "degraded" && reason == "" {
		reason = "STATISTICS_OBSERVATION_EXPIRED"
	}
	if failure != "" {
		reason = failure
	}
	return map[string]any{"snapshot_id": v.id, "instance_generation": v.decision.Generation, "items": items, "next_cursor": next,
		"truncated": more, "source": map[string]any{"provider_id": "ovsdb", "authority": "ovsdb-statistics", "observed_at": observed, "freshness": fresh, "confidence": "partial"},
		"availability": availability, "reason": optionalStatisticText(reason),
		"coverage": map[string]any{"state": "selected-interface-counters", "matched_interfaces": len(bindings), "returned_interfaces": len(items),
			"sampled_interfaces": len(cache.entries), "max_objects": statisticsObjects, "max_samples_per_interface": statisticsSamples,
			"retention_seconds": 300, "interval_ms": 5000, "native_capture_time": "unreported",
			"port_traffic": "individual member Interfaces", "includes_linux": false}}, nil
}

func statisticParents(v *view, binding Binding) (Binding, Binding) {
	port, ok := parent(v, "Port", "interfaces", binding.UUID)
	if !ok {
		return Binding{}, Binding{}
	}
	p := v.decision.Bindings[Key("Port", port.UUID)]
	bridge, ok := parent(v, "Bridge", "ports", port.UUID)
	if !ok {
		return p, Binding{}
	}
	return p, v.decision.Bindings[Key("Bridge", bridge.UUID)]
}

func optionalStatisticText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func statisticResource(v *view, failure string, binding Binding, cache statisticsCache, now time.Time, detail bool) map[string]any {
	item := base(binding.ManagementID, "statistic", "unknown", v)
	item["name"] = textValue(v.observation.Rows["Interface"][binding.UUID].Values["name"])
	item["native_uuid"], item["instance_generation"] = binding.UUID, v.decision.Generation
	item["interface_ref"] = apitypes.Ref{Kind: "interface", ID: binding.ManagementID}
	item["port_ref"], item["bridge_ref"], item["port_name"], item["bridge_name"] = nil, nil, nil, nil
	port, bridge := statisticParents(v, binding)
	if port.State == "active" {
		item["port_ref"] = apitypes.Ref{Kind: "port", ID: port.ManagementID}
		item["port_name"] = textValue(v.observation.Rows["Port"][port.UUID].Values["name"])
	}
	if bridge.State == "active" {
		item["bridge_ref"] = apitypes.Ref{Kind: "bridge", ID: bridge.ManagementID}
		item["bridge_name"] = textValue(v.observation.Rows["Bridge"][bridge.UUID].Values["name"])
	}
	entry := cache.entries[binding.ManagementID]
	reason, fresh, state := cache.failure, "unknown", "unknown"
	sample := statisticPointValue(statisticPoint{})
	var at any
	history := []statisticSample{}
	eligible := entry.binding == binding && entry.generation == v.decision.Generation && entry.schema == v.observation.Schema.Digest &&
		entry.association == statisticAssociation(v, binding)
	if eligible {
		for _, point := range entry.samples {
			if !point.at.Before(now.Add(-StatisticsRetention)) {
				history = append(history, statisticPointValue(point))
			}
		}
	}
	if len(history) > 0 {
		sample, fresh = history[len(history)-1], "fresh"
		at = sample.SampledAt
		known := false
		for _, value := range sample.Counters {
			known = known || value != nil
		}
		if known {
			state = "observed"
		} else if reason == "" {
			reason = "STATISTICS_COUNTERS_UNREPORTED"
		}
		if known {
			state = "observed"
		} else if reason == "" {
			reason = "STATISTICS_COUNTERS_UNREPORTED"
		}
		if !statisticViewFresh(v, failure, now) || reason != "" || now.Sub(sample.SampledAt) < 0 || now.Sub(sample.SampledAt) > statisticsFreshFor {
			fresh = "stale"
			sample.Rates = emptyStatisticRates()
			if reason == "" {
				reason = "STATISTICS_OBSERVATION_EXPIRED"
			}
		}
	} else if reason == "" {
		reason = "STATISTICS_RESYNCING"
	}
	if cache.failure == "STATISTICS_SCHEMA_UNSUPPORTED" {
		state = "unsupported"
	}
	if failure != "" {
		reason = failure
	}
	statsSource := map[string]any{"provider_id": "ovsdb", "authority": "ovsdb-statistics", "observed_at": at, "freshness": fresh, "confidence": "partial"}
	stats := map[string]any{"state": state, "reason": optionalStatisticText(reason), "sampled_at": at,
		"counters": sample.Counters, "rates": sample.Rates, "reset": sample.Reset, "discontinuity": sample.Discontinuity,
		"coalesced": sample.Coalesced, "sample_count": len(history), "interval_ms": 5000, "retention_seconds": 300, "source": statsSource}
	if detail {
		stats["history"] = history
	}
	item["statistics"], item["source"], item["state"] = stats, statsSource, fresh
	item["resource_version"] = Digest([]any{cache.revision, binding, entry.association, fresh})
	return item
}
