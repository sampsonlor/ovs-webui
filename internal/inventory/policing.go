package inventory

import (
	"context"
	"strconv"
	"time"
)

// Rates use canonical decimal strings: kernel uint64 values must survive JSON
// clients without the precision loss of JavaScript numbers. They are bytes/s
// and packets/s, not the native OVS configuration units.
type PolicingAction struct {
	FilterKind       string  `json:"filter_kind"`
	Priority         uint32  `json:"priority"`
	Handle           string  `json:"handle"`
	Index            uint32  `json:"index"`
	BytesPerSecond   *string `json:"bytes_per_second"`
	PacketsPerSecond *string `json:"packets_per_second"`
	ExceedAction     string  `json:"exceed_action"`
	ConformAction    string  `json:"conform_action"`
}

type PolicingSample struct {
	Availability string
	Reason       string
	ObservedAt   time.Time
	IfIndex      int
	FilterCount  int
	Actions      []PolicingAction
}

type PolicingObserver interface {
	ObservePolicing(context.Context, DeviceRequest) PolicingSample
}

func (s *Service) SetPolicingObserver(p PolicingObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policingObserver = p
}

func policingUnavailable(reason string) PolicingSample {
	return PolicingSample{Availability: "unavailable", Reason: reason, Actions: []PolicingAction{}}
}

func validPolicingSample(sample PolicingSample) bool {
	if sample.FilterCount < 0 || sample.FilterCount > 64 || len(sample.Actions) > 16 || len(sample.Actions) > sample.FilterCount*32 {
		return false
	}
	for _, a := range sample.Actions {
		if a.FilterKind != "basic" && a.FilterKind != "matchall" && a.FilterKind != "u32" || len(a.Handle) > 10 || len(a.ExceedAction) > 32 || len(a.ConformAction) > 32 {
			return false
		}
		for _, rate := range []*string{a.BytesPerSecond, a.PacketsPerSecond} {
			if rate != nil {
				n, err := strconv.ParseUint(*rate, 10, 64)
				if err != nil || strconv.FormatUint(n, 10) != *rate {
					return false
				}
			}
		}
	}
	return true
}

// The OVS association and kernel dump are separate observations. Recheck the
// immutable identity and native binding after reading; never publish old rules
// under a replacement Interface or use this diagnostic as dispatch evidence.
func (s *Service) readPolicing(ctx context.Context, v *view, b Binding, fresh string, allowedConfig bool) map[string]any {
	sample := policingUnavailable("LINUX_PROVIDER_UNAVAILABLE")
	req, eligible := deviceRequest(v.observation.Rows["Interface"][b.UUID])
	if !allowedConfig {
		sample.Availability, sample.Reason = "withheld", "CONFIGURATION_READ_REQUIRED"
	} else if fresh != "fresh" {
		sample = policingUnavailable("OVS_ASSOCIATION_STALE")
	} else if !eligible {
		sample = policingUnavailable("OVS_DEVICE_BINDING_UNPROVEN")
	} else {
		s.mu.RLock()
		p := s.policingObserver
		s.mu.RUnlock()
		if p != nil {
			sample = p.ObservePolicing(ctx, req)
			s.mu.RLock()
			current, failure := s.current, s.failure
			s.mu.RUnlock()
			matched := current != nil && failure == "" && current.decision.State == "confirmed" && current.decision.Generation == v.decision.Generation
			if matched {
				binding := current.decision.Bindings[Key("Interface", b.UUID)]
				row := current.observation.Rows["Interface"][b.UUID]
				next, valid := deviceRequest(row)
				age := s.now().Sub(current.observation.Evidence.ObservedAt)
				matched = valid && next == req && row.Values["type"] == v.observation.Rows["Interface"][b.UUID].Values["type"] && binding.ManagementID == b.ManagementID && age >= 0 && age <= FreshFor
			}
			if !matched || ctx.Err() != nil {
				sample = policingUnavailable("OVS_DEVICE_BINDING_CHANGED")
			}
		}
	}
	confidence, freshness := "unknown", "unavailable"
	var at, index, count any
	if sample.Availability == "known" || sample.Availability == "partial" {
		age := s.now().Sub(sample.ObservedAt)
		if sample.IfIndex != req.IfIndex || sample.ObservedAt.IsZero() || age < 0 || age > FreshFor || !validPolicingSample(sample) {
			sample = policingUnavailable("LINUX_POLICING_OBSERVATION_INVALID")
		} else {
			confidence, freshness, index, at, count = "proven", "fresh", sample.IfIndex, sample.ObservedAt.UTC(), sample.FilterCount
		}
	} else {
		// Failed reads cannot retain a previous/partial set of rule values.
		sample = policingUnavailable(sample.Reason)
		if !allowedConfig {
			sample.Availability = "withheld"
		}
	}
	if sample.Actions == nil {
		sample.Actions = []PolicingAction{}
	}
	return map[string]any{
		"availability": sample.Availability, "reason": sample.Reason, "ifindex": index, "filter_count": count, "actions": sample.Actions,
		"coverage": "linux-tc-ingress", "source": map[string]any{"provider_id": "linux", "authority": "linux-netlink-observation", "observed_at": at, "freshness": freshness, "confidence": confidence},
	}
}
