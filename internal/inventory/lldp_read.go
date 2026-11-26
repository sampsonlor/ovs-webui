package inventory

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

func lldpStatus(v *view, failure string, c lldpCache, selection neighborSelection, now time.Time) neighborStatus {
	out := neighborStatus{availability: "unavailable", freshness: "unavailable"}
	if len(c.grants) == 0 {
		out.reason = "LLDP_PROVIDER_DISABLED"
		return out
	}
	if selection.bridge == "" && selection.port == "" && selection.iface == "" {
		out.eligible = len(c.grants)
	} else {
		for _, binding := range lldpBindings(v, c.grants) {
			if selection.includes(binding) {
				out.eligible++
			}
		}
		if selection.iface != "" && out.eligible == 0 && slices.Contains(c.grants, selection.iface) {
			out.eligible = 1
		}
	}
	if !deviceViewFresh(v, failure, now) {
		out.reason, out.freshness = "OVS_ASSOCIATION_STALE", "stale"
		return out
	}
	if out.eligible == 0 {
		out.reason = "LLDP_SCOPE_NOT_COVERED"
		return out
	}
	if c.failure != "" {
		out.reason = c.failure
		if out.reason == "OVS_ASSOCIATION_STALE" {
			out.reason = "LLDP_RESYNCING"
		}
		return out
	}
	if c.observedAt.IsZero() {
		out.reason = "LLDP_RESYNCING"
		return out
	}
	if age := now.Sub(c.observedAt); age < 0 || age > lldpFreshFor {
		out.reason, out.freshness = "LLDP_EXPIRED", "stale"
		return out
	}
	for _, binding := range lldpBindings(v, c.grants) {
		if selection.includes(binding) && slices.Contains(c.covered, binding.request) {
			out.observed++
		}
	}
	if out.observed == 0 {
		out.reason = "LLDP_SCOPE_NOT_COVERED"
		return out
	}
	out.availability, out.freshness = "complete", "fresh"
	if c.truncated {
		out.availability, out.reason = "degraded", "LLDP_CAPTURE_TRUNCATED"
	} else if out.observed < out.eligible {
		out.availability, out.reason = "degraded", "LLDP_CAPTURE_PARTIAL"
	}
	return out
}

func lldpSource(c lldpCache, status neighborStatus) map[string]any {
	confidence := "unknown"
	if status.freshness == "fresh" {
		// The contents of a peer's advertisement are claims, not identity proof.
		confidence = "partial"
	}
	var at any
	if !c.observedAt.IsZero() && status.reason != "LLDP_SCOPE_NOT_COVERED" {
		at = c.observedAt.UTC()
	}
	return map[string]any{"provider_id": "linux", "authority": "linux-passive-lldp", "freshness": status.freshness, "confidence": confidence, "observed_at": at}
}

