package inventory

import "time"

// HealthObservation contains only source metadata, with no native rows or
// scoped object identifiers. A zero timestamp means no sample has qualified.
type HealthObservation struct {
	Key, State, Reason, Authority string
	ObservedAt                    time.Time
}

// RuntimeHealth reads the manager's existing publications. It does not run
// probes, initiate collection, or change a provider's observation time.
func (s *Service) RuntimeHealth() []HealthObservation {
	view := []HealthObservation{
		{Key: "ovsdb", Authority: "ovsdb-monitor"},
		{Key: "cache-interface-devices", Authority: "linux-sysfs-observation"},
		{Key: "cache-statistics", Authority: "ovsdb-statistics"},
		{Key: "cache-neighbors", Authority: "linux-rtnetlink-neighbors"},
		{Key: "cache-lldp", Authority: "linux-passive-lldp"},
		{Key: "cache-fdb", Authority: "native-ovs-fdb"},
	}
	if s == nil {
		for i := range view {
			view[i].State, view[i].Reason = "unavailable", "INVENTORY_STORAGE_UNAVAILABLE"
		}
		return view
	}
	if !s.mu.TryRLock() {
		for i := range view {
			view[i].State, view[i].Reason = "unknown", "STATE_CACHE_BUSY"
		}
		return view
	}
	defer s.mu.RUnlock()
	now, current := s.now(), s.current
	ovs := &view[0]
	if current != nil {
		ovs.ObservedAt = current.observation.Evidence.ObservedAt
	}
	switch {
	case s.failure == "OVSDB_RESYNCING":
		ovs.State, ovs.Reason = "unknown", s.failure
	case s.failure != "":
		ovs.State, ovs.Reason = "unavailable", s.failure
	case current == nil:
		ovs.State, ovs.Reason = "unknown", "OVSDB_RESYNCING"
	case current.decision.State != "confirmed":
		ovs.State, ovs.Reason = "unavailable", "INSTANCE_RECONCILIATION_REQUIRED"
	default:
		cacheObservation(ovs, now, ovs.ObservedAt, FreshFor, "", false)
	}

	scopeSize := 0
	if current != nil {
		scopeSize = len(current.neighborScope.bindings) + len(current.neighborScope.ambiguous)
	}
	s.deviceHealth(&view[1], now, scopeSize)
	s.statisticHealth(&view[2], now, current)

	neighbors := s.neighbors
	cacheObservation(&view[3], now, neighbors.observedAt, neighborFreshFor, neighbors.failure,
		neighbors.truncated || len(neighbors.covered) < scopeSize)
	if neighbors.observer == nil {
		view[3].State, view[3].Reason = "unavailable", "NEIGHBORS_DUMP_UNAVAILABLE"
	} else if view[3].State == "degraded" && len(neighbors.covered) == 0 {
		view[3].State, view[3].Reason = "unknown", "NEIGHBORS_NO_PROVEN_SCOPE"
	}

	lldp := s.lldp
	if lldp.observer == nil {
		view[4].State, view[4].Reason = "disabled", "LLDP_NOT_CONFIGURED"
	} else {
		cacheObservation(&view[4], now, lldp.observedAt, lldpFreshFor, lldp.failure, lldp.truncated || len(lldp.covered) < len(lldp.grants))
		if view[4].State == "degraded" && len(lldp.covered) == 0 {
			view[4].State, view[4].Reason = "unknown", "LLDP_NO_PROVEN_SCOPE"
		}
	}
	fdbHealth(&view[5], now, s.fdb)
	if ovs.State != "ready" {
		for i := 1; i < len(view); i++ {
			if view[i].State != "disabled" {
				view[i].State, view[i].Reason = ovs.State, "OVS_ASSOCIATION_UNPROVEN"
			}
		}
	}
	return view
}

func cacheObservation(out *HealthObservation, now, at time.Time, ageLimit time.Duration, failure string, partial bool) {
	out.ObservedAt = at
	switch {
	case failure == "STATISTICS_RESYNCING", failure == "NEIGHBORS_RESYNCING",
		failure == "NEIGHBORS_SCOPE_CHANGED", failure == "LLDP_RESYNCING":
		out.State, out.Reason = "unknown", failure
	case failure != "":
		out.State, out.Reason = "unavailable", failure
	case at.IsZero(), now.Before(at):
		out.State, out.Reason = "unknown", "STATE_CACHE_RESYNCING"
	case now.Sub(at) > ageLimit:
		out.State, out.Reason = "stale", "STATE_CACHE_EXPIRED"
	case partial:
		out.State, out.Reason = "degraded", "STATE_CACHE_PARTIAL"
	default:
		out.State, out.Reason = "ready", ""
	}
}

func (s *Service) deviceHealth(out *HealthObservation, now time.Time, scopeSize int) {
	if s.devices.observer == nil {
		out.State, out.Reason = "unavailable", "LINUX_PROVIDER_UNAVAILABLE"
		return
	}
	fresh, attempted := 0, false
	var newest time.Time
	for _, device := range s.devices.entries {
		attempted = attempted || !device.cachedAt.IsZero()
		at := device.sample.ObservedAt
		if device.sample.Availability != "known" || at.IsZero() || now.Before(at) {
			continue
		}
		if now.Sub(at) <= FreshFor {
			fresh++
		}
		if at.After(newest) {
			newest = at
		}
	}
	var failure string
	if newest.IsZero() && attempted {
		failure = "LINUX_DEVICE_OBSERVATIONS_UNAVAILABLE"
	}
	cacheObservation(out, now, newest, FreshFor, failure, fresh < scopeSize)
}

func (s *Service) statisticHealth(out *HealthObservation, now time.Time, current *view) {
	if !s.statisticsMu.TryLock() {
		out.State, out.Reason = "unknown", "STATE_CACHE_BUSY"
		return
	}
	defer s.statisticsMu.Unlock()
	cache := s.statistics
	if cache.observer == nil {
		out.State, out.Reason = "disabled", "STATISTICS_NOT_CONFIGURED"
		return
	}
	cacheObservation(out, now, cache.observedAt, statisticsFreshFor, cache.failure, false)
	if current == nil || out.State != "ready" {
		return
	}
	for _, entry := range cache.entries {
		if entry.sourceEpoch != s.statisticsEpoch || entry.generation != current.decision.Generation ||
			entry.schema != current.observation.Schema.Digest {
			out.State, out.Reason = "stale", "STATISTICS_SOURCE_CHANGED"
		}
		return // one successful round publishes entries with a common source
	}
	out.State, out.Reason = "unknown", "STATISTICS_NO_PROVEN_SCOPE"
}

func fdbHealth(out *HealthObservation, now time.Time, cache fdbCache) {
	if cache.observer == nil {
		out.State, out.Reason = "disabled", "FDB_NOT_CONFIGURED"
		return
	}
	var newest time.Time
	fresh, partial := 0, false
	for _, sample := range cache.samples {
		if sample.failure != "" || sample.observedAt.IsZero() || now.Before(sample.observedAt) {
			continue
		}
		if now.Sub(sample.observedAt) <= fdbFreshFor {
			fresh++
			partial = partial || int(sample.total) > len(sample.entries)
		}
		if sample.observedAt.After(newest) {
			newest = sample.observedAt
		}
	}
	var failure string
	if newest.IsZero() && len(cache.samples) > 0 {
		failure = "FDB_OBSERVATION_UNAVAILABLE"
	}
	cacheObservation(out, now, newest, fdbFreshFor, failure, partial || fresh < len(cache.grants))
}
