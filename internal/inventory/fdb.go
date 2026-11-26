package inventory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	MaxFDBBridges = 8
	MaxFDBRows    = 256
	fdbInterval   = 12 * time.Second
	fdbFreshFor   = 36 * time.Second
)

// FDBRequest is built from the root-selected, confirmed inventory. No API
// query can invoke a native request or choose the daemon arguments.
type FDBRequest struct {
	Bridge   Binding
	Name     string
	Evidence Evidence
}

type FDBNativeRow struct {
	MAC        string  `json:"mac"`
	VLAN       *uint16 `json:"vlan"`
	OFPort     *uint16 `json:"ofport"`
	PortName   *string `json:"port_name"`
	AgeSeconds *uint32 `json:"age_seconds"`
}

type FDBSample struct {
	ObservedAt time.Time
	Total      uint32
	Rows       []FDBNativeRow
}

type FDBObserver interface {
	ObserveFDB(context.Context, FDBRequest) (FDBSample, error)
}

type fdbBridge struct {
	binding Binding
	name    string
	ports   map[uint16][]fdbPort
}

type fdbPort struct {
	name    string
	binding Binding
}

type fdbEntry struct {
	FDBNativeRow
	id             string
	bridge         fdbBridge
	port           Binding
	portResolution string
	observedAt     time.Time
}

type fdbBridgeSample struct {
	observedAt time.Time
	total      uint32
	entries    []fdbEntry
	failure    string
}

// Every round publishes a complete immutable collection of successes and
// failures. Requests never fall back to an older sample following a failure.
type fdbCache struct {
	observer FDBObserver
	grants   []string
	scope    []fdbBridge
	fence    string
	epoch    uint64
	id       string
	sequence uint64
	samples  map[string]fdbBridgeSample
}

