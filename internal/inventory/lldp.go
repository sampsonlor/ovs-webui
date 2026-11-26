package inventory

import (
	"context"
	"encoding/hex"
	"net"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	MaxLLDPInterfaces = 16
	MaxLLDPNeighbors  = 256
	lldpFrameLimit    = 128
	lldpInterval      = time.Second
	lldpReadBudget    = 750 * time.Millisecond
	lldpFreshFor      = 5 * time.Second
)

type LLDPID struct {
	Subtype  uint8  `json:"subtype"`
	Encoding string `json:"encoding"`
	Value    string `json:"value"`
}

type LLDPFrame struct {
	Interface         DeviceRequest
	ChassisID, PortID LLDPID
	SystemName        string
	TTL               uint16
	ReceivedAt        time.Time
}

type LLDPObservation struct {
	ObservedAt time.Time
	Frames     []LLDPFrame
	Covered    []DeviceRequest
	Truncated  bool
}

// LLDPObserver receives only scoped inbound LLDP on the local Linux links.
// A different epoch revokes every open handle and any unpublished packets.
type LLDPObserver interface {
	ObserveLLDP(context.Context, uint64, []DeviceRequest) (LLDPObservation, error)
}

type lldpPeer struct {
	id      string
	binding neighborBinding
	frame   LLDPFrame
}

func (p lldpPeer) active(now time.Time) bool {
	age := now.Sub(p.frame.ReceivedAt)
	return age >= 0 && age < time.Duration(p.frame.TTL)*time.Second
}

type lldpCache struct {
	observer   LLDPObserver
	grants     []string
	collecting bool
	epoch      uint64
	id         string
	sequence   uint64
	observedAt time.Time
	covered    []DeviceRequest
	peers      []lldpPeer
	truncated  bool
	failure    string
}

// invalidate fences the next publication and tells the provider to close its
// handles and discard queued frames before starting a new capture.
func (c *lldpCache) invalidate(reason string) {
	c.epoch++
	c.id, c.failure = "", reason
	c.observedAt = time.Time{}
	c.covered, c.peers = nil, nil
	c.truncated = false
}

