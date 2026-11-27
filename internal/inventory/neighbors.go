package inventory

import (
	"context"
	"errors"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	MaxNeighbors       = 2048
	neighborInterval   = 5 * time.Second
	neighborReadBudget = 2 * time.Second
	neighborFreshFor   = 3 * neighborInterval
)

type Neighbor struct {
	Interface DeviceRequest
	IP        string
	MAC       *string
	State     string
}

type NeighborObservation struct {
	ObservedAt time.Time
	Neighbors  []Neighbor
	Covered    []DeviceRequest
	Truncated  bool
}

// NeighborObserver is a single bounded read of Linux's IP neighbor cache.
// Covered declares which requested Linux bindings were proved for the read.
type NeighborObserver interface {
	ObserveNeighbors(context.Context, []DeviceRequest) (NeighborObservation, error)
}

type neighborParent struct {
	id, name string
}

type neighborBinding struct {
	deviceProof
	port, bridge neighborParent
}

type neighborScope struct {
	generation, root, peer, schema string
	bindings                       []neighborBinding
	ambiguous                      []neighborBinding
}

func (n neighborScope) equal(other neighborScope) bool {
	return n.generation == other.generation && n.root == other.root && n.peer == other.peer && n.schema == other.schema &&
		slices.Equal(n.bindings, other.bindings) && slices.Equal(n.ambiguous, other.ambiguous)
}

// Build the association index from one immutable, confirmed OVS view. Keep
// ambiguous Linux indices out of the provider allowlist even if one name fits.
func indexNeighbors(v *view) neighborScope {
	if v.decision.State != "confirmed" {
		return neighborScope{}
	}
	o := v.observation
	scope := neighborScope{generation: v.decision.Generation, root: o.Evidence.Root, peer: o.Evidence.Peer, schema: o.Schema.Digest}
	ports := neighborParents(v, "Port", "interfaces")
	bridges := neighborParents(v, "Bridge", "ports")
	candidates := make([]neighborBinding, 0, len(o.Rows["Interface"]))
	indices := make(map[int]int)
	names := make(map[string]int)
	for uuid := range o.Rows["Interface"] {
		proof, valid := deviceBinding(v, uuid)
		if !valid || !neighborDeviceName(proof.request.Name) {
			continue
		}
		port := ports[uuid]
		bridge := bridges[port]
		binding := neighborBinding{deviceProof: proof, port: neighborParentFor(v, "Port", port), bridge: neighborParentFor(v, "Bridge", bridge)}
		candidates = append(candidates, binding)
		indices[proof.request.IfIndex]++
		names[proof.request.Name]++
	}
	slices.SortFunc(candidates, func(a, b neighborBinding) int { return strings.Compare(a.managementID, b.managementID) })
	for _, binding := range candidates {
		if indices[binding.request.IfIndex] == 1 && names[binding.request.Name] == 1 {
			scope.bindings = append(scope.bindings, binding)
		} else {
			scope.ambiguous = append(scope.ambiguous, binding)
		}
	}
	return scope
}

func neighborDeviceName(name string) bool {
	return len(name) > 0 && len(name) <= 15 && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00:\t\r\n ")
}

func neighborParents(v *view, table, column string) map[string]string {
	parents := make(map[string]string)
	for uuid, row := range v.observation.Rows[table] {
		for _, child := range refs(row.Values[column]) {
			previous, exists := parents[child]
			if !exists {
				parents[child] = uuid
			} else if previous != uuid {
				parents[child] = ""
			}
		}
	}
	return parents
}

func neighborParentFor(v *view, table, uuid string) neighborParent {
	b := v.decision.Bindings[Key(table, uuid)]
	if b.State != "active" || b.Table != table || b.UUID != uuid {
		return neighborParent{}
	}
	name := textValue(v.observation.Rows[table][uuid].Values["name"])
	if len(name) > 256 {
		name = ""
	}
	return neighborParent{b.ManagementID, name}
}

type neighborEntry struct {
	ID, IP, MAC, State, Family string
	Binding                    neighborBinding
}

