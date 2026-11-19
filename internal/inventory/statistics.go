package inventory

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	StatisticsInterval  = 5 * time.Second
	StatisticsRetention = 5 * time.Minute
	statisticsSamples   = 60
	statisticsFreshFor  = 3 * StatisticsInterval
	// Each entry retains at most 60 fixed-size eight-counter samples. Together
	// with the 2048-row native inventory bound this stays below 64 MiB.
	statisticsObjects = MaxRows
)

var statisticCounters = []string{"rx_bytes", "tx_bytes", "rx_packets", "tx_packets", "rx_errors", "tx_errors", "rx_dropped", "tx_dropped"}
var statisticRates = statisticCounters[:4]

type StatRequest struct {
	Root, Schema string
	Interfaces   []string
}

type StatObservation struct {
	ObservedAt   time.Time
	Root, Schema string
	Counters     map[string]map[string]*string
	Coalesced    bool
}

type StatisticsObserver interface {
	ObserveStatistics(context.Context, StatRequest) (StatObservation, error)
}

type statisticSample struct {
	SampledAt     time.Time           `json:"sampled_at"`
	Counters      map[string]*string  `json:"counters"`
	Rates         map[string]*float64 `json:"rates"`
	Reset         bool                `json:"reset"`
	Discontinuity *string             `json:"discontinuity"`
	Coalesced     bool                `json:"coalesced"`
}

type statisticPoint struct {
	at               time.Time
	values           [8]uint64
	rates            [4]float64
	valid, rateValid uint8
	reset, coalesced bool
	discontinuity    string
}

type statisticEntry struct {
	binding            Binding
	association        string
	generation, schema string
	sourceEpoch        uint64
	clock              time.Time
	samples            []statisticPoint
}

type statisticsCache struct {
	observer    StatisticsObserver
	collecting  bool
	lastAttempt time.Time
	failure     string
	entries     map[string]statisticEntry
	revision    string
	observedAt  time.Time
}

func (s *Service) ConfigureStatistics(observer StatisticsObserver) error {
	if observer == nil {
		return apitypes.Fail(422, "INVALID_STATISTICS_OBSERVER")
	}
	s.statisticsMu.Lock()
	defer s.statisticsMu.Unlock()
	if s.statistics.collecting {
		return apitypes.Fail(409, "STATISTICS_COLLECTION_BUSY")
	}
	s.statistics = statisticsCache{observer: observer, failure: "STATISTICS_RESYNCING", entries: map[string]statisticEntry{}, revision: Digest("statistics-initial")}
	return nil
}

