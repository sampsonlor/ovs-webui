package inventory

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

type fdbSelection struct {
	mac, bridge, port, filter string
	vlan                      int
	limit                     int
}

func selectFDB(v *view, q url.Values) (fdbSelection, error) {
	selection := fdbSelection{bridge: q.Get("bridge"), port: q.Get("port"), filter: strings.ToLower(q.Get("filter")), vlan: -1, limit: 100}
	invalid := apitypes.Fail(422, "INVALID_FDB_FILTER")
	if len(selection.filter) > 1024 {
		return selection, invalid
	}
	if raw := q.Get("mac"); raw != "" {
		mac, ok := fdbMAC(raw)
		if !ok {
			return selection, invalid
		}
		selection.mac = mac
	}
	if raw := q.Get("vlan"); raw != "" {
		vlan, err := strconv.ParseUint(raw, 10, 12)
		if err != nil {
			return selection, invalid
		}
		selection.vlan = int(vlan)
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			return selection, apitypes.Fail(422, "INVALID_LIMIT")
		}
		selection.limit = limit
	}
	bridge, err := selectManagedBridgePort(v, selection.bridge, selection.port, "INVALID_FDB_FILTER")
	selection.bridge = bridge
	return selection, err
}

// Scope by the current managed topology before either observation is projected.
func selectManagedBridgePort(v *view, bridge, port, invalidCode string) (string, error) {
	invalid := apitypes.Fail(422, invalidCode)
	for _, id := range []string{bridge, port} {
		if id != "" && !apitypes.ManagementID(id) {
			return "", invalid
		}
	}
	roots := refs(v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root].Values["bridges"])
	if port != "" {
		parent := ""
		for _, uuid := range roots {
			b := v.decision.Bindings[Key("Bridge", uuid)]
			if b.State != "active" {
				continue
			}
			for _, pid := range refs(v.observation.Rows["Bridge"][uuid].Values["ports"]) {
				p := v.decision.Bindings[Key("Port", pid)]
				if p.State != "active" || p.ManagementID != port {
					continue
				}
				if parent != "" && parent != b.ManagementID {
					return "", invalid
				}
				parent = b.ManagementID
			}
		}
		if parent == "" {
			return "", apitypes.Fail(404, "NOT_FOUND")
		}
		if bridge != "" && bridge != parent {
			return "", invalid
		}
		bridge = parent
	}
	if bridge != "" && !slices.ContainsFunc(roots, func(uuid string) bool {
		b := v.decision.Bindings[Key("Bridge", uuid)]
		return b.State == "active" && b.ManagementID == bridge
	}) {
		return "", apitypes.Fail(404, "NOT_FOUND")
	}
	return bridge, nil
}

func (s fdbSelection) matches(e fdbEntry) bool {
	return (s.mac == "" || s.mac == e.MAC) && (s.vlan == -1 || s.vlan == int(*e.VLAN)) &&
		(s.port == "" || s.port == e.port.ManagementID) &&
		(s.filter == "" || strings.Contains(e.MAC, s.filter) || strings.Contains(strings.ToLower(e.bridge.name), s.filter) ||
			e.PortName != nil && strings.Contains(strings.ToLower(*e.PortName), s.filter))
}

type fdbCoverage struct {
	BridgesSampled int    `json:"bridges_sampled"`
	BridgesTotal   int    `json:"bridges_total"`
	EntriesSampled int    `json:"entries_sampled"`
	EntriesTotal   uint64 `json:"entries_total"`
	Truncated      bool   `json:"truncated"`
}

type fdbResult struct {
	availability, freshness, reason string
	observedAt                      time.Time
	coverage                        fdbCoverage
	entries                         []fdbEntry
}

func evaluateFDB(v *view, failure string, cache fdbCache, now time.Time, sel fdbSelection) fdbResult {
	result := fdbResult{availability: "unavailable", freshness: "unavailable"}
	roots := refs(v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root].Values["bridges"])
	for _, uuid := range roots {
		b := v.decision.Bindings[Key("Bridge", uuid)]
		if b.State == "active" && (sel.bridge == "" || b.ManagementID == sel.bridge) {
			result.coverage.BridgesTotal++
		}
	}
	switch {
	case !deviceViewFresh(v, failure, now):
		result.freshness, result.reason = "stale", "epoch_changed"
		return result
	case cache.observer == nil:
		result.reason = "disabled"
		return result
	case len(cache.scope) == 0 || sel.bridge != "" && !slices.ContainsFunc(cache.scope, func(b fdbBridge) bool {
		return b.binding.ManagementID == sel.bridge
	}):
		result.reason = "no_managed_bridges"
		return result
	case cache.id == "":
		result.reason = "not_ready"
		return result
	}

	unresolvedPort := false
	var firstFailure string
	for _, bridge := range cache.scope {
		id := bridge.binding.ManagementID
		if sel.bridge != "" && sel.bridge != id {
			continue
		}
		sample := cache.samples[id]
		reason := sample.failure
		if reason == "" && (sample.observedAt.IsZero() || now.Sub(sample.observedAt) > fdbFreshFor || now.Before(sample.observedAt)) {
			reason = "expired"
		}
		if reason != "" {
			if firstFailure == "" {
				firstFailure = reason
			} else if firstFailure != reason {
				firstFailure = "partial"
			}
			continue
		}
		if result.observedAt.IsZero() || sample.observedAt.Before(result.observedAt) {
			result.observedAt = sample.observedAt
		}
		result.coverage.BridgesSampled++
		result.coverage.EntriesTotal += uint64(sample.total)
		result.coverage.EntriesSampled += len(sample.entries)
		result.coverage.Truncated = result.coverage.Truncated || sample.total > uint32(len(sample.entries))
		for _, e := range sample.entries {
			if sel.port != "" && e.portResolution != "resolved" {
				unresolvedPort = true
			}
			if sel.matches(e) {
				result.entries = append(result.entries, e)
			}
		}
	}
	if result.coverage.BridgesSampled == 0 {
		result.reason = firstFailure
		if firstFailure == "expired" {
			result.freshness = "stale"
		}
		return result
	}
	result.freshness, result.availability = "fresh", "available"
	switch {
	case result.coverage.BridgesSampled < result.coverage.BridgesTotal || unresolvedPort:
		result.availability, result.reason = "degraded", "partial"
	case result.coverage.Truncated:
		result.availability, result.reason = "degraded", "truncated"
	}
	slices.SortFunc(result.entries, func(a, b fdbEntry) int { return strings.Compare(a.id, b.id) })
	return result
}

