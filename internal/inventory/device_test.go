package inventory

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type deviceObserverFunc func(context.Context, DeviceRequest) DeviceSample

func (f deviceObserverFunc) Observe(ctx context.Context, r DeviceRequest) DeviceSample {
	return f(ctx, r)
}

type deviceTest struct {
	service     *Service
	observation Observation
	decision    Decision
	claims      authn.Claims
	binding     Binding
	now         time.Time
}

func newDeviceTest(t *testing.T) *deviceTest {
	t.Helper()
	s, o, d, c := fixture(t)
	f := &deviceTest{service: s, observation: o, decision: d, claims: c, now: o.Evidence.ObservedAt}
	for _, b := range d.Bindings {
		if b.Table == "Interface" {
			f.binding = b
			break
		}
	}
	s.now = func() time.Time { return f.now }
	f.observation = f.withDeviceValues(map[string]any{"type": "system", "ifindex": []any{"7"}})
	s.install(f.observation, d)
	return f
}

func (f *deviceTest) withDeviceValues(values map[string]any) Observation {
	observation := f.observation
	observation.Rows = maps.Clone(observation.Rows)
	observation.Rows["Interface"] = maps.Clone(observation.Rows["Interface"])
	row := observation.Rows["Interface"][f.binding.UUID]
	row.Values = maps.Clone(row.Values)
	maps.Copy(row.Values, values)
	observation.Rows["Interface"][f.binding.UUID] = row
	return observation
}

func (f *deviceTest) read(t *testing.T, id string, claims authn.Claims) map[string]any {
	t.Helper()
	result, err := f.service.Read(t.Context(), "readInterface", map[string]string{"interface_id": id}, nil, claims)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func (f *deviceTest) device(t *testing.T) map[string]any {
	t.Helper()
	return f.read(t, f.binding.ManagementID, f.claims)["linux_device"].(map[string]any)
}

func expectDeviceUnavailable(t *testing.T, device map[string]any, reason string) {
	t.Helper()
	source := device["source"].(map[string]any)
	if device["availability"] != "unavailable" || device["reason"] != reason ||
		device["ifindex"] != nil || len(device["fields"].(map[string]DeviceField)) != 0 ||
		source["observed_at"] != nil || source["freshness"] != "unavailable" {
		t.Fatalf("expected %s without device evidence, got %v", reason, device)
	}
}

func TestInterfaceLinuxDeviceSharedReadsAndPermissionSeparation(t *testing.T) {
	f := newDeviceTest(t)
	f.claims.Capabilities = []string{"inventory.read", "state.read"}
	var requests []DeviceRequest
	fields := map[string]DeviceField{"carrier": {Value: false, Availability: "known"}}
	f.service.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
		requests = append(requests, req)
		return DeviceSample{Availability: "known", IfIndex: 7, ObservedAt: f.now, Fields: fields}
	}))

	pending := f.read(t, f.binding.ManagementID, f.claims)
	expectDeviceUnavailable(t, pending["linux_device"].(map[string]any), "LINUX_OBSERVATION_PENDING")
	if len(requests) != 0 {
		t.Fatal("cache miss started a host read", requests)
	}
	f.service.SampleDevices(t.Context())
	item := f.read(t, f.binding.ManagementID, f.claims)
	device := item["linux_device"].(map[string]any)
	source := device["source"].(map[string]any)
	if item["config_revision"] != pending["config_revision"] ||
		device["availability"] != "known" || device["ifindex"] != 7 || source["provider_id"] != "linux" ||
		source["freshness"] != "fresh" || source["observed_at"] != f.now ||
		item["interface_type"] != "unknown" || item["fields"].(map[string]any)["type"].(map[string]any)["availability"] != "withheld" ||
		device["fields"].(map[string]DeviceField)["carrier"].Value != false {
		t.Fatal("source or permission conflation", item)
	}
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ := contract.Match("GET", "/api/v1/interfaces/"+f.binding.ManagementID)
	body, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.ValidateResponse(200, body); err != nil {
		t.Fatal(err)
	}

	other := f.claims
	other.PrincipalID, other.Revision = repository.NewID(), repository.NewID()
	fields["carrier"] = DeviceField{Value: true, Availability: "known"}
	delete(device["fields"].(map[string]DeviceField), "carrier")
	second := f.read(t, f.binding.ManagementID, other)
	secondDevice := second["linux_device"].(map[string]any)
	if second["config_revision"] != item["config_revision"] ||
		secondDevice["source"].(map[string]any)["observed_at"] != source["observed_at"] ||
		secondDevice["fields"].(map[string]DeviceField)["carrier"].Value != false {
		t.Fatal("second client did not receive the same isolated sample", second)
	}
	page, err := f.service.Read(t.Context(), "listInterfaces", nil, nil, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.(map[string]any)["items"].([]map[string]any) {
		if _, exists := entry["linux_device"]; exists {
			t.Fatal("Linux samples leaked into the inventory list")
		}
	}
	denied := f.claims
	denied.Capabilities = nil
	if _, err := f.service.Read(t.Context(), "readInterface", map[string]string{"interface_id": f.binding.ManagementID}, nil, denied); err == nil {
		t.Fatal("denied read returned device data")
	}
	for _, kind := range []string{"dpdk", "patch", "vxlan", "dummy", "future"} {
		f.service.install(f.withDeviceValues(map[string]any{"type": kind}), f.decision)
		f.service.SampleDevices(t.Context())
		expectDeviceUnavailable(t, f.device(t), "OVS_DEVICE_BINDING_UNPROVEN")
	}
	f.service.Unavailable("OVSDB_UNAVAILABLE")
	f.service.SampleDevices(t.Context())
	expectDeviceUnavailable(t, f.device(t), "OVS_ASSOCIATION_STALE")
	wantName := f.observation.Rows["Interface"][f.binding.UUID].Values["name"].(string)
	if len(requests) != 1 || requests[0] != (DeviceRequest{Name: wantName, IfIndex: 7}) {
		t.Fatal("only the confirmed association may start sampling", requests)
	}
}

