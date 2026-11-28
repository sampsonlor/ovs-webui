package ovsdb

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestAccelerationMonitorsOnlyQualifiedOVSDBRuntimeShapes(t *testing.T) {
	const id = "420b0a46-3d3c-439a-a179-000000000001"
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/vswitch-" + version + ".ovsschema")
			if err != nil {
				t.Fatal(err)
			}
			d, err := discover(raw)
			if err != nil || !accelTestMonitored(d, "dpdk_initialized") || !accelTestMonitored(d, "dpdk_version") {
				t.Fatalf("native acceleration fields missing: %v", err)
			}
			rows := inventory.Rows{}
			valid := []byte(`{"Open_vSwitch":{"` + id + `":{"new":{"dpdk_initialized":false,"dpdk_version":"DPDK 23.11.1"}}}}`)
			if err := update(d, rows, valid, true); err != nil ||
				rows["Open_vSwitch"][id].Values["dpdk_initialized"] != false ||
				!reflect.DeepEqual(rows["Open_vSwitch"][id].Values["dpdk_version"], []any{"DPDK 23.11.1"}) {
				t.Fatalf("explicit false or version normalization failed: %+v, %v", rows, err)
			}
			bad := []byte(`{"Open_vSwitch":{"` + id + `":{"old":{"dpdk_initialized":false},"new":{"dpdk_initialized":"false"}}}}`)
			if err := update(d, rows, bad, false); err == nil {
				t.Fatal("wire string accepted as a runtime Boolean")
			}
			var schema map[string]any
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			columns := schema["tables"].(map[string]any)["Open_vSwitch"].(map[string]any)["columns"].(map[string]any)
			delete(columns, "dpdk_initialized")
			columns["dpdk_version"].(map[string]any)["type"] = "integer"
			alternate, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			d, err = discover(alternate)
			if err != nil || accelTestMonitored(d, "dpdk_initialized") || accelTestMonitored(d, "dpdk_version") {
				t.Fatalf("optional absent/incompatible fields were required or monitored: %v", err)
			}
		})
	}
}

func accelTestMonitored(d discovered, name string) bool {
	request := d.requests["Open_vSwitch"].([]any)[0].(map[string]any)
	return slices.Contains(request["columns"].([]string), name)
}
