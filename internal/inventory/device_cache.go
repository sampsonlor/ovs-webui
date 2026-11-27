package inventory

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DeviceObservationInterval = time.Second
	deviceObservationBudget   = 500 * time.Millisecond
	deviceObservationBatch    = 24
	deviceObservationWorkers  = 2
	deviceEventBatch          = deviceObservationBatch / 2
)

type deviceProof struct {
	request                  DeviceRequest
	managementID, ovsUUID    string
	generation, nativeType   string
	root, schema, sourcePeer string
}

type cachedDevice struct {
	proof        deviceProof
	sample       DeviceSample
	cachedAt     time.Time
	linkRevision uint64
	dirty        bool
}

type deviceTarget struct {
	deviceProof
	linkRevision uint64
	priority     bool
}

// Service.mu guards the cache and the OVS view used for its bindings. Only the
// manager's observation worker calls the provider, outside that lock.
type deviceCache struct {
	observer     DeviceObserver
	sampling     bool
	epoch        uint64
	after        string
	dirtyAfter   string
	entries      map[string]cachedDevice
	linkRevision uint64
}

func (c *deviceCache) invalidate() {
	c.epoch++
	c.after, c.dirtyAfter = "", ""
	c.entries = nil
}

func (c *deviceCache) prune(v *view) {
	for id, entry := range c.entries {
		if proof, ok := deviceBinding(v, entry.proof.ovsUUID); !ok || proof != entry.proof {
			delete(c.entries, id)
		}
	}
}

func deviceBinding(v *view, uuid string) (deviceProof, bool) {
	if v == nil || v.decision.State != "confirmed" {
		return deviceProof{}, false
	}
	b := v.decision.Bindings[Key("Interface", uuid)]
	row, found := v.observation.Rows["Interface"][uuid]
	req, eligible := deviceRequest(row)
	if !found || !eligible || b.Table != "Interface" || b.State != "active" || b.UUID != uuid || b.ManagementID == "" {
		return deviceProof{}, false
	}
	typ, _ := row.Values["type"].(string)
	return deviceProof{
		request: req, managementID: b.ManagementID, ovsUUID: uuid,
		generation: v.decision.Generation, nativeType: typ,
		root: v.observation.Evidence.Root, schema: v.observation.Schema.Digest, sourcePeer: v.observation.Evidence.Peer,
	}, true
}

func deviceViewFresh(v *view, failure string, now time.Time) bool {
	if v == nil || failure != "" || v.decision.State != "confirmed" {
		return false
	}
	age := now.Sub(v.observation.Evidence.ObservedAt)
	return age >= 0 && age <= FreshFor
}

// DeviceLinkEvents carries kernel invalidation hints or a discontinuity in their
// delivery. Neither is evidence of a device's current fields or identity.
type DeviceLinkEvents struct {
	Indices []int
	Lost    bool
}

func (s *Service) HandleDeviceLinkEvents(events DeviceLinkEvents) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.devices
	if c.observer == nil {
		return
	}
	if events.Lost {
		// Keep the periodic rotation, but clear all Linux samples immediately.
		// No in-flight job can recreate an entry. Any targeted hint that
		// recreates one has a new link revision.
		c.entries = nil
		c.dirtyAfter = ""
		return
	}
	v := s.current
	// The last confirmed view may briefly age out and then recover with the
	// same binding. Invalidate it now so an earlier Linux sample cannot revive.
	if len(events.Indices) == 0 || v == nil || v.decision.State != "confirmed" || len(v.observation.Rows["Interface"]) > MaxRows {
		return
	}
	indices := make(map[int]bool, len(events.Indices))
	for _, index := range events.Indices {
		indices[index] = true
	}
	var pending DeviceSample
	for uuid := range v.observation.Rows["Interface"] {
		proof, ok := deviceBinding(v, uuid)
		if !ok || !indices[proof.request.IfIndex] {
			continue
		}
		if pending.Availability == "" {
			c.linkRevision++
			pending = deviceUnavailable("LINUX_OBSERVATION_PENDING")
			if c.entries == nil {
				c.entries = make(map[string]cachedDevice)
			}
		}
		c.entries[proof.managementID] = cachedDevice{
			proof: proof, sample: pending, linkRevision: c.linkRevision, dirty: true,
		}
	}
}

func (s *Service) cachedDevice(proof deviceProof, epoch uint64) DeviceSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	if !deviceViewFresh(s.current, s.failure, now) {
		return deviceUnavailable("OVS_ASSOCIATION_STALE")
	}
	current, ok := deviceBinding(s.current, proof.ovsUUID)
	if !ok || current != proof || s.devices.epoch != epoch {
		return deviceUnavailable("OVS_DEVICE_BINDING_CHANGED")
	}
	if s.devices.observer == nil {
		return deviceUnavailable("LINUX_PROVIDER_UNAVAILABLE")
	}
	if entry, ok := s.devices.entries[proof.managementID]; ok && entry.proof == proof {
		age := now.Sub(entry.cachedAt)
		if !entry.cachedAt.IsZero() && (age < 0 || age > FreshFor) {
			return deviceUnavailable("LINUX_OBSERVATION_EXPIRED")
		}
		return entry.sample
	}
	return deviceUnavailable("LINUX_OBSERVATION_PENDING")
}