func (s *Service) readFDB(op string, path map[string]string, q url.Values, claims authn.Claims) (any, error) {
	if !slices.Contains(claims.Capabilities, "state.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	s.mu.RLock()
	v, failure, cache := s.current, s.failure, s.fdb
	s.mu.RUnlock()
	if v == nil {
		return nil, apitypes.Fail(503, "OVSDB_RESYNCING")
	}
	if v.decision.State != "confirmed" {
		return nil, apitypes.Fail(503, "INSTANCE_RECONCILIATION_REQUIRED")
	}
	now := s.now()
	if op == "readFdbEntry" {
		result := evaluateFDB(v, failure, cache, now, fdbSelection{vlan: -1})
		for _, e := range result.entries {
			if e.id == path["fdb_entry_id"] {
				return fdbResource(e, cache.sequence), nil
			}
		}
		if result.availability != "available" {
			return nil, apitypes.Fail(503, "FDB_"+strings.ToUpper(result.reason))
		}
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	sel, err := selectFDB(v, q)
	if err != nil {
		return nil, err
	}
	result := evaluateFDB(v, failure, cache, now, sel)
	snapshot := cache.id
	if snapshot == "" {
		snapshot = v.id
	}
	cur := cursor{Principal: claims.PrincipalID, Permission: claims.Revision, Operation: op,
		Filter: sel.filter, Selection: Digest([]any{claims.CredentialID, sel.mac, sel.vlan, sel.bridge, sel.port, result.coverage.BridgesSampled}),
		Generation: v.decision.Generation, Snapshot: snapshot, Limit: sel.limit,
		Expires: now.Add(30 * time.Second).UnixMilli(), Order: "ovs-fdb-v1"}
	pageKey := []byte("ovs-fdb-page-v1")
	cur, err = s.observationCursor(q.Get("cursor"), pageKey, cur, now, result.freshness == "fresh")
	if err != nil {
		return nil, err
	}
	start := 0
	if cur.After != "" {
		index := slices.IndexFunc(result.entries, func(e fdbEntry) bool { return e.id == cur.After })
		if index < 0 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		start = index + 1
	}
	// Each row has a fixed-size shape and bounded native names. Even the
	// largest 50-row response fits inside the public transport budget.
	count := min(50, sel.limit, len(result.entries)-start)
	items := make([]map[string]any, 0, count)
	for _, e := range result.entries[start : start+count] {
		items = append(items, fdbResource(e, cache.sequence))
	}
	var next any
	if start+count < len(result.entries) {
		cur.After = result.entries[start+count-1].id
		next, err = s.sealObservationCursor(cur, pageKey)
		if err != nil {
			return nil, err
		}
	}
	page := map[string]any{"snapshot_id": snapshot, "instance_generation": v.decision.Generation,
		"items": items, "next_cursor": next, "truncated": result.coverage.Truncated,
		"source": "ovs_fdb", "availability": result.availability, "freshness": result.freshness, "coverage": result.coverage}
	if result.reason != "" {
		page["reason"] = result.reason
	}
	if !result.observedAt.IsZero() {
		page["observed_at"] = result.observedAt.UTC()
	}
	return page, nil
}

func fdbResource(e fdbEntry, sequence uint64) map[string]any {
	state := "static"
	row := map[string]any{"mac": e.MAC, "vlan": *e.VLAN, "bridge_name": e.bridge.name,
		"bridge_ref": apitypes.Ref{Kind: "bridge", ID: e.bridge.binding.ManagementID}, "port_resolution": e.portResolution}
	if e.PortName != nil {
		row["port_name"] = *e.PortName
	}
	if e.OFPort != nil {
		row["ofport"] = *e.OFPort
	}
	if e.AgeSeconds != nil {
		state = "learned"
		row["age_seconds"] = *e.AgeSeconds
		row["last_seen_at"] = e.observedAt.Add(-time.Duration(*e.AgeSeconds) * time.Second).UTC()
	}
	if e.portResolution == "resolved" {
		row["port_ref"] = apitypes.Ref{Kind: "port", ID: e.port.ManagementID}
	}
	return map[string]any{"id": e.id, "resource_kind": "fdb_entry", "state": state,
		"sequence": strconv.FormatUint(sequence, 10), "allowed_actions": []string{},
		"source": map[string]any{"provider_id": "ovs-vswitchd", "authority": "native-ovs-fdb",
			"freshness": "fresh", "confidence": "proven", "observed_at": e.observedAt.UTC()},
		"fdb": row}
}