func (s *Service) SetFDBObserver(observer FDBObserver, ids []string) error {
	if observer == nil || len(ids) == 0 || len(ids) > MaxFDBBridges {
		return apitypes.Fail(422, "INVALID_FDB_OBSERVATION_SCOPE")
	}
	grants := slices.Clone(ids)
	slices.Sort(grants)
	for i, id := range grants {
		if !apitypes.ManagementID(id) || i > 0 && grants[i-1] == id {
			return apitypes.Fail(422, "INVALID_FDB_OBSERVATION_SCOPE")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope, fence := fdbScope(s.current, grants)
	s.fdb = fdbCache{observer: observer, grants: grants, epoch: s.fdb.epoch + 1, scope: scope, fence: fence}
	return nil
}

func (c *fdbCache) invalidate() {
	c.epoch++
	c.id = ""
	c.samples = nil
}

func (c *fdbCache) install(v *view) {
	if c.observer == nil {
		return
	}
	scope, fence := fdbScope(v, c.grants)
	if fence != c.fence {
		c.fence, c.scope = fence, scope
		c.invalidate()
	}
}

func fdbScope(v *view, grants []string) ([]fdbBridge, string) {
	if v == nil || v.decision.State != "confirmed" {
		return nil, ""
	}
	o := v.observation
	e := o.Evidence
	root := o.Rows["Open_vSwitch"][e.Root]
	e.File = FileWitness{Available: e.File.Available, Device: e.File.Device, Inode: e.File.Inode, ServerHasFile: e.File.ServerHasFile, ServerBinding: e.File.ServerBinding}
	e.ObservedAt = time.Time{}
	e.Continuous = false
	e.Anchors = nil
	image := []any{v.decision.Generation, e}
	var scope []fdbBridge
	for _, uuid := range refs(root.Values["bridges"]) {
		b := v.decision.Bindings[Key("Bridge", uuid)]
		if b.State != "active" || !slices.Contains(grants, b.ManagementID) {
			continue
		}
		row := o.Rows["Bridge"][uuid]
		bridge := fdbBridge{binding: b, name: textValue(row.Values["name"]), ports: make(map[uint16][]fdbPort)}
		image = append(image, []any{b, row.Configuration, bridge.name, row.Values["ports"]})
		for _, portID := range refs(row.Values["ports"]) {
			p := o.Rows["Port"][portID]
			binding := v.decision.Bindings[Key("Port", portID)]
			name := textValue(p.Values["name"])
			image = append(image, []any{binding, p.Configuration, name, p.Values["interfaces"]})
			for _, ifaceID := range refs(p.Values["interfaces"]) {
				i := o.Rows["Interface"][ifaceID]
				iBinding := v.decision.Bindings[Key("Interface", ifaceID)]
				number := textValue(i.Values["ofport"])
				image = append(image, []any{iBinding, i.Configuration, number})
				n, err := strconv.ParseUint(number, 10, 16)
				if err == nil && n > 0 && (n < 65280 || n == 65534) {
					port := fdbPort{name, binding}
					if iBinding.State != "active" {
						port.binding = Binding{}
					}
					bridge.ports[uint16(n)] = append(bridge.ports[uint16(n)], port)
				}
			}
		}
		scope = append(scope, bridge)
	}
	slices.SortFunc(scope, func(a, b fdbBridge) int { return strings.Compare(a.binding.ManagementID, b.binding.ManagementID) })
	return scope, Digest(image)
}

func (s *Service) RunFDB(ctx context.Context) {
	tick := time.NewTicker(fdbInterval)
	defer tick.Stop()
	for ctx.Err() == nil {
		s.collectFDB(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) collectFDB(ctx context.Context) {
	s.mu.RLock()
	cache, v, failure := s.fdb, s.current, s.failure
	s.mu.RUnlock()
	if cache.observer == nil || !deviceViewFresh(v, failure, s.now()) {
		return
	}
	samples := make(map[string]fdbBridgeSample, len(cache.scope))
	for _, bridge := range cache.scope {
		s.mu.RLock()
		current, failure, epoch := s.current, s.failure, s.fdb.epoch
		s.mu.RUnlock()
		if epoch != cache.epoch || !deviceViewFresh(current, failure, s.now()) {
			return
		}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		native, err := cache.observer.ObserveFDB(bounded, FDBRequest{bridge.binding, bridge.name, current.observation.Evidence})
		timedOut := errors.Is(bounded.Err(), context.DeadlineExceeded)
		cancel()
		var sample fdbBridgeSample
		switch {
		case timedOut:
			sample.failure = "timeout"
		case err != nil:
			sample.failure = fdbFailure(err)
		default:
			sample = qualifyFDBSample(bridge, native, s.now())
		}
		samples[bridge.binding.ManagementID] = sample
		if ctx.Err() != nil {
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.fdb.epoch != cache.epoch || !deviceViewFresh(s.current, s.failure, s.now()) {
		return
	}
	s.fdb.id = repository.NewID()
	s.fdb.samples = samples
	s.fdb.sequence++
}

func fdbFailure(err error) string {
	var problem *apitypes.Problem
	if errors.As(err, &problem) {
		switch problem.Code {
		case "FDB_SOURCE_UNSUPPORTED":
			return "unsupported"
		case "FDB_SOURCE_UNAVAILABLE":
			return "source_unavailable"
		case "FDB_SOURCE_PERMISSION":
			return "permission"
		case "FDB_OBSERVATION_TIMEOUT":
			return "timeout"
		}
	}
	return "source_invalid"
}

func fdbMAC(value string) (string, bool) {
	if len(value) != 17 {
		return "", false
	}
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 {
		return "", false
	}
	return mac.String(), true
}

func qualifyFDBSample(bridge fdbBridge, native FDBSample, now time.Time) fdbBridgeSample {
	invalid := fdbBridgeSample{failure: "source_invalid"}
	if native.ObservedAt.IsZero() || now.Before(native.ObservedAt) || now.Sub(native.ObservedAt) > fdbFreshFor ||
		native.Rows == nil || int64(len(native.Rows)) != min(int64(native.Total), MaxFDBRows) {
		return invalid
	}
	result := fdbBridgeSample{observedAt: native.ObservedAt, total: native.Total, entries: make([]fdbEntry, 0, len(native.Rows))}
	seen := make(map[string]bool, len(native.Rows))
	for _, row := range native.Rows {
		mac, ok := fdbMAC(row.MAC)
		if !ok || row.VLAN == nil || *row.VLAN > 4095 || row.OFPort != nil && (*row.OFPort == 0 || *row.OFPort == 65535) {
			return invalid
		}
		row.MAC = mac
		key := fmt.Sprintf("%04d/%s", *row.VLAN, mac)
		if seen[key] {
			return invalid
		}
		seen[key] = true
		e := fdbEntry{FDBNativeRow: row, id: schemaID("ovs-fdb-v1/"+bridge.binding.ManagementID+"/"+bridge.binding.UUID, key),
			bridge: bridge, portResolution: "unmapped", observedAt: native.ObservedAt}
		if row.OFPort != nil {
			candidates := bridge.ports[*row.OFPort]
			switch {
			case len(candidates) > 1:
				e.portResolution = "ambiguous"
			case len(candidates) == 1 && row.PortName != nil:
				p := candidates[0]
				if p.binding.State == "active" && p.name == *row.PortName {
					e.port, e.portResolution = p.binding, "resolved"
				}
			}
		}
		result.entries = append(result.entries, e)
	}
	slices.SortFunc(result.entries, func(a, b fdbEntry) int { return strings.Compare(a.id, b.id) })
	return result
}