func (s *Service) readLLDP(op string, path map[string]string, q url.Values, claims authn.Claims) (any, error) {
	if !slices.Contains(claims.Capabilities, "state.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	s.mu.RLock()
	v, failure, cache := s.current, s.failure, s.lldp
	s.mu.RUnlock()
	if v == nil {
		return nil, apitypes.Fail(503, "OVSDB_RESYNCING")
	}
	if v.decision.State != "confirmed" {
		return nil, apitypes.Fail(503, "INSTANCE_RECONCILIATION_REQUIRED")
	}
	now := s.now()
	if op == "readLldpNeighbor" {
		status := lldpStatus(v, failure, cache, neighborSelection{}, now)
		if status.freshness != "fresh" {
			return nil, apitypes.Fail(503, status.reason)
		}
		for _, peer := range cache.peers {
			if peer.id == path["lldp_neighbor_id"] && peer.active(now) {
				return lldpResource(peer, cache, lldpSource(cache, status)), nil
			}
		}
		if status.availability != "complete" {
			return nil, apitypes.Fail(503, status.reason)
		}
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	selection, limit, err := selectObservedInterfaces(v, q, "INVALID_LLDP_FILTER", "INVALID_LLDP_SELECTION")
	if err != nil {
		return nil, err
	}
	status := lldpStatus(v, failure, cache, selection, now)
	matched := make([]lldpPeer, 0)
	if status.freshness == "fresh" {
		for _, peer := range cache.peers {
			if peer.active(now) && selection.includes(peer.binding) && lldpMatches(peer, selection.filter) {
				matched = append(matched, peer)
			}
		}
	}
	snapshot := v.id
	if cache.id != "" {
		// Expiration can change the selected result between sampler ticks.
		snapshot = schemaID(cache.id, strconv.Itoa(len(matched)))
	}
	cur := cursor{Principal: claims.PrincipalID, Permission: claims.Revision, Operation: op, Filter: selection.filter,
		Selection:  Digest([]string{claims.CredentialID, selection.bridge, selection.port, selection.iface}),
		Generation: v.decision.Generation, Snapshot: snapshot, Limit: limit, Expires: now.Add(30 * time.Second).UnixMilli(), Order: "linux-lldp-v1"}
	pageKey := []byte("lldp-page-v1")
	cur, err = s.observationCursor(q.Get("cursor"), pageKey, cur, now, status.freshness == "fresh")
	if err != nil {
		return nil, err
	}
	start := 0
	if cur.After != "" {
		index := slices.IndexFunc(matched, func(peer lldpPeer) bool { return peer.id == cur.After })
		if index < 0 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		start = index + 1
	}
	source := lldpSource(cache, status)
	items := make([]map[string]any, 0, min(limit, 50))
	size := 0
	for _, row := range matched[start:] {
		item := lldpResource(row, cache, source)
		encoded, _ := json.Marshal(item)
		if len(items) >= min(limit, 50) || len(items) > 0 && size+len(encoded) > 40<<10 {
			break
		}
		items, size = append(items, item), size+len(encoded)
	}
	var next any
	if start+len(items) < len(matched) {
		cur.After = matched[start+len(items)-1].id
		next, err = s.sealObservationCursor(cur, pageKey)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"snapshot_id": snapshot, "instance_generation": v.decision.Generation, "items": items, "next_cursor": next,
		"truncated": cache.truncated && status.freshness == "fresh", "availability": status.availability, "reason": nullableObservationText(status.reason), "source": source,
		"coverage": map[string]any{"scope": "opted-in-managed-interfaces", "configured_interfaces": status.eligible,
			"observed_interfaces": status.observed, "matched_entries": len(matched), "returned_entries": len(items), "max_entries": MaxLLDPNeighbors},
	}, nil
}

func lldpMatches(peer lldpPeer, filter string) bool {
	for _, value := range []string{peer.frame.ChassisID.Value, peer.frame.PortID.Value, peer.frame.SystemName, peer.binding.request.Name} {
		if strings.Contains(strings.ToLower(value), filter) {
			return true
		}
	}
	return false
}

func lldpResource(peer lldpPeer, c lldpCache, source map[string]any) map[string]any {
	b, frame := peer.binding, peer.frame
	var port, bridge any
	if b.port.id != "" {
		port = apitypes.Ref{Kind: "port", ID: b.port.id}
	}
	if b.bridge.id != "" {
		bridge = apitypes.Ref{Kind: "bridge", ID: b.bridge.id}
	}
	return map[string]any{
		"id": peer.id, "resource_kind": "lldp_neighbor", "state": "observed", "sequence": strconv.FormatUint(c.sequence, 10), "source": source, "allowed_actions": []string{},
		"ifindex": b.request.IfIndex, "interface_name": b.request.Name, "interface_ref": apitypes.Ref{Kind: "interface", ID: b.managementID},
		"port_ref": port, "bridge_ref": bridge, "port_name": nullableObservationText(b.port.name), "bridge_name": nullableObservationText(b.bridge.name),
		"lldp": map[string]any{"chassis_id": frame.ChassisID, "port_id": frame.PortID, "system_name": nullableObservationText(frame.SystemName),
			"ttl_seconds": frame.TTL, "last_seen_at": frame.ReceivedAt.UTC()},
	}
}