func TestInterfaceLinuxDeviceConcurrentRebindingDropsEvidence(t *testing.T) {
	for _, change := range []string{"name", "index", "type", "identity", "generation", "source", "rebind-and-revert", "outage", "outage-and-recovery"} {
		t.Run(change, func(t *testing.T) {
			f := newDeviceTest(t)
			next := f.observation
			decision := f.decision
			readID, reason := f.binding.ManagementID, "LINUX_OBSERVATION_PENDING"
			switch change {
			case "name":
				next = f.withDeviceValues(map[string]any{"name": "replacement"})
			case "index", "rebind-and-revert":
				next = f.withDeviceValues(map[string]any{"ifindex": []any{"8"}})
			case "type":
				next = f.withDeviceValues(map[string]any{"type": "internal"})
			case "identity":
				decision.Bindings = maps.Clone(decision.Bindings)
				key := Key("Interface", f.binding.UUID)
				binding := decision.Bindings[key]
				binding.ManagementID = repository.NewID()
				readID = binding.ManagementID
				decision.Bindings[key] = binding
			case "generation":
				decision.Generation = repository.NewID()
			case "source":
				next.Evidence.Peer = "new-peer"
			case "outage":
				reason = "OVS_ASSOCIATION_STALE"
			}
			f.service.SetDeviceObserver(deviceObserverFunc(func(context.Context, DeviceRequest) DeviceSample {
				switch change {
				case "outage":
					f.service.Unavailable("OVSDB_UNAVAILABLE")
				case "outage-and-recovery":
					f.service.Unavailable("OVSDB_UNAVAILABLE")
					f.service.install(next, decision)
				case "rebind-and-revert":
					f.service.install(next, decision)
					f.service.install(f.observation, f.decision)
				default:
					f.service.install(next, decision)
				}
				return DeviceSample{
					Availability: "known", IfIndex: 7, ObservedAt: f.now,
					Fields: map[string]DeviceField{"pci_address": {Value: "0000:01:00.0", Availability: "known"}},
				}
			}))
			f.service.SampleDevices(t.Context())
			item := f.read(t, readID, f.claims)
			expectDeviceUnavailable(t, item["linux_device"].(map[string]any), reason)
		})
	}
}