func (s *Service) RunDeviceObservations(ctx context.Context) {
	next := time.NewTimer(DeviceObservationInterval)
	defer next.Stop()
	for ctx.Err() == nil {
		s.SampleDevices(ctx)
		select {
		case <-ctx.Done():
			return
		case <-next.C:
			next.Reset(DeviceObservationInterval)
		}
	}
}

// SampleDevices publishes a bounded round of independent Linux observations.
// Concurrent calls are ignored. REST clients never start or wait for a round.
func (s *Service) SampleDevices(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	v, observer, epoch := s.current, s.devices.observer, s.devices.epoch
	if observer == nil || s.devices.sampling || !deviceViewFresh(v, s.failure, s.now()) {
		s.mu.Unlock()
		return
	}
	s.devices.sampling = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.devices.sampling = false
		s.mu.Unlock()
	}()

	round, cancel := context.WithTimeout(ctx, deviceObservationBudget)
	defer cancel()
	targets := s.prepareDeviceRound(v, epoch)
	if len(targets) == 0 {
		return
	}
	samples, attempted := observeDeviceRound(round, observer, targets)
	if ctx.Err() != nil || attempted == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.devices.epoch != epoch || !deviceViewFresh(s.current, s.failure, now) {
		return
	}
	for i, target := range targets[:attempted] {
		entry, ok := s.devices.entries[target.managementID]
		// Rebinding or link invalidation can happen during the provider read.
		// Publish only to the same binding at the same link revision.
		if ok && entry.proof == target.deviceProof && entry.linkRevision == target.linkRevision {
			entry.sample = samples[i]
			entry.cachedAt = now
			entry.dirty = false
			s.devices.entries[target.managementID] = entry
		}
		if target.priority {
			s.devices.dirtyAfter = target.managementID
		} else {
			s.devices.after = target.managementID
		}
	}
}

func (s *Service) prepareDeviceRound(v *view, epoch uint64) []deviceTarget {
	rows := v.observation.Rows["Interface"]
	if len(rows) > MaxRows {
		return nil
	}
	proofs := make([]deviceProof, 0, len(rows))
	for uuid := range rows {
		if proof, ok := deviceBinding(v, uuid); ok {
			proofs = append(proofs, proof)
		}
	}
	slices.SortFunc(proofs, func(a, b deviceProof) int { return strings.Compare(a.managementID, b.managementID) })
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.devices.epoch != epoch || !deviceViewFresh(s.current, s.failure, s.now()) {
		return nil
	}
	c := &s.devices
	limit := min(len(proofs), deviceObservationBatch)
	priority := make([]deviceTarget, 0, min(limit, deviceEventBatch))
	selected := make(map[string]bool, cap(priority))
	dirtyStart := sort.Search(len(proofs), func(i int) bool { return proofs[i].managementID > c.dirtyAfter })
	for i := 0; i < len(proofs) && len(priority) < cap(priority); i++ {
		proof := proofs[(dirtyStart+i)%len(proofs)]
		if entry := c.entries[proof.managementID]; entry.dirty && entry.proof == proof {
			priority = append(priority, deviceTarget{deviceProof: proof, linkRevision: entry.linkRevision, priority: true})
			selected[proof.managementID] = true
		}
	}

	// Keep separate rotations and interleave work so a tight deadline still
	// admits both event-driven and regular reads.
	targets := make([]deviceTarget, 0, limit)
	start := sort.Search(len(proofs), func(i int) bool { return proofs[i].managementID > c.after })
	regularLimit := limit - len(priority)
	for i, regular := 0, 0; i < len(proofs) && regular < regularLimit; i++ {
		proof := proofs[(start+i)%len(proofs)]
		current, ok := deviceBinding(s.current, proof.ovsUUID)
		if !ok || proof != current || selected[proof.managementID] {
			continue
		}
		entry, exists := c.entries[proof.managementID]
		if !exists {
			if c.entries == nil {
				c.entries = make(map[string]cachedDevice)
			}
			entry = cachedDevice{proof: proof, sample: deviceUnavailable("LINUX_OBSERVATION_PENDING")}
			c.entries[proof.managementID] = entry
		}
		if len(priority) != 0 {
			targets = append(targets, priority[0])
			priority = priority[1:]
		}
		targets = append(targets, deviceTarget{deviceProof: proof, linkRevision: entry.linkRevision})
		regular++
	}
	return append(targets, priority...)
}

func observeDeviceRound(ctx context.Context, observer DeviceObserver, targets []deviceTarget) ([]DeviceSample, int) {
	samples := make([]DeviceSample, len(targets))
	work := make(chan int)
	var workers sync.WaitGroup
	for range min(deviceObservationWorkers, len(targets)) {
		workers.Go(func() {
			for i := range work {
				sample := observer.Observe(ctx, targets[i].request)
				if ctx.Err() != nil {
					sample = deviceUnavailable("LINUX_OBSERVATION_TIMEOUT")
				} else if sample.Availability != "known" {
					sample = deviceUnavailable(sample.Reason)
				} else if len(sample.Fields) == 0 {
					sample.Fields = map[string]DeviceField{}
				} else {
					sample.Fields = maps.Clone(sample.Fields)
				}
				samples[i] = sample
			}
		})
	}
	attempted := 0
dispatch:
	for attempted < len(targets) && ctx.Err() == nil {
		select {
		case <-ctx.Done():
			break dispatch
		case work <- attempted:
			attempted++
		}
	}
	close(work)
	workers.Wait()
	return samples, attempted
}
