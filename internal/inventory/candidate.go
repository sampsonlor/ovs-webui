package inventory

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// SetLocalVLANPorts is configured by the root-owned mgrd process, never HTTP or
// a client capability assertion. Empty means ownership remains unknown.
func (s *Service) SetLocalVLANPorts(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_VLAN_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_VLAN_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localVLAN = allow
	return nil
}
func integers(v any) ([]int, bool) {
	values, ok := v.([]any)
	if !ok {
		values = []any{v}
	}
	out := []int{}
	for _, v := range values {
		var n int64
		switch x := v.(type) {
		case string:
			// The OVSDB adapter preserves exact signed native integers as decimal
			// strings. Narrow to the VLAN range only at this domain boundary.
			var err error
			n, err = strconv.ParseInt(x, 10, 64)
			if err != nil {
				return nil, false
			}
		case int:
			n = int64(x)
		case int64:
			n = x
		case float64:
			if x != float64(int64(x)) {
				return nil, false
			}
			n = int64(x)
		case json.Number:
			var err error
			n, err = x.Int64()
			if err != nil {
				return nil, false
			}
		default:
			return nil, false
		}
		if n < 0 || n > 4095 {
			return nil, false
		}
		out = append(out, int(n))
	}
	sort.Ints(out)
	if len(slices.Compact(append([]int{}, out...))) != len(out) {
		return nil, false
	}
	return out, true
}
func nativeVLAN(row Row) (candidate.VLAN, bool) {
	var out candidate.VLAN
	mode, exists := row.Values["vlan_mode"]
	if !exists {
		return out, false
	}
	if a, ok := mode.([]any); ok {
		if len(a) > 1 {
			return out, false
		}
		if len(a) == 1 {
			mode = a[0]
		} else {
			mode = nil
		}
	}
	if mode != nil {
		s, ok := mode.(string)
		if !ok {
			return out, false
		}
		out.Mode = &s
	}
	tags, ok := integers(row.Values["tag"])
	if !ok || len(tags) > 1 {
		return out, false
	}
	if len(tags) == 1 {
		out.Tag = &tags[0]
	}
	out.Trunks, ok = integers(row.Values["trunks"])
	if !ok {
		return out, false
	}
	out.CVLANs, ok = integers(row.Values["cvlans"])
	if !ok {
		return out, false
	}
	return out, true
}
func externalControl(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for key := range m {
		if strings.HasPrefix(key, "ovn-") || strings.HasPrefix(key, "neutron:") || key == "iface-id" || key == "attached-mac" {
			return true
		}
	}
	return false
}
func (s *Service) CandidateSnapshot(ctx context.Context, bindings []candidate.Binding) (candidate.Snapshot, error) {
	var out candidate.Snapshot
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if len(bindings) > candidate.MaxIntents*2 {
		return out, apitypes.Fail(429, "CANDIDATE_INTENT_LIMIT")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.current
	if v == nil || s.failure != "" || s.now().Before(v.observation.Evidence.ObservedAt) || s.now().Sub(v.observation.Evidence.ObservedAt) > FreshFor {
		return out, apitypes.Fail(503, "PROVIDER_UNAVAILABLE")
	}
	if v.decision.State != "confirmed" {
		return out, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	return candidateSnapshot(v, s.localVLAN, bindings), nil
}

// Project the same immutable observation and authority policy for validation and
// inventory edit hints. These hints never replace admission authorization.
func candidateSnapshot(v *view, localVLAN map[string]bool, bindings []candidate.Binding) candidate.Snapshot {
	out := candidate.Snapshot{Generation: v.decision.Generation, Schema: v.observation.Schema.Digest, Policy: Digest(localVLAN), Ports: map[string]candidate.Port{}}
	compatible := 0
	modes := []string{}
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Port" {
			for _, c := range t.Columns {
				if c.VLANCompatible && c.Monitored {
					compatible++
					if c.Name == "vlan_mode" {
						modes = append(modes, c.VLANModes...)
					}
				}
			}
		}
	}
	for _, requested := range bindings {
		b := v.decision.Bindings[Key("Port", requested.OVSUUID)]
		if b.ManagementID != requested.ManagementID || b.State != "active" {
			continue
		}
		row, ok := v.observation.Rows["Port"][b.UUID]
		if !ok {
			continue
		}
		p := candidate.Port{Binding: candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Port", Generation: out.Generation}, SchemaSupported: compatible == 4, Modes: modes, Authority: "unknown"}
		p.VLAN, p.Known = nativeVLAN(row)
		bridge, exists := parent(v, "Bridge", "ports", b.UUID)
		if !exists {
			p.Known = false
		}
		// One parent and all member interfaces are required. Other ports, link
		// counters and unrelated fields do not become a global conflict gate.
		parents := 0
		for _, parent := range v.observation.Rows["Bridge"] {
			if slices.Contains(refs(parent.Values["ports"]), b.UUID) {
				parents++
			}
		}
		members := map[string]any{}
		externalMember := false
		for _, id := range refs(row.Values["interfaces"]) {
			member, ok := v.observation.Rows["Interface"][id]
			if !ok {
				p.Known = false
			}
			members[id] = []any{member.Values["name"], member.Values["type"], member.Values["options"]}
			if externalControl(member.Values["external_ids"]) {
				externalMember = true
			}
		}
		if parents != 1 || len(members) == 0 {
			p.Known = false
		}
		if localVLAN[b.ManagementID] {
			p.Authority = "local-managed"
		}
		if externalMember || externalControl(row.Values["external_ids"]) || externalControl(bridge.Values["external_ids"]) {
			p.Authority = "externally-controlled"
		}
		for _, root := range v.observation.Rows["Open_vSwitch"] {
			if externalControl(root.Values["external_ids"]) {
				p.Authority = "externally-controlled"
			}
		}
		p.Dependency = Digest([]any{bridge.UUID, bridge.Values["name"], bridge.Values["datapath_type"], row.Values["name"], members, p.Authority})
		out.Ports[b.ManagementID] = p
	}
	// Copy projected values before releasing the lock. No caller receives live
	// provider maps or native options; dependency material is only a digest.
	out.Revision = Digest([]any{out.Generation, out.Schema, out.Ports})
	return out
}
