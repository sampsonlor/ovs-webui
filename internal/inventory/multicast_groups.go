package inventory

import (
	"context"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/multicast"
)

// Requests contain only a confirmed private provider association. Browser
// requests never supply native command arguments, paths or peer identities.
type MulticastGroupRequest struct {
	Bridge   Binding
	Name     string
	Evidence Evidence
}
type MulticastGroupSample struct {
	ObservedAt time.Time
	Groups     []multicast.Group
}
type MulticastGroupObserver interface {
	ObserveMulticast(context.Context, MulticastGroupRequest) (MulticastGroupSample, error)
}

func (s *Service) SetMulticastGroupObserver(p MulticastGroupObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multicastGroupObserver = p
}

// The complete persistent configuration and ephemeral ofport association are
// checked again after the separate native observation. A changed association
// invalidates the sample; it never turns into an empty membership table.
func multicastGroupAssociation(v *view) string {
	images := map[string]any{}
	for _, table := range []string{"Open_vSwitch", "Bridge", "Port", "Interface"} {
		for id, row := range v.observation.Rows[table] {
			images[Key(table, id)] = []any{row.Configuration, row.Values["ofport"], v.decision.Bindings[Key(table, id)]}
		}
	}
	return Digest([]any{v.decision.Generation, v.observation.Evidence.Endpoint, v.observation.Evidence.Root, v.observation.Evidence.Schema, images})
}

func (s *Service) readMulticastGroups(ctx context.Context, v *view, b Binding, fresh string, config bool) map[string]any {
	reason := "MULTICAST_GROUP_SOURCE_UNAVAILABLE"
	result := map[string]any{"availability": "unavailable", "reason": reason, "items": nil, "source": map[string]any{"provider_id": "ovs-vswitchd", "authority": "native-multicast-mdb", "observed_at": nil, "freshness": "unavailable", "confidence": "unknown"}}
	if !config {
		result["availability"] = "withheld"
		result["reason"] = "CONFIGURATION_WITHHELD"
		return result
	}
	if fresh != "fresh" {
		result["reason"] = "MULTICAST_ASSOCIATION_STALE"
		return result
	}
	row := v.observation.Rows["Bridge"][b.UUID]
	enabled, known := row.Configuration["mcast_snooping_enable"].(bool)
	if !known {
		result["reason"] = "MULTICAST_CONFIGURATION_UNKNOWN"
		return result
	}
	if !enabled {
		result["reason"] = "MULTICAST_SNOOPING_DISABLED"
		return result
	}
	s.mu.RLock()
	observer := s.multicastGroupObserver
	s.mu.RUnlock()
	if observer == nil {
		return result
	}
	name, valid := row.Values["name"].(string)
	if !valid {
		result["reason"] = "MULTICAST_ASSOCIATION_UNPROVEN"
		return result
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	association := multicastGroupAssociation(v)
	sample, err := observer.ObserveMulticast(bounded, MulticastGroupRequest{Bridge: b, Name: name, Evidence: v.observation.Evidence})
	if err != nil || bounded.Err() != nil || sample.Groups == nil || len(sample.Groups) > multicast.MaxGroups {
		result["reason"] = "MULTICAST_GROUP_OBSERVATION_UNPROVEN"
		return result
	}
	s.mu.RLock()
	current, failure := s.current, s.failure
	s.mu.RUnlock()
	age := s.now().Sub(sample.ObservedAt)
	if current == nil || failure != "" || current.decision.State != "confirmed" || multicastGroupAssociation(current) != association || sample.ObservedAt.IsZero() || age < 0 || age > FreshFor {
		result["reason"] = "MULTICAST_ASSOCIATION_CHANGED"
		return result
	}
	currentAge := s.now().Sub(current.observation.Evidence.ObservedAt)
	if currentAge < 0 || currentAge > FreshFor {
		result["reason"] = "MULTICAST_ASSOCIATION_STALE"
		return result
	}
	// Resolve native ofport only through this confirmed Bridge -> Port ->
	// Interface graph. Ambiguous, missing or reassigned numbers reject the
	// complete observation rather than attaching groups to a same-name Port.
	ports := map[uint32]Binding{}
	for _, id := range refs(row.Values["ports"]) {
		p := v.observation.Rows["Port"][id]
		binding := v.decision.Bindings[Key("Port", id)]
		if binding.State != "active" {
			result["reason"] = "MULTICAST_PORT_ASSOCIATION_UNPROVEN"
			return result
		}
		for _, iid := range refs(p.Values["interfaces"]) {
			n, err := strconv.ParseUint(textValue(v.observation.Rows["Interface"][iid].Values["ofport"]), 10, 16)
			if err != nil || n == 0 || n >= 65280 && n != 65534 {
				continue
			}
			if _, exists := ports[uint32(n)]; exists {
				result["reason"] = "MULTICAST_PORT_ASSOCIATION_UNPROVEN"
				return result
			}
			ports[uint32(n)] = binding
		}
	}
	items := []any{}
	for _, group := range sample.Groups {
		port, ok := ports[group.Port]
		if !ok {
			result["reason"] = "MULTICAST_PORT_ASSOCIATION_UNPROVEN"
			return result
		}
		items = append(items, map[string]any{"ofport": group.Port, "vlan": group.VLAN, "protocol": group.Protocol, "address": group.Address, "router": group.Router, "age_seconds": group.Age, "port_ref": map[string]any{"kind": "port", "id": port.ManagementID}, "port_name": v.observation.Rows["Port"][port.UUID].Values["name"]})
	}
	return map[string]any{"availability": "known", "reason": "", "items": items, "source": map[string]any{"provider_id": "ovs-vswitchd", "authority": "native-multicast-mdb", "observed_at": sample.ObservedAt.UTC(), "freshness": "fresh", "confidence": "proven"}}
}
