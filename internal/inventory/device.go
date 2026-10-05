package inventory

import (
	"context"
	"slices"
	"strconv"
	"time"
)

// DeviceRequest is constructed only from the current native Interface row.
// Public reads cannot supply host paths or a replacement device binding.
type DeviceRequest struct {
	Name    string
	IfIndex int
}
type DeviceField struct {
	Value        any    `json:"value"`
	Availability string `json:"availability"`
	Reason       string `json:"reason"`
}
type DeviceSample struct {
	Availability string
	Reason       string
	ObservedAt   time.Time
	IfIndex      int
	Fields       map[string]DeviceField
}
type DeviceObserver interface {
	Observe(context.Context, DeviceRequest) DeviceSample
}

func (s *Service) SetDeviceObserver(p DeviceObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deviceObserver = p
}

func deviceRequest(row Row) (DeviceRequest, bool) {
	typ, typeKnown := row.Values["type"].(string)
	name, nameKnown := row.Values["name"].(string)
	index, err := strconv.ParseInt(textValue(row.Values["ifindex"]), 10, 32)
	return DeviceRequest{Name: name, IfIndex: int(index)}, typeKnown && nameKnown && slices.Contains([]string{"", "system", "internal"}, typ) && err == nil && index > 0
}

func deviceUnavailable(reason string) DeviceSample {
	return DeviceSample{Availability: "unavailable", Reason: reason, Fields: map[string]DeviceField{}}
}

// The OVS snapshot and Linux sample are separate observations, not an atomic
// host snapshot. Check the native association again after the bounded read.
func (s *Service) readDevice(ctx context.Context, v *view, b Binding, fresh string) map[string]any {
	sample := deviceUnavailable("LINUX_PROVIDER_UNAVAILABLE")
	req, eligible := deviceRequest(v.observation.Rows["Interface"][b.UUID])
	if fresh != "fresh" {
		sample = deviceUnavailable("OVS_ASSOCIATION_STALE")
	} else if !eligible {
		sample = deviceUnavailable("OVS_DEVICE_BINDING_UNPROVEN")
	} else {
		s.mu.RLock()
		p := s.deviceObserver
		s.mu.RUnlock()
		if p != nil {
			sample = p.Observe(ctx, req)
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
				sample = deviceUnavailable("OVS_DEVICE_BINDING_CHANGED")
			}
		}
	}
	confidence, freshness := "unknown", "unavailable"
	var at, index any
	if !sample.ObservedAt.IsZero() {
		at = sample.ObservedAt.UTC()
	}
	if sample.Availability == "known" {
		age := s.now().Sub(sample.ObservedAt)
		if sample.IfIndex != req.IfIndex || sample.ObservedAt.IsZero() || age < 0 || age > FreshFor {
			sample = deviceUnavailable("LINUX_OBSERVATION_EXPIRED")
			at = nil
		} else {
			confidence, freshness, index = "proven", "fresh", sample.IfIndex
		}
	}
	return map[string]any{
		"availability": sample.Availability, "reason": sample.Reason, "ifindex": index, "fields": sample.Fields,
		"source": map[string]any{"provider_id": "linux", "authority": "linux-sysfs-observation", "observed_at": at, "freshness": freshness, "confidence": confidence},
	}
}
