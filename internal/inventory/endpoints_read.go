package inventory

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

const (
	endpointListEvidence = 2
	endpointPageBytes    = 40 << 10
)

type endpointSelection struct {
	bridge, port, mac, ip, filter string
	vlan, limit                   int
}

func endpointIP(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	addr, err := netip.ParseAddr(raw)
	if len(raw) > 45 || err != nil || addr.Zone() != "" || addr.Is4In6() {
		return "", apitypes.Fail(422, "INVALID_ENDPOINT_FILTER")
	}
	return addr.String(), nil
}

func selectEndpoints(v *view, q url.Values) (endpointSelection, error) {
	sel := endpointSelection{
		bridge: q.Get("bridge"), port: q.Get("port"), filter: strings.ToLower(q.Get("filter")),
		vlan: -1, limit: 100,
	}
	invalid := apitypes.Fail(422, "INVALID_ENDPOINT_FILTER")
	if len(sel.filter) > 1024 {
		return sel, invalid
	}
	if raw := q.Get("mac"); raw != "" {
		var ok bool
		if sel.mac, ok = fdbMAC(raw); !ok {
			return sel, invalid
		}
	}
	var err error
	if sel.ip, err = endpointIP(q.Get("ip")); err != nil {
		return sel, err
	}
	if raw := q.Get("vlan"); raw != "" {
		vlan, err := strconv.ParseUint(raw, 10, 12)
		if err != nil {
			return sel, invalid
		}
		sel.vlan = int(vlan)
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			return sel, apitypes.Fail(422, "INVALID_LIMIT")
		}
		sel.limit = limit
	}
	sel.bridge, err = selectManagedBridgePort(v, sel.bridge, sel.port, "INVALID_ENDPOINT_FILTER")
	return sel, err
}

// A queried value may only select records from sources that establish it.
// A protocol identifier that happens to be shaped like a MAC stays an LLDP
// advertisement claim, and a Linux neighbor's local Port supplies no VLAN.
func (s endpointSelection) sourceGap(kind string) string {
	switch {
	case s.vlan >= 0 && kind != "fdb":
		return "VLAN_NOT_REPORTED"
	case s.ip != "" && kind != "neighbor":
		return "IP_NOT_REPORTED"
	case s.mac != "" && kind == "lldp":
		return "MAC_NOT_VERIFIED"
	default:
		return ""
	}
}

func (s endpointSelection) matches(g endpointGroup) bool {
	return (s.mac == "" || s.mac == g.mac) && (s.ip == "" || g.hasAddress(s.ip)) &&
		(s.vlan < 0 || g.vlan != nil && s.vlan == int(*g.vlan)) && g.matchesText(s.filter)
}

type endpointSourceCoverage struct {
	Kind         string         `json:"kind"`
	Source       map[string]any `json:"source"`
	Availability string         `json:"availability"`
	Reason       string         `json:"reason,omitempty"`
	ScopeReason  string         `json:"scope_reason,omitempty"`
	ScopeUnit    string         `json:"scope_unit"`
	Eligible     int            `json:"eligible"`
	Observed     int            `json:"observed"`
	Truncated    bool           `json:"truncated"`
}

type endpointSnapshot struct {
	groups                  []endpointGroup
	sources                 []endpointSourceCoverage
	id                      string
	availability, freshness string
	truncated               bool
}

func endpointFDBSource(result fdbResult, scopeReason string) endpointSourceCoverage {
	status := result.availability
	if status == "available" {
		status = "complete"
	}
	confidence := "unknown"
	if result.freshness == "fresh" {
		confidence = "proven"
		if status == "degraded" {
			confidence = "partial"
		}
	}
	var observed any
	if !result.observedAt.IsZero() {
		observed = result.observedAt.UTC()
	}
	return endpointSourceCoverage{
		Kind: "fdb", Availability: status, Reason: result.reason, ScopeReason: scopeReason,
		ScopeUnit: "bridge", Eligible: result.coverage.BridgesTotal, Observed: result.coverage.BridgesSampled,
		Truncated: result.coverage.Truncated,
		Source: map[string]any{"provider_id": "ovs-vswitchd", "authority": "native-ovs-fdb",
			"freshness": result.freshness, "confidence": confidence, "observed_at": observed},
	}
}

