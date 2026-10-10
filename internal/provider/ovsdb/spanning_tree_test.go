package ovsdb

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestSpanningTreeNativeSchemaObservationAndRedaction(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			for _, table := range []string{"Bridge", "Port"} {
				for _, column := range []string{"other_config", "status", "rstp_status"} {
					found := false
					for _, tt := range d.public.Tables {
						if tt.Name == table {
							for _, c := range tt.Columns {
								if c.Name == column {
									found = c.Monitored && c.SpanningTreeCompatible
								}
							}
						}
					}
					if !found {
						t.Fatalf("%s.%s observation missing", table, column)
					}
				}
			}
			for _, pair := range [][2]string{{"Bridge", "status"}, {"Bridge", "rstp_status"}, {"Port", "status"}, {"Port", "rstp_status"}, {"Bridge", "other_config"}} {
				keys := spanningTreeMapKeys(pair[0], pair[1])
				m := map[string]any{"unrelated-secret": "must-not-leak"}
				m[keys[0]] = "synthetic-safe"
				value := sanitize(pair[0], pair[1], m).(map[string]any)
				if len(value) != 1 || value[keys[0]] != "synthetic-safe" {
					t.Fatal(value)
				}
				invalid := sanitize(pair[0], pair[1], map[string]any{keys[0]: strings.Repeat("x", 129)}).(map[string]any)
				if raw, exists := invalid[keys[0]]; !exists || raw != nil {
					t.Fatal("invalid presence lost during filtering")
				}
				if graphConfigurationColumn(pair[0], pair[1], d.native.Tables[pair[0]].Columns[pair[1]]) && pair[1] != "other_config" {
					t.Fatal("runtime included in checkpoint")
				}
			}
		})
	}
}

func TestSpanningTreeFutureColumnShapesAreNotPromoted(t *testing.T) {
	for _, name := range []string{"stp_enable", "rstp_enable", "status", "rstp_status", "other_config"} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			_ = json.Unmarshal(b, &raw)
			columns := raw["tables"].(map[string]any)["Bridge"].(map[string]any)["columns"].(map[string]any)
			columns[name] = map[string]any{"type": "integer"}
			b, _ = json.Marshal(raw)
			d, err := discover(b)
			if err != nil {
				t.Fatal(err)
			}
			for _, tt := range d.public.Tables {
				if tt.Name == "Bridge" {
					for _, c := range tt.Columns {
						if c.Name == name && (c.Monitored || c.SpanningTreeCompatible) {
							t.Fatal("future shape admitted")
						}
					}
				}
			}
			requests, _ := json.Marshal(d.requests["Bridge"])
			// Persistent columns may still be privately guarded by the graph codec.
			if strings.Contains(string(requests), "password") || slices.Contains(inventory.SpanningTreeRuntimeKeys("Bridge", "status"), "unrelated-secret") {
				t.Fatal("unexpected public key")
			}
		})
	}
}
