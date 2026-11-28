package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func TestOpenFlowSourceRequiresObservedDatapathAndExactActiveBridge(t *testing.T) {
	const root = "9967464c-12e9-40ca-9532-000000000001"
	const uuid = "9967464c-12e9-40ca-9532-000000000002"
	const id = "9967464c-12e9-40ca-9532-000000000003"
	const generation = "9967464c-12e9-40ca-9532-000000000004"
	values := map[string]any{
		"name":         "br-proof",
		"datapath_id":  []any{"aa11223344556677"},
		"other_config": map[string]any{"datapath-id": "11aabbccddeeff00"},
	}
	b := Binding{Table: "Bridge", State: "active", UUID: uuid, ManagementID: id}
	now := time.Now()
	s := New(nil)
	s.now = func() time.Time { return now }
	s.failure = ""
	v := &view{
		observation: Observation{
			Rows: Rows{
				"Open_vSwitch": {root: {UUID: root, Values: map[string]any{"bridges": []any{uuid}}}},
				"Bridge":       {uuid: {UUID: uuid, Values: values}},
			},
			Evidence: Evidence{Root: root, ObservedAt: now},
		},
		decision: Decision{State: "confirmed", Generation: generation, Bindings: map[string]Binding{Key("Bridge", uuid): b}},
	}
	s.current = v
	got, err := s.OpenFlowSource(t.Context(), id)
	if err != nil || got.Bridge != b || got.Name != "br-proof" || got.Generation != generation || got.DatapathID != 0xaa11223344556677 {
		t.Fatalf("wrong immutable source: %+v, %v", got, err)
	}
	for _, bad := range []any{nil, []any{"aabb"}, []any{"0000000000000000"}, []any{"aabbccddeeff0011", "1122"}, "0x11223344556677"} {
		values["datapath_id"] = bad
		requireOpenFlowBindingCode(t, s, id, "OPENFLOW_DPID_UNOBSERVED")
	}
	values["datapath_id"] = []any{"aa11223344556677"}
	v.observation.Rows["Open_vSwitch"][root].Values["bridges"] = []any{}
	requireOpenFlowBindingCode(t, s, id, "OPENFLOW_BRIDGE_UNPROVEN")
	now = now.Add(FreshFor + time.Nanosecond)
	requireOpenFlowBindingCode(t, s, id, "OPENFLOW_INVENTORY_UNAVAILABLE")
}

func requireOpenFlowBindingCode(t *testing.T, s *Service, id, code string) {
	t.Helper()
	got, err := s.OpenFlowSource(t.Context(), id)
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Code != code || got.DatapathID != 0 {
		t.Fatalf("want %s and empty source, got %+v, %v", code, got, err)
	}
}