func (s *Service) RunStatistics(ctx context.Context) {
	ticker := time.NewTicker(StatisticsInterval)
	defer ticker.Stop()
	for {
		_ = s.SampleStatistics(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func statisticViewFresh(v *view, failure string, now time.Time) bool {
	return v != nil && failure == "" && v.decision.State == "confirmed" &&
		now.Sub(v.observation.Evidence.ObservedAt) >= 0 && now.Sub(v.observation.Evidence.ObservedAt) <= FreshFor
}

// SampleStatistics is called by one manager-owned scheduler. HTTP readers only
// copy published samples and cannot create collectors or extend retention.
func (s *Service) SampleStatistics(ctx context.Context) error {
	now := s.now()
	s.statisticsMu.Lock()
	cache := &s.statistics
	if cache.collecting || !cache.lastAttempt.IsZero() && now.Sub(cache.lastAttempt) >= 0 && now.Sub(cache.lastAttempt) < StatisticsInterval {
		s.statisticsMu.Unlock()
		return apitypes.Fail(429, "STATISTICS_SAMPLE_PENDING")
	}
	observer := cache.observer
	if observer == nil {
		s.statisticsMu.Unlock()
		return apitypes.Fail(503, "STATISTICS_RESYNCING")
	}
	cache.collecting, cache.lastAttempt = true, now
	s.statisticsMu.Unlock()

	s.mu.RLock()
	initial, failure, initialEpoch := s.current, s.failure, s.statisticsEpoch
	s.mu.RUnlock()
	var observation StatObservation
	var err error
	if !statisticViewFresh(initial, failure, now) {
		err = apitypes.Fail(503, "STATISTICS_SOURCE_CHANGED")
	} else {
		request := StatRequest{Root: initial.observation.Evidence.Root, Schema: initial.observation.Schema.Digest}
		for uuid := range initial.observation.Rows["Interface"] {
			request.Interfaces = append(request.Interfaces, uuid)
		}
		slices.Sort(request.Interfaces)
		if len(request.Interfaces) > statisticsObjects {
			err = apitypes.Fail(429, "STATISTICS_OBJECT_BUDGET")
		} else {
			collection, cancel := context.WithTimeout(ctx, 2*time.Second)
			observation, err = observer.ObserveStatistics(collection, request)
			cancel()
		}
	}
	s.mu.RLock()
	current, currentFailure, currentEpoch := s.current, s.failure, s.statisticsEpoch
	s.mu.RUnlock()
	completed := s.now()
	s.statisticsMu.Lock()
	defer s.statisticsMu.Unlock()
	cache = &s.statistics
	cache.collecting = false
	if err == nil && (!statisticViewFresh(current, currentFailure, completed) || initialEpoch != currentEpoch || initial.decision.Generation != current.decision.Generation ||
		observation.Root != current.observation.Evidence.Root || observation.Schema != current.observation.Schema.Digest ||
		observation.ObservedAt.IsZero() || observation.ObservedAt.After(completed.Add(time.Second))) {
		err = apitypes.Fail(503, "STATISTICS_SOURCE_CHANGED")
	}
	if err != nil {
		cache.failure = "STATISTICS_UNAVAILABLE"
		switch err.Error() {
		case "STATISTICS_SCHEMA_UNSUPPORTED", "STATISTICS_SOURCE_CHANGED", "STATISTICS_OBJECT_BUDGET":
			cache.failure = err.Error()
		}
		cache.revision = Digest([]any{cache.revision, cache.failure})
		return err
	}
	previousFailure := cache.failure
	entries := make(map[string]statisticEntry)
	for _, binding := range current.decision.Bindings {
		if binding.Table != "Interface" || binding.State != "active" {
			continue
		}
		original := initial.decision.Bindings[Key("Interface", binding.UUID)]
		association := statisticAssociation(current, binding)
		if binding != original || association != statisticAssociation(initial, binding) {
			continue
		}
		values, present := observation.Counters[binding.UUID]
		if !present {
			continue
		}
		entry := cache.entries[binding.ManagementID]
		reason := ""
		if len(entry.samples) == 0 {
			reason = "first-sample"
		} else if entry.binding != binding || entry.generation != current.decision.Generation || entry.schema != observation.Schema {
			entry.samples, reason = nil, "source-changed"
		} else if entry.association != association {
			entry.samples, reason = nil, "association-changed"
		} else if entry.sourceEpoch != currentEpoch {
			reason = "source-changed"
		} else if previousFailure != "" || completed.Sub(entry.clock) >= 2*StatisticsInterval || observation.Coalesced {
			reason = "collection-gap"
		} else if completed.Sub(entry.clock) <= 0 || !observation.ObservedAt.After(entry.samples[len(entry.samples)-1].at) {
			reason = "clock-discontinuity"
		}
		sample := statisticPoint{at: observation.ObservedAt, coalesced: observation.Coalesced}
		if len(entry.samples) > 0 && completed.Sub(entry.clock) >= 2*StatisticsInterval {
			sample.coalesced = true
		}
		for index, key := range statisticCounters {
			if value := values[key]; value != nil {
				n, err := strconv.ParseUint(*value, 10, 63)
				if err == nil && strconv.FormatUint(n, 10) == *value {
					sample.values[index] = n
					sample.valid |= 1 << index
				}
			}
		}
		if reason == "" {
			before := entry.samples[len(entry.samples)-1]
			seconds := observation.ObservedAt.Sub(before.at).Seconds()
			for index := range statisticCounters {
				if before.valid&sample.valid&(1<<index) == 0 {
					continue
				}
				old, value := before.values[index], sample.values[index]
				if value < old {
					sample.reset, reason = true, "counter-reset"
					break
				}
				if index < len(statisticRates) {
					sample.rates[index] = float64(value-old) / seconds
					sample.rateValid |= 1 << index
				}
			}
		}
		if reason != "" {
			sample.discontinuity, sample.rateValid = reason, 0
		}
		entry.samples = append(entry.samples, sample)
		cutoff := completed.Add(-StatisticsRetention)
		entry.samples = slices.DeleteFunc(entry.samples, func(sample statisticPoint) bool { return sample.at.Before(cutoff) })
		if len(entry.samples) > statisticsSamples {
			entry.samples = slices.Clone(entry.samples[len(entry.samples)-statisticsSamples:])
		}
		entry.binding, entry.association = binding, association
		entry.generation, entry.schema, entry.clock = current.decision.Generation, observation.Schema, completed
		entry.sourceEpoch = currentEpoch
		entries[binding.ManagementID] = entry
	}
	cache.entries, cache.failure = entries, ""
	cache.observedAt = observation.ObservedAt
	cache.revision = Digest([]any{cache.revision, observation.ObservedAt, current.decision.Generation})
	return nil
}

func statisticPointValue(point statisticPoint) statisticSample {
	out := make(map[string]*string, len(statisticCounters))
	for index, key := range statisticCounters {
		out[key] = nil
		if point.valid&(1<<index) != 0 {
			text := strconv.FormatUint(point.values[index], 10)
			out[key] = &text
		}
	}
	rates := emptyStatisticRates()
	for index, key := range statisticRates {
		if point.rateValid&(1<<index) != 0 {
			value := point.rates[index]
			rates[key] = &value
		}
	}
	value := statisticSample{SampledAt: point.at.UTC(), Counters: out, Rates: rates, Reset: point.reset, Coalesced: point.coalesced}
	if point.discontinuity != "" {
		text := point.discontinuity
		value.Discontinuity = &text
	}
	return value
}

func emptyStatisticRates() map[string]*float64 {
	out := make(map[string]*float64, len(statisticRates))
	for _, key := range statisticRates {
		out[key] = nil
	}
	return out
}

func statisticAssociation(v *view, binding Binding) string {
	if v == nil {
		return ""
	}
	ports := []string{}
	bridges := []string{}
	for uuid, row := range v.observation.Rows["Port"] {
		if slices.Contains(refs(row.Values["interfaces"]), binding.UUID) {
			ports = append(ports, uuid)
			for bridge, row := range v.observation.Rows["Bridge"] {
				if slices.Contains(refs(row.Values["ports"]), uuid) {
					bridges = append(bridges, bridge)
				}
			}
		}
	}
	slices.Sort(ports)
	slices.Sort(bridges)
	row := v.observation.Rows["Interface"][binding.UUID]
	return Digest([]any{ports, bridges, row.Values["ifindex"], row.Values["ofport"], row.Values["name"], row.Values["type"], v.observation.Evidence.Peer})
}