type neighborCache struct {
	observer                          NeighborObserver
	collecting                        bool
	epoch                             uint64
	eventRevision, collectingRevision uint64
	id, failure                       string
	sequence                          uint64
	observedAt                        time.Time
	entries                           []neighborEntry
	covered                           []DeviceRequest
	truncated                         bool
	// Readers hold snapshots outside Service.mu. Replace this map on writes,
	// as with the published rows and coverage.
	pending map[int]uint64
}

// Service.mu protects publication and snapshots only. A failed or interrupted
// observation clears the old rows. In-progress work is fenced by the epoch.
func (c *neighborCache) invalidate(reason string) {
	c.epoch++
	c.id, c.failure = "", reason
	c.observedAt = time.Time{}
	c.entries, c.covered = nil, nil
	c.truncated = false
	c.pending = nil
}

func (s *Service) SetNeighborObserver(observer NeighborObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.neighbors.observer = observer
	s.neighbors.invalidate("NEIGHBORS_RESYNCING")
}

// HandleNeighborEvents removes evidence invalidated by kernel notifications.
// The existing collector remains the only source of rows and coverage.
func (s *Service) HandleNeighborEvents(events InterfaceEvents) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.neighbors
	if c.observer == nil {
		return
	}
	if events.Lost {
		c.invalidate("NEIGHBORS_RESYNCING")
		return
	}
	v := s.current
	if len(events.Indices) == 0 || v == nil || v.decision.State != "confirmed" || len(v.neighborScope.bindings) > MaxRows {
		return
	}
	indices := make(map[int]bool, len(events.Indices))
	for _, index := range events.Indices {
		indices[index] = true
	}
	var pending map[int]uint64
	for _, binding := range v.neighborScope.bindings {
		index := binding.request.IfIndex
		if !indices[index] {
			continue
		}
		if rev, exists := c.pending[index]; exists && (!c.collecting || rev > c.collectingRevision) {
			continue
		}
		if pending == nil {
			pending = maps.Clone(c.pending)
			if pending == nil {
				pending = make(map[int]uint64)
			}
			c.eventRevision++
		}
		pending[index] = c.eventRevision
	}
	if pending == nil {
		return
	}
	c.pending = pending
	if slices.ContainsFunc(c.covered, func(req DeviceRequest) bool { return pending[req.IfIndex] != 0 }) {
		c.entries, c.covered = omitPendingNeighbors(slices.Clone(c.entries), slices.Clone(c.covered), pending)
		c.id = repository.NewID()
		c.sequence++
	}
}

func omitPendingNeighbors(entries []neighborEntry, covered []DeviceRequest, pending map[int]uint64) ([]neighborEntry, []DeviceRequest) {
	return slices.DeleteFunc(entries, func(row neighborEntry) bool { return pending[row.Binding.request.IfIndex] != 0 }),
		slices.DeleteFunc(covered, func(req DeviceRequest) bool { return pending[req.IfIndex] != 0 })
}