func evaluateEndpoints(v *view, failure string, fdb fdbCache, neighbors neighborCache, lldp lldpCache, now time.Time, sel endpointSelection) endpointSnapshot {
	fdbData := evaluateFDB(v, failure, fdb, now, fdbSelection{bridge: sel.bridge, port: sel.port, mac: sel.mac, vlan: sel.vlan})
	scope := neighborSelection{bridge: sel.bridge, port: sel.port}
	neighborState := statusNeighbors(v, failure, neighbors, now, scope)
	lldpState := lldpStatus(v, failure, lldp, scope, now)
	neighborMeta, lldpMeta := neighborSource(neighbors, neighborState), lldpSource(lldp, lldpState)
	sources := []endpointSourceCoverage{
		endpointFDBSource(fdbData, sel.sourceGap("fdb")),
		{Kind: "neighbor", Source: neighborMeta, Availability: neighborState.availability, Reason: neighborState.reason,
			ScopeReason: sel.sourceGap("neighbor"), ScopeUnit: "interface", Eligible: neighborState.eligible,
			Observed: neighborState.observed, Truncated: neighborState.freshness == "fresh" && neighbors.truncated},
		{Kind: "lldp", Source: lldpMeta, Availability: lldpState.availability, Reason: lldpState.reason,
			ScopeReason: sel.sourceGap("lldp"), ScopeUnit: "interface", Eligible: lldpState.eligible,
			Observed: lldpState.observed, Truncated: lldpState.freshness == "fresh" && lldp.truncated},
	}
	var fdbRows []fdbEntry
	var neighborRows []neighborEntry
	var lldpRows []lldpPeer
	var versions [3]string
	if fdbData.freshness == "fresh" && sources[0].ScopeReason == "" {
		fdbRows, versions[0] = fdbData.entries, fdb.id
	}
	if neighborState.freshness == "fresh" && sources[1].ScopeReason == "" {
		for _, row := range neighbors.entries {
			if scope.includes(row.Binding) {
				neighborRows = append(neighborRows, row)
			}
		}
		versions[1] = neighbors.id
	}
	if lldpState.freshness == "fresh" && sources[2].ScopeReason == "" {
		for _, peer := range lldp.peers {
			if peer.active(now) && scope.includes(peer.binding) {
				lldpRows = append(lldpRows, peer)
			}
		}
		versions[2] = lldp.id
	}

	published := endpointSnapshot{sources: sources, availability: "unavailable", freshness: "unavailable"}
	states := make([][]any, 0, len(sources))
	complete, fresh, stale := true, false, false
	for _, source := range sources {
		freshness := source.Source["freshness"]
		states = append(states, []any{source.Availability, source.Reason, source.ScopeReason, source.Eligible, source.Observed, source.Truncated, freshness})
		complete = complete && source.Availability == "complete" && source.ScopeReason == ""
		if source.ScopeReason == "" {
			fresh = fresh || freshness == "fresh"
			stale = stale || freshness == "stale"
			published.truncated = published.truncated || source.Truncated
		}
	}
	switch {
	case complete:
		published.availability, published.freshness = "complete", "fresh"
	case fresh:
		published.availability, published.freshness = "degraded", "fresh"
	case stale:
		published.freshness = "stale"
	}

	groupIDs := make([]string, 0, len(fdbRows)+len(neighborRows)+len(lldpRows))
	for _, group := range buildEndpoints(fdbRows, neighborRows, lldpRows, neighborMeta, lldpMeta) {
		if sel.matches(group) {
			published.groups = append(published.groups, group)
			groupIDs = append(groupIDs, group.id)
		}
	}
	// Heartbeats may advance provider timestamps without changing membership.
	// TTL expiry, coverage changes, and new applicable snapshots invalidate
	// the cursor even if the next result happens to have the same row count.
	published.id = schemaID("endpoint-page-v1", Digest([]any{v.decision.Generation, versions, states, groupIDs}))
	return published
}

