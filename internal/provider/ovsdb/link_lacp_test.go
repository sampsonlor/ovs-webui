package ovsdb

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestLinkLACPMonitorsOnlyNativeOptionalBoolean(t *testing.T) {
	const uuid = "ea65a4d0-e6ac-4e94-8ff2-000000000001"
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			body, err := os.ReadFile("testdata/vswitch-" + version + ".ovsschema")
			if err != nil {
				t.Fatal(err)
			}
			d, err := discover(body)
			if err != nil || !linkLACPMonitored(d) {
				t.Fatalf("native negotiation not monitored: %v", err)
			}
			rows := inventory.Rows{}
			known := []byte(`{"Interface":{"` + uuid + `":{"new":{"lacp_current":true}}}}`)
			if err := update(d, rows, known, true); err != nil ||
				!reflect.DeepEqual(rows["Interface"][uuid].Values["lacp_current"], []any{true}) {
				t.Fatalf("valid native optional value was not normalized: %v", err)
			}
			corrupt := []byte(`{"Interface":{"` + uuid + `":{"old":{"lacp_current":true},"new":{"lacp_current":"bad"}}}}`)
			if err := update(d, rows, corrupt, false); err == nil {
				t.Fatal("malformed LACP value was accepted")
			}
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			columns := raw["tables"].(map[string]any)["Interface"].(map[string]any)["columns"].(map[string]any)
			columns["lacp_current"].(map[string]any)["type"] = "string"
			wrongType, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			d, err = discover(wrongType)
			if err != nil || linkLACPMonitored(d) {
				t.Fatalf("unsupported representation was monitored: %v", err)
			}
		})
	}
}

func linkLACPMonitored(d discovered) bool {
	request := d.requests["Interface"].([]any)[0].(map[string]any)
	return slices.Contains(request["columns"].([]string), "lacp_current")
}