func (s *Service) RunNeighbors(ctx context.Context) {
	ticker := time.NewTicker(neighborInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.SampleNeighbors(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) SampleNeighbors(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	v, observer, epoch := s.current, s.neighbors.observer, s.neighbors.epoch
	if observer == nil || s.neighbors.collecting || !deviceViewFresh(v, s.failure, s.now()) || len(v.neighborScope.bindings) == 0 {
		s.mu.Unlock()
		return
	}
	s.neighbors.collecting = true
	revision := s.neighbors.eventRevision
	s.neighbors.collectingRevision = revision
	s.mu.Unlock()

	requests := make([]DeviceRequest, 0, len(v.neighborScope.bindings))
	for _, binding := range v.neighborScope.bindings {
		requests = append(requests, binding.request)
	}
	readCtx, cancel := context.WithTimeout(ctx, neighborReadBudget)
	observation, err := observer.ObserveNeighbors(readCtx, requests)
	if readCtx.Err() != nil {
		err = apitypes.Fail(503, "NEIGHBORS_DUMP_TIMEOUT")
	}
	cancel()
	now := s.now()
	var entries []neighborEntry
	var covered []DeviceRequest
	if err == nil {
		entries, covered, err = qualifyNeighbors(v.neighborScope, observation, now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.neighbors
	c.collecting = false
	if ctx.Err() != nil || c.epoch != epoch || !deviceViewFresh(s.current, s.failure, s.now()) {
		return
	}
	if err != nil {
		c.invalidate(neighborFailure(err))
		return
	}
	var pending map[int]uint64
	for index, changed := range c.pending {
		if changed > revision {
			if pending == nil {
				pending = make(map[int]uint64)
			}
			pending[index] = changed
		}
	}
	if len(pending) != 0 {
		entries, covered = omitPendingNeighbors(entries, covered, pending)
	}
	c.pending = pending
	if c.id == "" || !slices.Equal(c.entries, entries) || !slices.Equal(c.covered, covered) || c.truncated != observation.Truncated {
		c.id = repository.NewID()
		c.sequence++
	}
	c.entries, c.covered = entries, covered
	c.observedAt, c.truncated, c.failure = observation.ObservedAt, observation.Truncated, ""
}

func neighborFailure(err error) string {
	var problem *apitypes.Problem
	if errors.As(err, &problem) {
		switch problem.Code {
		case "NEIGHBORS_DUMP_TIMEOUT", "NEIGHBORS_DUMP_UNAVAILABLE", "NEIGHBORS_DUMP_UNPROVEN", "NEIGHBORS_DUMP_BUDGET", "NEIGHBORS_INVALID_BINDINGS":
			return problem.Code
		}
	}
	return "NEIGHBORS_DUMP_UNAVAILABLE"
}

func qualifyNeighbors(scope neighborScope, observation NeighborObservation, now time.Time) ([]neighborEntry, []DeviceRequest, error) {
	reject := apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
	age := now.Sub(observation.ObservedAt)
	if observation.ObservedAt.IsZero() || age < 0 || age > neighborFreshFor || len(observation.Neighbors) > MaxNeighbors || len(observation.Covered) > len(scope.bindings) {
		return nil, nil, reject
	}
	approved := make(map[DeviceRequest]neighborBinding, len(scope.bindings))
	for _, binding := range scope.bindings {
		approved[binding.request] = binding
	}
	covered := slices.Clone(observation.Covered)
	slices.SortFunc(covered, func(a, b DeviceRequest) int { return a.IfIndex - b.IfIndex })
	observed := make(map[DeviceRequest]bool, len(covered))
	for i, request := range covered {
		if _, ok := approved[request]; !ok || i > 0 && covered[i-1] == request {
			return nil, nil, reject
		}
		observed[request] = true
	}
	entries := make([]neighborEntry, 0, len(observation.Neighbors))
	seen := make(map[string]bool)
	for _, neighbor := range observation.Neighbors {
		binding, ok := approved[neighbor.Interface]
		ip, err := netip.ParseAddr(neighbor.IP)
		if !ok || !observed[neighbor.Interface] || err != nil || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, nil, reject
		}
		family := "ipv6"
		if ip.Is4() {
			family = "ipv4"
		}
		mac, state := "", neighbor.State
		switch state {
		case "reachable", "stale", "delay", "probe", "noarp", "permanent":
			if neighbor.MAC != nil && len(*neighbor.MAC) == 17 {
				value, parseErr := net.ParseMAC(*neighbor.MAC)
				if parseErr == nil && len(value) == 6 && value[0]&1 == 0 && value.String() != "00:00:00:00:00:00" {
					mac = value.String()
				}
			}
		case "failed", "incomplete":
		default:
			state = "unknown"
		}
		id := schemaID("linux-neighbor-v1/"+binding.managementID+"/"+binding.ovsUUID, ip.String())
		if seen[id] {
			return nil, nil, reject
		}
		seen[id] = true
		entries = append(entries, neighborEntry{ID: id, IP: ip.String(), MAC: mac, State: state, Family: family, Binding: binding})
	}
	slices.SortFunc(entries, func(a, b neighborEntry) int {
		if cmp := strings.Compare(a.Binding.managementID, b.Binding.managementID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.IP, b.IP)
	})
	return entries, covered, nil
}
