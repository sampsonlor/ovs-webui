package ovsdb

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestQoSWriteSchemaAndIntegerQueueReference(t *testing.T) {
	queue := "00000000-0000-4000-8000-000000000001"
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		d := schema(t, version)
		if !d.public.QoSRateWrite || !d.public.QoSCreate {
			t.Fatalf("QoS schema %s should qualify", version)
		}
		value, err := nativeValue(map[string]any{"7": queue}, d.native.Tables["QoS"].Columns["queues"])
		data, _ := json.Marshal(value)
		if err != nil || string(data) != `["map",[[7,["uuid","`+queue+`"]]]]` {
			t.Fatalf("Queue keys are native integers: %s %v", data, err)
		}
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
	tables["Vendor"] = map[string]any{"isRoot": true, "columns": map[string]any{"shared_queue": map[string]any{
		"type": map[string]any{"key": map[string]any{"type": "uuid", "refTable": "Queue", "refType": "strong"}, "min": 0, "max": 1},
	}}}
	body, _ := json.Marshal(raw)
	d, err := discover(body)
	if err != nil || d.public.QoSRateWrite || d.public.QoSCreate || !d.public.QoSConfiguration {
		t.Fatal("write admission ignored an unexpected Queue reference", err)
	}
	delete(tables, "Vendor")
	columns := tables["Queue"].(map[string]any)["columns"].(map[string]any)
	columns["vendor_queue_settings"] = map[string]any{"type": "string"}
	body, _ = json.Marshal(raw)
	d, err = discover(body)
	if err != nil || d.public.QoSCreate || !d.public.QoSRateWrite {
		t.Fatal("untracked Queue data did not block create compensation independently", err)
	}
	if boundedPrivateQoS("QoS", map[string]any{"opaque": strings.Repeat("x", 4096)}) != nil {
		t.Fatal("unbounded optional raw settings")
	}
}

func TestQoSMonitorKeepsUnboundRootsAndBoundsOptionalMaps(t *testing.T) {
	d := schema(t, "3.3.9")
	queue := "00000000-0000-4000-8000-000000000001"
	qos := "00000000-0000-4000-8000-000000000002"
	pairs := make([]any, 129)
	marker := strings.Repeat("a", 64)
	for i := range pairs {
		pairs[i] = []any{i, []any{"uuid", queue}}
	}
	rows := inventory.Rows{}
	body, err := json.Marshal(map[string]any{
		"QoS": map[string]any{qos: map[string]any{"new": map[string]any{
			"type": "linux-htb", "queues": []any{"map", pairs},
			"other_config": []any{"map", []any{[]any{"max-rate", "10000"}, []any{"private-key", "synthetic-private-value"}}},
			"external_ids": []any{"map", []any{[]any{candidate.BridgeCreationMarker, marker}, []any{"private-label", "synthetic-private-value"}}},
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
	if rows["QoS"][qos].Configuration["other_config"].(map[string]any)["private-key"] != "synthetic-private-value" {
		t.Fatal("private native map lost the opaque setting")
	}
	labels := observed["external_ids"].(map[string]any)
	if len(labels) != 1 || labels[candidate.BridgeCreationMarker] != marker ||
		rows["QoS"][qos].Configuration["external_ids"].(map[string]any)["private-label"] != "synthetic-private-value" {
		t.Fatal("creation evidence was dropped or private labels were exposed")
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
