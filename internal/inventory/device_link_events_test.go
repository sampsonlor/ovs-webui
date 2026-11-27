package inventory

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func addEventTestDevice(f *deviceTest, name string, index int) Binding {
	f.observation.Rows = maps.Clone(f.observation.Rows)
	f.observation.Rows["Interface"] = maps.Clone(f.observation.Rows["Interface"])
	f.observation.Rows["Port"] = maps.Clone(f.observation.Rows["Port"])
	f.decision.Bindings = maps.Clone(f.decision.Bindings)
	id := repository.NewID()
	row := Row{UUID: id, Values: map[string]any{
		"name": name, "type": "system", "ifindex": []any{strconv.Itoa(index)},
	}}
	binding := Binding{ManagementID: repository.NewID(), Table: "Interface", UUID: id, State: "active"}
	f.observation.Rows["Interface"][id] = row
	f.decision.Bindings[Key("Interface", id)] = binding
	for portID, port := range f.observation.Rows["Port"] {
		port.Values = maps.Clone(port.Values)
		port.Values["interfaces"] = append(slices.Clone(port.Values["interfaces"].([]any)), id)
		f.observation.Rows["Port"][portID] = port
	}
	f.service.install(f.observation, f.decision)
	return binding
}

func TestDeviceLinkEventsOnlyInvalidateMatchingManagedDevices(t *testing.T) {
	f := newDeviceTest(t)
	sharedIndex := addEventTestDevice(f, "same-index", 7)
	independent := addEventTestDevice(f, "independent", 8)
	observed := map[DeviceRequest]int{}
	var mu sync.Mutex
	observer := deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
		mu.Lock()
		observed[req]++
		mu.Unlock()
		return DeviceSample{Availability: "known", IfIndex: req.IfIndex, ObservedAt: f.now}
	})
	f.service.SetDeviceObserver(observer)
	f.service.SampleDevices(t.Context())
	if len(observed) != 3 {
		t.Fatal("expected all three managed devices to be sampled", observed)
	}

	read := func(binding Binding) map[string]any {
		return f.read(t, binding.ManagementID, f.claims)["linux_device"].(map[string]any)
	}
	f.service.HandleDeviceLinkEvents(DeviceLinkEvents{Indices: []int{1234}})
	if f.device(t)["availability"] != "known" || f.service.devices.linkRevision != 0 {
		t.Fatal("an unmanaged index invalidated observations or set pending work")
	}
	for range 10 {
		f.service.HandleDeviceLinkEvents(DeviceLinkEvents{Indices: []int{7, 7, 1234}})
	}
	if len(f.service.devices.entries) != 3 {
		t.Fatal("event burst added entries to the bounded cache")
	}
	expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_PENDING")
	expectDeviceUnavailable(t, read(sharedIndex), "LINUX_OBSERVATION_PENDING")
	if read(independent)["availability"] != "known" {
		t.Fatal("an unrelated managed index lost its sample")
	}

	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" || read(sharedIndex)["availability"] != "known" {
		t.Fatal("event-assisted round did not replace both invalidated samples")
	}
	for req, count := range observed {
		if count != 2 {
			t.Fatalf("read %d times for %v, want one read per window", count, req)
		}
	}
}

func TestDeviceLinkEventsFenceInFlightSamples(t *testing.T) {
	for _, tc := range []struct {
		name string
		hint []DeviceLinkEvents
	}{
		{"changed-index", []DeviceLinkEvents{{Indices: []int{7}}}},
		{"lost", []DeviceLinkEvents{{Lost: true}}},
		{"lost-and-recreated", []DeviceLinkEvents{{Lost: true}, {Indices: []int{7}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeviceTest(t)
			inFlight := false
			f.service.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
				if inFlight {
					for _, event := range tc.hint {
						f.service.HandleDeviceLinkEvents(event)
					}
				}
				return DeviceSample{Availability: "known", IfIndex: req.IfIndex, ObservedAt: f.now}
			}))
			f.service.SampleDevices(t.Context())
			if f.device(t)["availability"] != "known" {
				t.Fatal("initial sample missing")
			}
			inFlight = true
			f.service.SampleDevices(t.Context())
			expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_PENDING")
			inFlight = false
			f.service.SampleDevices(t.Context())
			if f.device(t)["availability"] != "known" {
				t.Fatal("resampling after link invalidation did not restore a fresh observation")
			}
		})
	}
}

func TestDeviceLinkEventsAreKeptAcrossUnchangedOVSRecovery(t *testing.T) {
	f := newDeviceTest(t)
	f.now = f.now.Add(FreshFor - time.Second)
	f.service.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
		return DeviceSample{Availability: "known", IfIndex: req.IfIndex, ObservedAt: f.now}
	}))
	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" {
		t.Fatal("initial sample missing")
	}
	f.now = f.now.Add(2 * time.Second)
	expectDeviceUnavailable(t, f.device(t), "OVS_ASSOCIATION_STALE")
	f.service.HandleDeviceLinkEvents(DeviceLinkEvents{Indices: []int{7}})
	f.observation.Evidence.ObservedAt = f.now
	f.service.install(f.observation, f.decision)
	expectDeviceUnavailable(t, f.device(t), "LINUX_OBSERVATION_PENDING")
	f.service.SampleDevices(t.Context())
	if f.device(t)["availability"] != "known" {
		t.Fatal("retained invalidation did not trigger a new observation")
	}
}

func TestDeviceLinkEventRoundsPreservePeriodicRotation(t *testing.T) {
	f := newDeviceTest(t)
	for index := 8; index < 7+2*deviceObservationBatch; index++ {
		addEventTestDevice(f, "event"+strconv.Itoa(index), index)
	}
	observed := map[int]int{}
	var mu sync.Mutex
	f.service.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
		mu.Lock()
		observed[req.IfIndex]++
		mu.Unlock()
		return DeviceSample{Availability: "known", IfIndex: req.IfIndex, ObservedAt: f.now}
	}))
	f.service.SampleDevices(t.Context())
	if len(observed) != deviceObservationBatch {
		t.Fatal("periodic round did not respect batch budget")
	}
	// Under continuous link churn, the next round must service some dirty
	// targets from the first batch and other targets from its regular rotation.
	changed := make([]int, 0, len(observed))
	for index := range observed {
		changed = append(changed, index)
	}
	f.service.HandleDeviceLinkEvents(DeviceLinkEvents{Indices: changed})
	targets := f.service.prepareDeviceRound(f.service.current, f.service.devices.epoch)
	if len(targets) != deviceObservationBatch || !targets[0].priority || targets[1].priority {
		t.Fatal("expected a single bounded round alternating dirty and regular reads")
	}
	f.service.SampleDevices(t.Context())
	if len(observed) != 2*deviceObservationBatch-deviceEventBatch {
		t.Fatalf("dirty devices consumed the regular rotation's share, sampled %d devices", len(observed))
	}
	var reads int
	for _, count := range observed {
		reads += count
	}
	if reads != 2*deviceObservationBatch {
		t.Fatalf("samples exceeded one aggregate round per window, got %d provider calls", reads)
	}
	f.service.HandleDeviceLinkEvents(DeviceLinkEvents{Indices: changed})
	f.service.SampleDevices(t.Context())
	if len(observed) != 2*deviceObservationBatch {
		t.Fatalf("link event batch stalled periodic rotation, sampled %d devices", len(observed))
	}
}