func TestInterfaceLinuxDeviceExpiredAndMismatchedSamples(t *testing.T) {
	for _, name := range []string{"expired", "mismatched", "undated", "future-dated"} {
		t.Run(name, func(t *testing.T) {
			f := newDeviceTest(t)
			sample := DeviceSample{Availability: "known", IfIndex: 7, ObservedAt: f.now}
			switch name {
			case "expired":
				sample.ObservedAt = f.now.Add(-FreshFor - time.Second)
			case "mismatched":
				sample.IfIndex = 8
			case "undated":
				sample.ObservedAt = time.Time{}
			case "future-dated":
				sample.ObservedAt = f.now.Add(time.Second)
			}
			f.service.SetDeviceObserver(deviceObserverFunc(func(context.Context, DeviceRequest) DeviceSample {
				return sample
			}))
			f.service.SampleDevices(t.Context())
			expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_EXPIRED")
		})
	}
}

func TestInterfaceLinuxDeviceCacheExpiryFailureAndRecovery(t *testing.T) {
	f := newDeviceTest(t)
	next := DeviceSample{Availability: "known", IfIndex: 7, ObservedAt: f.now, Fields: map[string]DeviceField{
		"carrier": {Availability: "known", Value: true},
	}}
	f.service.SetDeviceObserver(deviceObserverFunc(func(context.Context, DeviceRequest) DeviceSample {
		return next
	}))
	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" {
		t.Fatal("scheduled sample was not published")
	}

	f.now = f.now.Add(FreshFor + time.Second)
	f.observation.Evidence.ObservedAt = f.now
	f.service.install(f.observation, f.decision)
	expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_EXPIRED")
	next.ObservedAt = f.now
	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" {
		t.Fatal("fresh sample did not replace expired evidence")
	}

	known := next
	next = deviceUnavailable("LINUX_OBSERVATION_TIMEOUT")
	f.service.SampleDevices(t.Context())
	expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_TIMEOUT")
	f.now = f.now.Add(FreshFor + time.Second)
	f.observation.Evidence.ObservedAt = f.now
	f.service.install(f.observation, f.decision)
	expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_EXPIRED")
	next = known
	next.ObservedAt = f.now
	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" {
		t.Fatal("successful sample did not supersede provider failure")
	}

	f.service.Unavailable("OVSDB_UNAVAILABLE")
	expectDeviceUnavailable(t, f.device(t), "OVS_ASSOCIATION_STALE")
	f.service.install(f.observation, f.decision)
	expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_PENDING")
	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" {
		t.Fatal("reconnected view did not receive a fresh sample")
	}
	f.service.SetDeviceObserver(nil)
	expectDeviceUnavailable(t, f.device(t), "LINUX_PROVIDER_UNAVAILABLE")
}

func TestInterfaceLinuxDeviceSamplingRotatesAcrossBoundedBatches(t *testing.T) {
	const batchLimit = deviceObservationBatch
	f := newDeviceTest(t)
	f.observation.Rows = maps.Clone(f.observation.Rows)
	f.observation.Rows["Interface"] = map[string]Row{}
	f.decision.Bindings = maps.Clone(f.decision.Bindings)
	for key, b := range f.decision.Bindings {
		if b.Table == "Interface" {
			delete(f.decision.Bindings, key)
		}
	}
	for index := 1; index <= 2*batchLimit; index++ {
		id := repository.NewID()
		f.observation.Rows["Interface"][id] = Row{UUID: id, Values: map[string]any{
			"name": "synthetic-" + strconv.Itoa(index), "type": "internal", "ifindex": []any{strconv.Itoa(index)},
		}}
		f.decision.Bindings[Key("Interface", id)] = Binding{ManagementID: repository.NewID(), Table: "Interface", UUID: id, State: "active"}
	}
	f.service.install(f.observation, f.decision)
	var mu sync.Mutex
	observed := map[int]int{}
	f.service.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
		mu.Lock()
		observed[req.IfIndex]++
		mu.Unlock()
		return DeviceSample{Availability: "known", IfIndex: req.IfIndex, ObservedAt: f.now}
	}))
	f.service.SampleDevices(t.Context())
	if len(observed) != batchLimit {
		t.Fatal("first pass did not respect batch budget", len(observed))
	}
	f.service.SampleDevices(t.Context())
	for index := 1; index <= 2*batchLimit; index++ {
		if observed[index] != 1 {
			t.Fatalf("device %d was sampled %d times before completing the rotation", index, observed[index])
		}
	}
}
