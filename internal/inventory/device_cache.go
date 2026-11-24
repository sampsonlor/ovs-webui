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
)

type deviceProof struct {
	request                  DeviceRequest
	managementID, ovsUUID    string
	generation, nativeType   string
	root, schema, sourcePeer string
}

type cachedDevice struct {
	proof    deviceProof
	sample   DeviceSample
	cachedAt time.Time
}

// Service.mu guards the cache and the OVS view used for its bindings. Only the
// manager's observation worker calls the provider, outside that lock.
type deviceCache struct {
	observer DeviceObserver
	sampling bool
	epoch    uint64
	after    string
	entries  map[string]cachedDevice
}

func (c *deviceCache) invalidate() {
	c.epoch++
	c.after = ""
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
	ticker := time.NewTicker(DeviceObservationInterval)
	defer ticker.Stop()
	for {
		s.SampleDevices(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
	v, observer, epoch, after := s.current, s.devices.observer, s.devices.epoch, s.devices.after
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
	targets := s.prepareDeviceRound(v, epoch, after)
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
	for i, proof := range targets[:attempted] {
		entry, ok := s.devices.entries[proof.managementID]
		// An intervening install can drop this entry even if the same binding
		// reappears before the Linux observer returns. Never recreate it here.
		if ok && entry.proof == proof {
			entry.sample = samples[i]
			entry.cachedAt = now
			s.devices.entries[proof.managementID] = entry
		}
	}
	s.devices.after = targets[attempted-1].managementID
}

func (s *Service) prepareDeviceRound(v *view, epoch uint64, after string) []deviceProof {
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
	start := sort.Search(len(proofs), func(i int) bool { return proofs[i].managementID > after })
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.devices.epoch != epoch || !deviceViewFresh(s.current, s.failure, s.now()) {
		return nil
	}
	limit := min(len(proofs), deviceObservationBatch)
	targets := make([]deviceProof, 0, limit)
	for i := 0; i < len(proofs) && len(targets) < limit; i++ {
		proof := proofs[(start+i)%len(proofs)]
		current, ok := deviceBinding(s.current, proof.ovsUUID)
		if !ok || proof != current {
			continue
		}
		if s.devices.entries == nil {
			s.devices.entries = make(map[string]cachedDevice)
		}
		if _, exists := s.devices.entries[proof.managementID]; !exists {
			s.devices.entries[proof.managementID] = cachedDevice{proof: proof, sample: deviceUnavailable("LINUX_OBSERVATION_PENDING")}
		}
		targets = append(targets, proof)
	}
	return targets
}

func observeDeviceRound(ctx context.Context, observer DeviceObserver, targets []deviceProof) ([]DeviceSample, int) {
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
