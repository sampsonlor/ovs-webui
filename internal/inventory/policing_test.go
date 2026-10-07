package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
)

type policingObserverFunc func(context.Context, DeviceRequest) PolicingSample

func (f policingObserverFunc) ObservePolicing(ctx context.Context, r DeviceRequest) PolicingSample {
	return f(ctx, r)
}

func TestInterfacePolicingPermissionsScopeAndRevision(t *testing.T) {
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
	calls := 0
	rate := "18446744073709551615"
	s.SetPolicingObserver(policingObserverFunc(func(_ context.Context, req DeviceRequest) PolicingSample {
		calls++
		if req.Name != row.Values["name"] || req.IfIndex != 7 {
			t.Fatal(req)
		}
		return PolicingSample{Availability: "known", ObservedAt: s.now(), IfIndex: 7, FilterCount: 1, Actions: []PolicingAction{{FilterKind: "basic", Handle: "0x00000001", BytesPerSecond: &rate, ExceedAction: "drop", ConformAction: "continue"}}}
	}))
	read := func() map[string]any {
		t.Helper()
		value, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c)
		if err != nil {
			t.Fatal(err)
		}
		contract, err := apicontract.New()
		if err != nil {
			t.Fatal(err)
		}
		op, _, _ := contract.Match("GET", "/api/v1/interfaces/"+b.ManagementID)
		body, _ := json.Marshal(value)
		if err := op.ValidateResponse(200, body); err != nil {
			t.Fatal(err)
		}
		return value.(map[string]any)
	}
	c.Capabilities = []string{"inventory.read", "state.read"}
	if sample := read()["linux_ingress_policing"].(map[string]any); sample["availability"] != "withheld" || calls != 0 || sample["filter_count"] != nil || sample["ifindex"] != nil || len(sample["actions"].([]PolicingAction)) != 0 {
		t.Fatal("state reader acquired rule configuration", sample, calls)
	}
	c.Capabilities = append(c.Capabilities, "configuration.read")
	first := read()
	sample := first["linux_ingress_policing"].(map[string]any)
	if sample["availability"] != "known" || sample["source"].(map[string]any)["authority"] != "linux-netlink-observation" || *sample["actions"].([]PolicingAction)[0].BytesPerSecond != rate {
		t.Fatal(sample)
	}
	if read()["config_revision"] != first["config_revision"] {
		t.Fatal("runtime sample changed config revision")
	}
	_, err := s.Read(context.Background(), "listInterfaces", nil, url.Values{}, c)
	if err != nil || calls != 2 {
		t.Fatal("list triggered host scans", calls, err)
	}
	for _, typ := range []string{"dpdk", "patch", "vxlan", "dummy", "future"} {
		row.Values["type"] = typ
		s.install(o, d)
		if sample := read()["linux_ingress_policing"].(map[string]any); sample["availability"] != "unavailable" || calls != 2 {
			t.Fatal(typ, sample, calls)
		}
	}
	row.Values["type"] = "internal"
	s.install(o, d)
	s.Unavailable("OVSDB_UNAVAILABLE")
	if sample := read()["linux_ingress_policing"].(map[string]any); sample["reason"] != "OVS_ASSOCIATION_STALE" || calls != 2 {
		t.Fatal(sample, calls)
	}
}

func TestInterfacePolicingRebindingAndInvalidSamplesDiscardValues(t *testing.T) {
	for _, change := range []string{"index", "type", "identity", "generation", "outage", "expired", "mismatch", "invalid-rate", "too-many", "failed-with-values", "partial"} {
		t.Run(change, func(t *testing.T) {
			s, o, d, c := fixture(t)
			c.Capabilities = []string{"inventory.read", "state.read", "configuration.read"}
			var b Binding
			for _, binding := range d.Bindings {
				if binding.Table == "Interface" {
					b = binding
					break
				}
			}
			o.Rows["Interface"][b.UUID].Values["type"] = "internal"
			o.Rows["Interface"][b.UUID].Values["ifindex"] = []any{"7"}
			s.install(o, d)
			s.SetPolicingObserver(policingObserverFunc(func(context.Context, DeviceRequest) PolicingSample {
				encoded, _ := json.Marshal(o)
				var next Observation
				_ = json.Unmarshal(encoded, &next)
				decision := d
				decision.Bindings = map[string]Binding{}
				for key, value := range d.Bindings {
					decision.Bindings[key] = value
				}
				rate := "125000"
				sample := PolicingSample{Availability: "known", ObservedAt: s.now(), IfIndex: 7, FilterCount: 1, Actions: []PolicingAction{{FilterKind: "basic", Handle: "0x00000001", BytesPerSecond: &rate, ExceedAction: "drop", ConformAction: "continue"}}}
				switch change {
				case "index":
					next.Rows["Interface"][b.UUID].Values["ifindex"] = []any{"8"}
				case "type":
					next.Rows["Interface"][b.UUID].Values["type"] = "patch"
				case "identity":
					binding := decision.Bindings[Key("Interface", b.UUID)]
					binding.ManagementID = "replacement"
					decision.Bindings[Key("Interface", b.UUID)] = binding
				case "generation":
					decision.Generation = "replacement"
				case "expired":
					sample.ObservedAt = s.now().Add(-time.Minute)
				case "mismatch":
					sample.IfIndex = 8
				case "invalid-rate":
					rate = "18446744073709551616"
				case "too-many":
					sample.Actions = make([]PolicingAction, 17)
				case "failed-with-values":
					sample.Availability, sample.Reason = "unavailable", "LINUX_POLICING_DUMP_UNPROVEN"
				case "partial":
					sample.Availability, sample.Reason = "partial", "LINUX_POLICING_COVERAGE_PARTIAL"
				}
				s.install(next, decision)
				if change == "outage" {
					s.Unavailable("OVSDB_UNAVAILABLE")
				}
				return sample
			}))
			result, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c)
			if err != nil {
				t.Fatal(err)
			}
			sample := result.(map[string]any)["linux_ingress_policing"].(map[string]any)
			if change == "partial" {
				if sample["availability"] != "partial" || len(sample["actions"].([]PolicingAction)) != 1 {
					t.Fatal("partial evidence lost its explicit coverage", sample)
				}
			} else if sample["availability"] != "unavailable" || sample["ifindex"] != nil || sample["filter_count"] != nil || len(sample["actions"].([]PolicingAction)) != 0 || sample["source"].(map[string]any)["observed_at"] != nil {
				t.Fatal("unproven values survived", sample)
			}
		})
	}
}