func (s *Service) readEndpoints(op string, path map[string]string, q url.Values, claims authn.Claims) (any, error) {
	if !slices.Contains(claims.Capabilities, "state.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	s.mu.RLock()
	v, failure, fdb, neighbors, lldp := s.current, s.failure, s.fdb, s.neighbors, s.lldp
	s.mu.RUnlock()
	if v == nil {
		return nil, apitypes.Fail(503, "OVSDB_RESYNCING")
	}
	if v.decision.State != "confirmed" {
		return nil, apitypes.Fail(503, "INSTANCE_RECONCILIATION_REQUIRED")
	}
	now := s.now()
	sequence := map[string]uint64{"fdb": fdb.sequence, "neighbor": neighbors.sequence, "lldp": lldp.sequence}
	if op == "readEndpoint" {
		preferredIP, err := endpointIP(q.Get("ip"))
		if err != nil {
			return nil, err
		}
		all := evaluateEndpoints(v, failure, fdb, neighbors, lldp, now, endpointSelection{vlan: -1})
		for _, group := range all.groups {
			if group.id == path["endpoint_id"] {
				return group.resource(sequence[group.basis], maxEndpointDetailEvidence, preferredIP), nil
			}
		}
		if all.freshness == "stale" {
			return nil, apitypes.Fail(503, "ENDPOINT_STALE")
		}
		if all.availability != "complete" {
			return nil, apitypes.Fail(503, "ENDPOINT_COVERAGE_LIMITED")
		}
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}

	sel, err := selectEndpoints(v, q)
	if err != nil {
		return nil, err
	}
	data := evaluateEndpoints(v, failure, fdb, neighbors, lldp, now, sel)
	cur := cursor{Principal: claims.PrincipalID, Permission: claims.Revision, Operation: op, Filter: sel.filter,
		Selection:  Digest([]any{claims.CredentialID, sel.bridge, sel.port, sel.mac, sel.ip, sel.vlan}),
		Generation: v.decision.Generation, Snapshot: data.id, Limit: sel.limit, Expires: now.Add(30 * time.Second).UnixMilli(),
		Order: "endpoint-v1"}
	pageKey := []byte("endpoint-page-v1")
	cur, err = s.observationCursor(q.Get("cursor"), pageKey, cur, now, data.freshness == "fresh")
	if err != nil {
		return nil, err
	}
	start := 0
	if cur.After != "" {
		index := slices.IndexFunc(data.groups, func(group endpointGroup) bool { return group.id == cur.After })
		if index < 0 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		start = index + 1
	}
	items := make([]map[string]any, 0, min(sel.limit, 50))
	used := 0
	for _, group := range data.groups[start:] {
		if len(items) >= min(sel.limit, 50) {
			break
		}
		item := group.resource(sequence[group.basis], endpointListEvidence, sel.ip)
		wire, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if used+len(wire) > endpointPageBytes && len(items) > 0 {
			break
		}
		items = append(items, item)
		used += len(wire)
	}
	var next any
	if start+len(items) < len(data.groups) {
		cur.After = data.groups[start+len(items)-1].id
		next, err = s.sealObservationCursor(cur, pageKey)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"snapshot_id": data.id, "instance_generation": v.decision.Generation, "items": items,
		"next_cursor": next, "truncated": data.truncated, "availability": data.availability,
		"freshness": data.freshness, "matched": len(data.groups), "sources": data.sources,
	}, nil
}
