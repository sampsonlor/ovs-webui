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

type neighborSelection struct {
	bridge, port, iface, filter, family string
}

func (s neighborSelection) includes(binding neighborBinding) bool {
	return (s.bridge == "" || s.bridge == binding.bridge.id) && (s.port == "" || s.port == binding.port.id) &&
		(s.iface == "" || s.iface == binding.managementID)
}

func (s neighborSelection) matches(row neighborEntry) bool {
	return s.includes(row.Binding) && (s.family == "" || s.family == row.Family) && (s.filter == "" ||
		strings.Contains(row.IP, s.filter) || strings.Contains(row.MAC, s.filter) || strings.Contains(strings.ToLower(row.Binding.request.Name), s.filter))
}

func selectNeighbors(v *view, q url.Values) (neighborSelection, int, error) {
	selection := neighborSelection{q.Get("bridge_id"), q.Get("port_id"), q.Get("interface_id"), strings.ToLower(q.Get("filter")), q.Get("family")}
	if len(q.Get("filter")) > 1024 || selection.family != "" && selection.family != "ipv4" && selection.family != "ipv6" {
		return selection, 0, apitypes.Fail(422, "INVALID_NEIGHBOR_FILTER")
	}
	limit := 100
	if q.Get("limit") != "" {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			return selection, 0, apitypes.Fail(422, "INVALID_LIMIT")
		}
	}
	for _, item := range []struct{ table, id string }{{"Bridge", selection.bridge}, {"Port", selection.port}, {"Interface", selection.iface}} {
		if item.id == "" {
			continue
		}
		if !apitypes.ManagementID(item.id) {
			return selection, 0, apitypes.Fail(422, "INVALID_NEIGHBOR_SELECTION")
		}
		found := false
		for _, binding := range v.decision.Bindings {
			if binding.Table == item.table && binding.State == "active" && binding.ManagementID == item.id {
				found = true
				break
			}
		}
		if !found {
			return selection, 0, apitypes.Fail(404, "NOT_FOUND")
		}
	}
	return selection, limit, nil
}

type neighborStatus struct {
	availability, reason, freshness string
	eligible, observed              int
}

func statusNeighbors(v *view, failure string, c neighborCache, now time.Time, selection neighborSelection) neighborStatus {
	status := neighborStatus{availability: "unavailable", freshness: "unavailable"}
	covered := make(map[DeviceRequest]bool, len(c.covered))
	for _, request := range c.covered {
		covered[request] = true
	}
	for _, group := range [][]neighborBinding{v.neighborScope.bindings, v.neighborScope.ambiguous} {
		for _, binding := range group {
			if selection.includes(binding) {
				status.eligible++
				if covered[binding.request] {
					status.observed++
				}
			}
		}
	}
	switch {
	case !deviceViewFresh(v, failure, now):
		status.reason, status.freshness, status.observed = "OVS_ASSOCIATION_STALE", "stale", 0
	case status.eligible == 0:
		status.reason = "NEIGHBORS_NO_PROVEN_SCOPE"
	case c.observer == nil:
		status.reason = "NEIGHBORS_DUMP_UNAVAILABLE"
	case c.failure != "":
		status.reason = c.failure
	case c.id == "":
		status.reason = "NEIGHBORS_RESYNCING"
	case now.Sub(c.observedAt) > neighborFreshFor || now.Before(c.observedAt):
		status.reason, status.freshness, status.observed = "NEIGHBORS_EXPIRED", "stale", 0
	case status.observed == 0:
		status.reason = "NEIGHBORS_NO_PROVEN_SCOPE"
	case status.observed < status.eligible:
		status.availability, status.freshness, status.reason = "degraded", "fresh", "NEIGHBORS_PARTIAL_COVERAGE"
	case c.truncated:
		status.availability, status.freshness, status.reason = "degraded", "fresh", "NEIGHBORS_TRUNCATED"
	default:
		status.availability, status.freshness = "complete", "fresh"
	}
	return status
}

func neighborSource(c neighborCache, status neighborStatus) map[string]any {
	confidence := "unknown"
	if status.freshness == "fresh" {
		confidence = "proven"
		if status.availability == "degraded" {
			confidence = "partial"
		}
	}
	var at any
	if !c.observedAt.IsZero() {
		at = c.observedAt.UTC()
	}
	return map[string]any{"provider_id": "linux", "authority": "linux-rtnetlink-neighbors", "freshness": status.freshness, "confidence": confidence, "observed_at": at}
}

