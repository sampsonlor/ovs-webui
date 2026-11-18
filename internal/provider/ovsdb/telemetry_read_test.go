package ovsdb

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestBridgeTelemetryDiscoveryQualifiesEachProtocolAndField(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			for protocol, table := range telemetryTables {
				if !d.public.Telemetry[protocol] || !monitoredColumn(d, "Bridge", protocol) || d.requests[table] == nil ||
					!monitoredColumn(d, table, "targets") || monitoredColumn(d, table, "external_ids") || privateInventoryColumn(d, table, "external_ids") {
					t.Fatalf("%s exporter projection did not qualify safely", protocol)
				}
				for name := range telemetryFields[table] {
					if !monitoredColumn(d, table, name) {
						t.Fatalf("%s.%s should be monitored", table, name)
					}
				}
				request := d.requests[table].([]any)[0].(map[string]any)
				columns := request["columns"].([]string)
				allowed := 1 + len(telemetryFields[table])
				if table == "IPFIX" {
					allowed++
					if !slices.Contains(columns, "other_config") {
						t.Fatal("IPFIX sampling options were not selected")
					}
				}
				if len(columns) != allowed {
					t.Fatalf("unexpected %s monitor columns: %v", table, columns)
				}
			}
			if d.requests["Flow_Sample_Collector_Set"] != nil {
				t.Fatal("per-flow IPFIX is outside the Bridge-scoped view")
			}
		})
	}

	data, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	tables := raw["tables"].(map[string]any)
	cols := func(table string) map[string]any {
		return tables[table].(map[string]any)["columns"].(map[string]any)
	}
	cols("Bridge")["sflow"].(map[string]any)["type"].(map[string]any)["key"].(map[string]any)["refType"] = "weak"
	cols("NetFlow")["targets"].(map[string]any)["type"].(map[string]any)["min"] = 0
	cols("IPFIX")["obs_point_id"].(map[string]any)["type"].(map[string]any)["key"].(map[string]any)["maxInteger"] = 255
	data, _ = json.Marshal(raw)
	d, err := discover(data)
	if err != nil {
		t.Fatal("unrecognized exporter shapes disrupted core inventory", err)
	}
	if d.public.Telemetry["sflow"] || d.public.Telemetry["netflow"] || d.requests["sFlow"] != nil || d.requests["NetFlow"] != nil ||
		monitoredColumn(d, "Bridge", "sflow") || monitoredColumn(d, "Bridge", "netflow") {
		t.Fatal("incompatible exporter references or targets were accepted")
	}
	if !d.public.Telemetry["ipfix"] || !monitoredColumn(d, "IPFIX", "targets") || monitoredColumn(d, "IPFIX", "obs_point_id") {
		t.Fatal("one optional field should not qualify a changed shape or disable the exporter")
	}
	if !privateInventoryColumn(d, "Bridge", "sflow") {
		t.Fatal("read qualification must preserve the private Bridge configuration fence")
	}
}

func TestBridgeTelemetryMonitorPreservesNativeShapesAndBoundsText(t *testing.T) {
	d := schema(t, "3.3.9")
	root := "00000000-0000-4000-8000-000000000001"
	bridge := "00000000-0000-4000-8000-000000000002"
	sflow := "00000000-0000-4000-8000-000000000003"
	netflow := "00000000-0000-4000-8000-000000000004"
	ipfix := "00000000-0000-4000-8000-000000000005"
	targets := make([]any, 33)
	for i := range targets {
		targets[i] = fmt.Sprintf("192.0.2.%d:6343", i+1)
	}
	empty := []any{"set", []any{}}
	changes := map[string]any{
		"Open_vSwitch": map[string]any{root: map[string]any{"new": map[string]any{"bridges": []any{"uuid", bridge}}}},
		"Bridge": map[string]any{bridge: map[string]any{"new": map[string]any{
			"name": "telemetry-fixture", "ports": empty, "sflow": []any{"uuid", sflow}, "netflow": []any{"uuid", netflow}, "ipfix": empty,
		}}},
		"sFlow": map[string]any{sflow: map[string]any{"new": map[string]any{
			"targets": []any{"set", targets}, "agent": "eth0", "header": empty, "sampling": int64(9223372036854775807),
		}}},
		"NetFlow": map[string]any{netflow: map[string]any{"new": map[string]any{
			"targets": "user:synthetic-secret@192.0.2.5:2055", "engine_id": 256, "active_timeout": -1, "add_id_to_interface": false,
		}}},
		"IPFIX": map[string]any{ipfix: map[string]any{"new": map[string]any{
			"targets": empty, "sampling": 1, "obs_domain_id": uint32(4294967295),
			"other_config": []any{"map", []any{
				[]any{"enable-input-sampling", "true"}, []any{"enable-output-sampling", "synthetic-secret"},
				[]any{"virtual_obs_id", "synthetic-secret"}, []any{"unchecked-option", "synthetic-secret"},
			}},
		}}},
	}
	body, _ := json.Marshal(changes)
	rows := inventory.Rows{}
	if err := update(d, rows, body, true); err != nil {
		t.Fatal(err)
	}
	if err := validateRelations(rows); err != nil {
		t.Fatal("legitimate per-flow-only IPFIX record disrupted Bridge inventory", err)
	}
	if ref := rows["Bridge"][bridge].Values["sflow"]; !slices.Equal(ref.([]any), []any{sflow}) {
		t.Fatal("Bridge reference lost", ref)
	}
	for _, edge := range []struct {
		table, id string
	}{{"sFlow", sflow}, {"NetFlow", netflow}} {
		if rows[edge.table][edge.id].Values["targets"] != nil {
			t.Fatalf("unsafe or unbounded %s targets leaked", edge.table)
		}
	}
	nf := rows["NetFlow"][netflow].Values
	if nf["engine_id"] != nil || nf["active_timeout"] != "-1" || nf["add_id_to_interface"] != false {
		t.Fatal("NetFlow native bounds or explicit values lost", nf)
	}
	sf := rows["sFlow"][sflow].Values
	if !slices.Equal(sf["sampling"].([]any), []any{"9223372036854775807"}) || len(sf["header"].([]any)) != 0 {
		t.Fatal("native precision or unset field lost", sf)
	}
	ip := rows["IPFIX"][ipfix].Values
	opts := ip["other_config"].(map[string]any)
	malformed, present := opts["enable-output-sampling"]
	if len(ip["targets"].([]any)) != 0 || !slices.Equal(ip["obs_domain_id"].([]any), []any{"4294967295"}) ||
		len(opts) != 2 || opts["enable-input-sampling"] != "true" || !present || malformed != nil {
		t.Fatal("filtered IPFIX settings, explicitly empty targets, or unsigned precision lost", ip)
	}
	body, _ = json.Marshal(rows)
	if strings.Contains(string(body), "synthetic-secret") {
		t.Fatal("raw exporter values leaked")
	}
	changes = map[string]any{"sFlow": map[string]any{sflow: map[string]any{
		"old": map[string]any{"targets": []any{"set", targets}},
		"new": map[string]any{"targets": []any{"set", []any{"192.0.2.5", "[2001:db8::7]:6343"}}},
	}}}
	body, _ = json.Marshal(changes)
	if err := update(d, rows, body, false); err != nil {
		t.Fatal("monitor could not recover from unknown telemetry targets", err)
	}
	if !slices.Equal(rows["sFlow"][sflow].Values["targets"].([]any), []any{"192.0.2.5", "[2001:db8::7]:6343"}) {
		t.Fatal("known numeric addresses not retained")
	}
}