func (s *Service) SetLLDPObserver(observer LLDPObserver, grants []string) error {
	if observer == nil || len(grants) == 0 || len(grants) > MaxLLDPInterfaces {
		return apitypes.Fail(422, "INVALID_LLDP_SCOPE")
	}
	ids := slices.Clone(grants)
	slices.Sort(ids)
	for i, id := range ids {
		if !apitypes.ManagementID(id) || i > 0 && ids[i-1] == id {
			return apitypes.Fail(422, "INVALID_LLDP_SCOPE")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lldp.observer, s.lldp.grants = observer, ids
	s.lldp.invalidate("LLDP_RESYNCING")
	return nil
}

func lldpBindings(v *view, grants []string) []neighborBinding {
	if v == nil || len(grants) == 0 {
		return nil
	}
	var result []neighborBinding
	for _, binding := range v.neighborScope.bindings {
		if _, allowed := slices.BinarySearch(grants, binding.managementID); allowed {
			result = append(result, binding)
		}
	}
	return result
}

func (s *Service) RunLLDP(ctx context.Context) {
	ticker := time.NewTicker(lldpInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.SampleLLDP(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) SampleLLDP(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if s.lldp.observer == nil || s.lldp.collecting {
		s.mu.Unlock()
		return
	}
	capture := &s.lldp
	ready := deviceViewFresh(s.current, s.failure, s.now())
	if !ready && capture.failure != "OVS_ASSOCIATION_STALE" {
		capture.invalidate("OVS_ASSOCIATION_STALE")
	}
	var bindings []neighborBinding
	if ready {
		bindings = lldpBindings(s.current, capture.grants)
	}
	observer, epoch := capture.observer, capture.epoch
	capture.collecting = true
	s.mu.Unlock()

	requests := make([]DeviceRequest, 0, len(bindings))
	for _, binding := range bindings {
		requests = append(requests, binding.request)
	}
	// An empty scope is sent as well so unavailable/ineligible links are closed.
	readCtx, cancel := context.WithTimeout(ctx, lldpReadBudget)
	observation, err := observer.ObserveLLDP(readCtx, epoch, requests)
	if readCtx.Err() != nil {
		err = apitypes.Fail(503, "LLDP_CAPTURE_UNAVAILABLE")
	}
	cancel()
	var checked []lldpPeer
	if err == nil {
		checked, err = qualifyLLDP(observation, bindings, s.now())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	capture.collecting = false
	if ctx.Err() != nil || capture.epoch != epoch || !ready || !deviceViewFresh(s.current, s.failure, s.now()) {
		return
	}
	if err != nil {
		capture.invalidate("LLDP_CAPTURE_UNAVAILABLE")
		return
	}
	capture.publish(observation, checked, s.now())
}

func qualifyLLDP(observation LLDPObservation, bindings []neighborBinding, now time.Time) ([]lldpPeer, error) {
	bad := apitypes.Fail(503, "LLDP_CAPTURE_UNPROVEN")
	if observation.ObservedAt.IsZero() || now.Before(observation.ObservedAt) || now.Sub(observation.ObservedAt) > lldpFreshFor ||
		len(observation.Frames) > lldpFrameLimit || len(observation.Covered) > len(bindings) {
		return nil, bad
	}
	covered := make(map[DeviceRequest]neighborBinding, len(observation.Covered))
	for _, req := range observation.Covered {
		i := slices.IndexFunc(bindings, func(b neighborBinding) bool { return b.request == req })
		if _, duplicate := covered[req]; i < 0 || duplicate {
			return nil, bad
		}
		covered[req] = bindings[i]
	}
	peers := make([]lldpPeer, 0, len(observation.Frames))
	for _, frame := range observation.Frames {
		binding, ok := covered[frame.Interface]
		if !ok || frame.ReceivedAt.IsZero() || frame.ReceivedAt.After(observation.ObservedAt) ||
			!validLLDPID(frame.ChassisID, 4) || !validLLDPID(frame.PortID, 3) || len(frame.SystemName) > 255 || !lldpText(frame.SystemName) {
			return nil, bad
		}
		// Use the local immutable identity and the complete typed remote MSAP.
		key := Digest([]LLDPID{frame.ChassisID, frame.PortID})
		id := schemaID("linux-passive-lldp-v1/"+binding.managementID+"/"+binding.ovsUUID, key)
		peers = append(peers, lldpPeer{id: id, binding: binding, frame: frame})
	}
	slices.SortStableFunc(peers, func(a, b lldpPeer) int { return a.frame.ReceivedAt.Compare(b.frame.ReceivedAt) })
	return peers, nil
}

func lldpText(text string) bool {
	return utf8.ValidString(text) && strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) < 0
}

func validLLDPID(id LLDPID, macSubtype uint8) bool {
	if id.Subtype < 1 || id.Subtype > 7 || id.Value == "" {
		return false
	}
	if id.Subtype == macSubtype {
		mac, err := net.ParseMAC(id.Value)
		return id.Encoding == "mac" && err == nil && len(mac) == 6 && mac.String() == id.Value
	}
	if id.Encoding == "text" {
		return len(id.Value) <= 255 && lldpText(id.Value)
	}
	if id.Encoding == "hex" && len(id.Value) <= 510 && id.Value == strings.ToLower(id.Value) {
		_, err := hex.DecodeString(id.Value)
		return err == nil
	}
	return false
}

func (c *lldpCache) publish(observation LLDPObservation, incoming []lldpPeer, now time.Time) {
	peers := make(map[string]lldpPeer, len(c.peers))
	// Dropped frames may include a withdrawal. Retain only newly observed
	// evidence in a loss round rather than carrying old peers through the gap.
	if !observation.Truncated {
		for _, peer := range c.peers {
			if peer.active(now) && slices.Contains(observation.Covered, peer.frame.Interface) {
				peers[peer.id] = peer
			}
		}
	}
	truncated := c.truncated || observation.Truncated
	for _, peer := range incoming {
		old, present := peers[peer.id]
		if present && peer.frame.ReceivedAt.Before(old.frame.ReceivedAt) {
			continue
		}
		if !peer.active(now) {
			delete(peers, peer.id)
			continue
		}
		if !present && len(peers) >= MaxLLDPNeighbors {
			truncated = true
			continue
		}
		peers[peer.id] = peer
	}
	rows := make([]lldpPeer, 0, len(peers))
	for _, peer := range peers {
		rows = append(rows, peer)
	}
	slices.SortFunc(rows, func(a, b lldpPeer) int { return strings.Compare(a.id, b.id) })
	covered := slices.Clone(observation.Covered)
	slices.SortFunc(covered, func(a, b DeviceRequest) int { return a.IfIndex - b.IfIndex })
	if c.id == "" || !slices.Equal(c.peers, rows) || !slices.Equal(c.covered, covered) || c.truncated != truncated {
		c.id = repository.NewID()
		c.sequence++
	}
	c.peers, c.covered, c.truncated = rows, covered, truncated
	c.observedAt, c.failure = observation.ObservedAt, ""
}
