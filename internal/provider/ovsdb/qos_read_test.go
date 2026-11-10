package ovsdb

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestQoSMonitorKeepsUnboundRootsAndBoundsOptionalMaps(t *testing.T) {
	d := schema(t, "3.3.9")
	queue := "00000000-0000-4000-8000-000000000001"
	qos := "00000000-0000-4000-8000-000000000002"
	pairs := make([]any, 129)
	for i := range pairs {
		pairs[i] = []any{i, []any{"uuid", queue}}
	}
	rows := inventory.Rows{}
	body, err := json.Marshal(map[string]any{
		"QoS": map[string]any{qos: map[string]any{"new": map[string]any{
			"type": "linux-htb", "queues": []any{"map", pairs},
			"other_config": []any{"map", []any{[]any{"max-rate", "10000"}, []any{"private-key", "synthetic-private-value"}}},
		}}},
		"Queue": map[string]any{queue: map[string]any{"new": map[string]any{"dscp": 0, "other_config": []any{"map", []any{}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := update(d, rows, body, true); err != nil {
		t.Fatal(err)
	}
	if err := validateRelations(rows); err != nil {
		t.Fatal("unreferenced native root tables broke core inventory", err)
	}
	observed := rows["QoS"][qos].Values
	if observed["queues"] != nil || len(observed["other_config"].(map[string]any)) != 1 || observed["other_config"].(map[string]any)["max-rate"] != "10000" {
		t.Fatal("large mapping or private options leaked", observed)
	}
	body, _ = json.Marshal(map[string]any{"QoS": map[string]any{qos: map[string]any{
		"old": map[string]any{"queues": []any{"map", pairs}},
		"new": map[string]any{"queues": []any{"map", []any{}}},
	}}})
	if err := update(d, rows, body, false); err != nil {
		t.Fatal("monitor could not recover from bounded optional state", err)
	}
	if mapping, ok := rows["QoS"][qos].Values["queues"].(map[string]any); !ok || len(mapping) != 0 {
		t.Fatal("known empty mapping lost after oversized state")
	}
	data, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	tables := raw["tables"].(map[string]any)
	tables["Queue"].(map[string]any)["isRoot"] = false
	data, _ = json.Marshal(raw)
	d, err = discover(data)
	if err != nil || d.public.QoSConfiguration || d.requests["QoS"] != nil || d.requests["Queue"] != nil || d.requests["Bridge"] == nil {
		t.Fatal("optional unrecognized QoS schema disrupted core inventory", err)
	}
}