func (s *Service) readNeighbors(op string, path map[string]string, q url.Values, claims authn.Claims) (any, error) {
	if !slices.Contains(claims.Capabilities, "state.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	s.mu.RLock()
	v, failure, cache := s.current, s.failure, s.neighbors
	s.mu.RUnlock()
	if v == nil {
		return nil, apitypes.Fail(503, "OVSDB_RESYNCING")
	}
	if v.decision.State != "confirmed" {
		return nil, apitypes.Fail(503, "INSTANCE_RECONCILIATION_REQUIRED")
	}
	now := s.now()
	if op == "readNeighbor" {
		status := statusNeighbors(v, failure, cache, now, neighborSelection{})
		if status.freshness != "fresh" {
			return nil, apitypes.Fail(503, status.reason)
		}
		for _, row := range cache.entries {
			if row.ID == path["neighbor_id"] {
				return neighborResource(row, cache, neighborSource(cache, status)), nil
			}
		}
		if status.availability != "complete" {
			return nil, apitypes.Fail(503, status.reason)
		}
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	selection, limit, err := selectNeighbors(v, q)
	if err != nil {
		return nil, err
	}
	status := statusNeighbors(v, failure, cache, now, selection)
	snapshot := cache.id
	if snapshot == "" {
		snapshot = v.id
	}
	cur := cursor{Principal: claims.PrincipalID, Permission: claims.Revision, Operation: op, Filter: selection.filter,
		Selection:  Digest([]string{claims.CredentialID, selection.bridge, selection.port, selection.iface, selection.family}),
		Generation: v.decision.Generation, Snapshot: snapshot, Limit: limit, Expires: now.Add(30 * time.Second).UnixMilli(), Order: "linux-neighbor-v1"}
	pageKey := []byte("neighbor-page-v1")
	if token := q.Get("cursor"); token != "" {
		if len(token) > 4096 || status.freshness != "fresh" {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		sealed, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		plain, err := authn.Unseal(s.key, sealed, pageKey)
		var old cursor
		if err != nil || json.Unmarshal(plain, &old) != nil || old.After == "" || old.Expires <= now.UnixMilli() {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		cur.After, cur.Expires = old.After, old.Expires
		if cur != old {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
	}
	matched := make([]neighborEntry, 0)
	if status.freshness == "fresh" {
		for _, row := range cache.entries {
			if selection.matches(row) {
				matched = append(matched, row)
			}
		}
	}
	start := 0
	if cur.After != "" {
		index := slices.IndexFunc(matched, func(e neighborEntry) bool { return e.ID == cur.After })
		if index < 0 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		start = index + 1
	}
	source := neighborSource(cache, status)
	items := make([]map[string]any, 0, min(limit, 50))
	size := 0
	for _, row := range matched[start:] {
		item := neighborResource(row, cache, source)
		encoded, _ := json.Marshal(item)
		if len(items) >= min(limit, 50) || len(items) > 0 && size+len(encoded) > 40<<10 {
			break
		}
		items, size = append(items, item), size+len(encoded)
	}
	var next any
	if start+len(items) < len(matched) {
		cur.After = matched[start+len(items)-1].ID
		plain, _ := json.Marshal(cur)
		sealed, err := authn.Seal(s.key, plain, pageKey)
		if err != nil {
			return nil, err
		}
		next = base64.RawURLEncoding.EncodeToString(sealed)
	}
	return map[string]any{
		"snapshot_id": snapshot, "instance_generation": v.decision.Generation, "items": items, "next_cursor": next,
		"truncated": cache.truncated && status.freshness == "fresh", "availability": status.availability, "reason": nullableObservationText(status.reason), "source": source,
		"coverage": map[string]any{"scope": "managed-linux-interfaces", "eligible_interfaces": status.eligible, "observed_interfaces": status.observed,
			"matched_entries": len(matched), "returned_entries": len(items), "max_entries": MaxNeighbors},
	}, nil
}

func neighborResource(row neighborEntry, c neighborCache, source map[string]any) map[string]any {
	b := row.Binding
	var port, bridge any
	if b.port.id != "" {
		port = apitypes.Ref{Kind: "port", ID: b.port.id}
	}
	if b.bridge.id != "" {
		bridge = apitypes.Ref{Kind: "bridge", ID: b.bridge.id}
	}
	return map[string]any{
		"id": row.ID, "resource_kind": "neighbor", "state": row.State, "sequence": strconv.FormatUint(c.sequence, 10), "source": source, "allowed_actions": []string{},
		"address": row.IP, "mac": nullableObservationText(row.MAC), "family": row.Family, "ifindex": b.request.IfIndex, "interface_name": b.request.Name,
		"interface_ref": apitypes.Ref{Kind: "interface", ID: b.managementID}, "port_ref": port, "bridge_ref": bridge,
		"port_name": nullableObservationText(b.port.name), "bridge_name": nullableObservationText(b.bridge.name),
	}
}
