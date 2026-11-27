package inventory

import "github.com/sampsonlor/ovs-webui/internal/supportbundle"

// SupportOVSSnapshot reads only current provenance and aggregate counts. It
// never renders private rows or samples the provider on a bundle request.
func (s *Service) SupportOVSSnapshot() *supportbundle.OVSSnapshot {
	out := &supportbundle.OVSSnapshot{State: "unknown"}
	if s == nil || !s.mu.TryRLock() {
		return out
	}
	defer s.mu.RUnlock()
	if s.failure == "OVSDB_RESYNCING" || s.current == nil {
		return out
	}
	if s.failure != "" || s.current.decision.State != "confirmed" {
		out.State = "unavailable"
		return out
	}
	at, now := s.current.observation.Evidence.ObservedAt, s.now()
	if at.IsZero() || now.Before(at) {
		return out
	}
	if now.Sub(at) > FreshFor {
		out.State = "stale"
		return out
	}
	rows := s.current.observation.Rows
	out.State = "ready"
	out.Topology = &supportbundle.TopologyCounts{Bridges: len(rows["Bridge"]), Ports: len(rows["Port"]), Interfaces: len(rows["Interface"])}
	if len(rows["Open_vSwitch"]) == 1 {
		for _, root := range rows["Open_vSwitch"] {
			out.Version, _ = root.Values["ovs_version"].(string)
		}
	}
	return out
}
