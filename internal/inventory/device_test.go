package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
)

type deviceObserverFunc func(context.Context, DeviceRequest) DeviceSample

func (f deviceObserverFunc) Observe(ctx context.Context, r DeviceRequest) DeviceSample {
	return f(ctx, r)
}

func TestInterfaceLinuxDeviceScopeAndPermissionSeparation(t *testing.T) {
	s, o, d, c := fixture(t)
	var b Binding
	for _, binding := range d.Bindings {
		if binding.Table == "Interface" {
			b = binding
			break
		}
	}
	row := o.Rows["Interface"][b.UUID]
	row.Values["type"], row.Values["ifindex"] = "system", []any{"7"}
	s.install(o, d)
	c.Capabilities = []string{"inventory.read", "state.read"}
	calls := 0
	s.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, r DeviceRequest) DeviceSample {
		calls++
		if r.Name != row.Values["name"] || r.IfIndex != 7 {
			t.Fatal(r)
		}
		return DeviceSample{Availability: "known", IfIndex: 7, ObservedAt: s.now(), Fields: map[string]DeviceField{"carrier": {Value: false, Availability: "known"}}}
	}))
	read := func() map[string]any {
		t.Helper()
		result, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c)
		if err != nil {
			t.Fatal(err)
		}
		item := result.(map[string]any)
		contract, err := apicontract.New()
		if err != nil {
			t.Fatal(err)
		}
		op, _, _ := contract.Match("GET", "/api/v1/interfaces/"+b.ManagementID)
		body, _ := json.Marshal(item)
		if err := op.ValidateResponse(200, body); err != nil {
			t.Fatal(err)
		}
		return item
	}
	item := read()
	device := item["linux_device"].(map[string]any)
	if device["availability"] != "known" || device["source"].(map[string]any)["provider_id"] != "linux" || item["interface_type"] != "unknown" || item["fields"].(map[string]any)["type"].(map[string]any)["availability"] != "withheld" {
		t.Fatal("source or permission conflation", item)
	}
	revision := item["config_revision"]
	if read()["config_revision"] != revision {
		t.Fatal("Linux sampling changed configuration revision")
	}
	_, err := s.Read(context.Background(), "listInterfaces", nil, url.Values{}, c)
	if err != nil || calls != 2 {
		t.Fatal("list started a per-row host scan", calls, err)
	}
	for _, typ := range []string{"dpdk", "patch", "vxlan", "dummy", "future"} {
		row.Values["type"] = typ
		s.install(o, d)
		if read()["linux_device"].(map[string]any)["availability"] != "unavailable" || calls != 2 {
			t.Fatal("unsupported type acquired host identity", typ)
		}
	}
	row.Values["type"] = "internal"
	s.install(o, d)
	s.Unavailable("OVSDB_UNAVAILABLE")
	if read()["linux_device"].(map[string]any)["reason"] != "OVS_ASSOCIATION_STALE" || calls != 2 {
		t.Fatal("stale association performed a fresh host read")
	}
	c.Capabilities = nil
	if _, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c); err == nil || calls != 2 {
		t.Fatal("denied read reached host provider", err)
	}
}

func TestInterfaceLinuxDeviceConcurrentRebindingDropsEvidence(t *testing.T) {
	for _, change := range []string{"index", "type", "identity", "generation", "outage"} {
		t.Run(change, func(t *testing.T) {
			s, o, d, c := fixture(t)
			var b Binding
			for _, binding := range d.Bindings {
				if binding.Table == "Interface" {
					b = binding
					break
				}
			}
			row := o.Rows["Interface"][b.UUID]
			row.Values["type"], row.Values["ifindex"] = "internal", []any{"7"}
			s.install(o, d)
			s.SetDeviceObserver(deviceObserverFunc(func(context.Context, DeviceRequest) DeviceSample {
				// Published snapshots are immutable; create an independent changed copy.
				encoded, _ := json.Marshal(o)
				var next Observation
				_ = json.Unmarshal(encoded, &next)
				encoded, _ = json.Marshal(d)
				var decision Decision
				_ = json.Unmarshal(encoded, &decision)
				decision.Bindings = map[string]Binding{}
				for key, value := range d.Bindings {
					decision.Bindings[key] = value
				}
				switch change {
				case "index":
					next.Rows["Interface"][b.UUID].Values["ifindex"] = []any{"8"}
				case "type":
					next.Rows["Interface"][b.UUID].Values["type"] = "patch"
				case "identity":
					binding := decision.Bindings[Key("Interface", b.UUID)]
					binding.ManagementID = "different"
					decision.Bindings[Key("Interface", b.UUID)] = binding
				case "generation":
					decision.Generation = "different"
				}
				s.install(next, decision)
				if change == "outage" {
					s.Unavailable("OVSDB_UNAVAILABLE")
				}
				return DeviceSample{Availability: "known", IfIndex: 7, ObservedAt: s.now(), Fields: map[string]DeviceField{"pci_address": {Value: "0000:01:00.0", Availability: "known"}}}
			}))
			result, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c)
			if err != nil {
				t.Fatal(err)
			}
			device := result.(map[string]any)["linux_device"].(map[string]any)
			if device["reason"] != "OVS_DEVICE_BINDING_CHANGED" || device["ifindex"] != nil || len(device["fields"].(map[string]DeviceField)) != 0 || device["source"].(map[string]any)["observed_at"] != nil {
				t.Fatal("old device evidence survived rebinding", device)
			}
		})
	}
}

func TestInterfaceLinuxDeviceExpiredAndMismatchedSamples(t *testing.T) {
	for _, sample := range []DeviceSample{{Availability: "known", IfIndex: 7, ObservedAt: time.Now().Add(-time.Minute)}, {Availability: "known", IfIndex: 8, ObservedAt: time.Now()}, {Availability: "known", IfIndex: 7}} {
		s, o, d, c := fixture(t)
		var b Binding
		for _, binding := range d.Bindings {
			if binding.Table == "Interface" {
				b = binding
				break
			}
		}
		o.Rows["Interface"][b.UUID].Values["type"] = "system"
		o.Rows["Interface"][b.UUID].Values["ifindex"] = []any{"7"}
		s.install(o, d)
		s.SetDeviceObserver(deviceObserverFunc(func(context.Context, DeviceRequest) DeviceSample { return sample }))
		result, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c)
		if err != nil {
			t.Fatal(err)
		}
		device := result.(map[string]any)["linux_device"].(map[string]any)
		if device["availability"] != "unavailable" || device["ifindex"] != nil || device["source"].(map[string]any)["observed_at"] != nil {
			t.Fatal("stale or wrong host sample accepted", device)
		}
	}
}
