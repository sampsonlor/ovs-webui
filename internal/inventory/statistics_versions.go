package inventory

import (
	"slices"
	"time"
)

// StatisticVersions serves only opaque versions of requested active Interfaces
// from the published cache. It performs no sampling or storage I/O.
func (s *Service) StatisticVersions(ids []string) map[string]string {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	s.mu.RLock()
	v, failure, epoch := s.current, s.failure, s.statisticsEpoch
	s.mu.RUnlock()
	if v == nil || v.decision.State != "confirmed" {
		return nil
	}
	targets := make([]Binding, 0, len(wanted))
	for _, binding := range v.decision.Bindings {
		if wanted[binding.ManagementID] && binding.Table == "Interface" && binding.State == "active" {
			targets = append(targets, binding)
		}
	}
	associations := selectedStatisticAssociations(v, targets)
	now := s.now()
	out := make(map[string]string, len(targets))
	s.statisticsMu.Lock()
	defer s.statisticsMu.Unlock()
	cache := &s.statistics
	if cache.observer == nil {
		return nil
	}
	for _, binding := range targets {
		entry := cache.entries[binding.ManagementID]
		freshness := "unknown"
		if statisticEntryMatches(v, binding, entry, associations[binding.UUID]) && len(entry.samples) != 0 {
			latest := entry.samples[len(entry.samples)-1]
			if !latest.at.Before(now.Add(-StatisticsRetention)) {
				freshness = "stale"
				if latest.valid != 0 && statisticSampleFresh(v, failure, epoch, entry, cache.failure, latest.at, now) {
					freshness = "fresh"
				}
			}
		}
		out[binding.ManagementID] = statisticResourceVersion(cache.revision, binding, entry, freshness)
	}
	return out
}

// Compute the same digests as the individual Statistics detail without doing
// a parent-table scan per subscribed Interface.
func selectedStatisticAssociations(v *view, targets []Binding) map[string]string {
	if len(targets) == 0 {
		return nil
	}
	ports := make(map[string][]string, len(targets))
	for _, binding := range targets {
		ports[binding.UUID] = []string{}
	}
	bridges := make(map[string][]string)
	for uuid, row := range v.observation.Rows["Port"] {
		for _, iface := range refs(row.Values["interfaces"]) {
			matches, selected := ports[iface]
			if selected && (len(matches) == 0 || matches[len(matches)-1] != uuid) {
				ports[iface] = append(matches, uuid)
				bridges[uuid] = nil
			}
		}
	}
	for uuid, row := range v.observation.Rows["Bridge"] {
		for _, port := range refs(row.Values["ports"]) {
			matches, selected := bridges[port]
			if selected && (len(matches) == 0 || matches[len(matches)-1] != uuid) {
				bridges[port] = append(matches, uuid)
			}
		}
	}
	out := make(map[string]string, len(ports))
	for iface, portIDs := range ports {
		bridgeIDs := []string{}
		for _, port := range portIDs {
			bridgeIDs = append(bridgeIDs, bridges[port]...)
		}
		slices.Sort(portIDs)
		slices.Sort(bridgeIDs)
		out[iface] = statisticAssociationDigest(v, iface, portIDs, bridgeIDs)
	}
	return out
}

func statisticEntryMatches(v *view, binding Binding, entry statisticEntry, association string) bool {
	return entry.binding == binding && entry.generation == v.decision.Generation &&
		entry.schema == v.observation.Schema.Digest && entry.association == association
}

func statisticSampleFresh(v *view, failure string, epoch uint64, entry statisticEntry, reason string, at, now time.Time) bool {
	return statisticViewFresh(v, failure, now) && entry.sourceEpoch == epoch && reason == "" &&
		now.Sub(at) >= 0 && now.Sub(at) <= statisticsFreshFor
}

func statisticResourceVersion(cacheRevision string, binding Binding, entry statisticEntry, freshness string) string {
	return Digest([]any{cacheRevision, binding, entry.association, freshness})
}
