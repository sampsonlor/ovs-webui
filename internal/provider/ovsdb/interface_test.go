package ovsdb

import (
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestInterfaceNativeObservationColumnsAndStatusAllowlist(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			rows := inventory.Rows{}
			id := "00000000-0000-4000-8000-000000000002"
			native := map[string]any{"name": "synthetic-observe", "type": "future-native", "mtu": "1500", "mtu_request": []any{"set", []any{}}, "ofport": "-1", "status": []any{"map", []any{[]any{"driver_name", "synthetic-driver"}, []any{"bus_info", "0000:01:00.0"}, []any{"unexpected-secret", "never-publish"}}}}
			// Native integer JSON uses numeric values; normalized observation keeps exact decimal strings.
			native["mtu"], native["ofport"] = 1500, -1
			data, _ := json.Marshal(map[string]any{"Interface": map[string]any{id: map[string]any{"new": native}}})
			if err := update(d, rows, data, true); err != nil {
				t.Fatal(err)
			}
			value := rows["Interface"][id].Values
			status := value["status"].(map[string]any)
			if value["type"] != "future-native" || value["mtu"].([]any)[0] != "1500" || value["ofport"].([]any)[0] != "-1" || len(value["mtu_request"].([]any)) != 0 || len(status) != 2 || status["unexpected-secret"] != nil {
				t.Fatal(value)
			}
		})
	}
}
