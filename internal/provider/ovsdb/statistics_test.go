package ovsdb

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

const (
	statisticsRootID      = "00000000-0000-4000-8000-000000000001"
	statisticsInterfaceID = "00000000-0000-4000-8000-000000000002"
)

func TestStatisticsSchemaIsIndependentOfMonitorAndClosedToChangedTypes(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		data, err := os.ReadFile("testdata/vswitch-" + version + ".ovsschema")
		if err != nil {
			t.Fatal(err)
		}
		d := schema(t, version)
		if !statisticsSchemaSupported(d, data) || monitoredColumn(d, "Interface", "statistics") {
			t.Fatal("statistics schema or independent-monitor boundary changed", version)
		}
	}
	data, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"value", "key", "ephemeral", "constraints"} {
		t.Run(mutation, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			column := raw["tables"].(map[string]any)["Interface"].(map[string]any)["columns"].(map[string]any)["statistics"].(map[string]any)
			typ := column["type"].(map[string]any)
			switch mutation {
			case "ephemeral":
				column["ephemeral"] = false
			case "constraints":
				typ["value"] = map[string]any{"type": "integer", "maxInteger": 100}
			default:
				typ[mutation] = "string"
				if mutation == "key" {
					typ[mutation] = "integer"
				}
			}
			body, _ := json.Marshal(raw)
			d, err := discover(body)
			if err != nil {
				t.Fatal("optional statistics shape disrupted core discovery", err)
			}
			if statisticsSchemaSupported(d, body) {
				t.Fatal("changed statistics type qualified", mutation)
			}
		})
	}
}

func TestStatisticsCounterProjectionPreservesPrecisionAndUnknowns(t *testing.T) {
	raw := json.RawMessage(`["map",[["rx_bytes",9223372036854775807],["tx_bytes",0],["rx_packets",-1],["tx_packets",1.5],["rx_errors","7"],["tx_errors",9223372036854775808],["vendor-counter",12]]]`)
	counters := decodeStatisticsCounters(raw)
	if len(counters) != len(statisticsCounters) || counters["rx_bytes"] == nil || *counters["rx_bytes"] != "9223372036854775807" ||
		counters["tx_bytes"] == nil || *counters["tx_bytes"] != "0" {
		t.Fatal("native counter precision or valid zero lost", counters)
	}
	for _, name := range []string{"rx_packets", "tx_packets", "rx_errors", "tx_errors", "rx_dropped", "tx_dropped"} {
		if counters[name] != nil {
			t.Fatal("missing, negative or invalid counter became known", name)
		}
	}
	if _, exposed := counters["vendor-counter"]; exposed {
		t.Fatal("vendor counter escaped the fixed public projection")
	}
	pairs := []any{[]any{"rx_bytes", 9}}
	for index := 0; index < 129; index++ {
		pairs = append(pairs, []any{"custom-" + strconv.Itoa(index), index})
	}
	body, _ := json.Marshal([]any{"map", pairs})
	if got := decodeStatisticsCounters(body)["rx_bytes"]; got == nil || *got != "9" {
		t.Fatal("legitimate custom counters exhausted the monitor's smaller map budget")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`["map",[["rx_bytes",1],["rx_bytes",2]]]`),
		json.RawMessage(`["map",[["rx_bytes",1],["bad"]]]`),
		json.RawMessage(`["set",[]]`),
	} {
		for _, value := range decodeStatisticsCounters(invalid) {
			if value != nil {
				t.Fatal("malformed native map supplied a known counter")
			}
		}
	}
	for index := 129; index < 513; index++ {
		pairs = append(pairs, []any{"custom-" + strconv.Itoa(index), index})
	}
	body, _ = json.Marshal([]any{"map", pairs})
	for _, value := range decodeStatisticsCounters(body) {
		if value != nil {
			t.Fatal("oversized native map did not degrade only this row")
		}
	}
}

func statisticsTestResult() []any {
	return []any{
		map[string]any{"rows": []any{map[string]any{"_uuid": uuidValue(statisticsRootID)}}},
		map[string]any{"rows": []any{map[string]any{"_uuid": uuidValue(statisticsInterfaceID), "statistics": []any{"map", []any{[]any{"rx_bytes", 77}}}}}},
	}
}

func TestStatisticsTransactionAndResultBindExactSnapshot(t *testing.T) {
	req := inventory.StatRequest{Root: statisticsRootID, Interfaces: []string{statisticsInterfaceID}}
	body, _ := json.Marshal(statisticsTestResult())
	result, err := decodeStatisticsResult(body, req)
	if err != nil || result[statisticsInterfaceID]["rx_bytes"] == nil || *result[statisticsInterfaceID]["rx_bytes"] != "77" {
		t.Fatal("native counter snapshot did not bind to the requested Interface", result, err)
	}
	ops := statisticsOperations()
	if len(ops) != 3 || ops[0] != "Open_vSwitch" {
		t.Fatal("unexpected statistics transaction")
	}
	for _, raw := range ops[1:] {
		if raw.(map[string]any)["op"] != "select" {
			t.Fatal("statistics observer may only send read-only selects")
		}
	}
	changed := func(change func([]any)) []byte {
		result := statisticsTestResult()
		change(result)
		body, _ := json.Marshal(result)
		return body
	}
	for name, body := range map[string][]byte{
		"root-replaced": changed(func(r []any) {
			r[0] = map[string]any{"rows": []any{map[string]any{"_uuid": uuidValue(statisticsInterfaceID)}}}
		}),
		"interface-missing": changed(func(r []any) { r[1] = map[string]any{"rows": []any{}} }),
		"duplicate-interface": changed(func(r []any) {
			result := r[1].(map[string]any)
			rows := result["rows"].([]any)
			result["rows"] = append(rows, rows[0])
		}),
		"native-error": changed(func(r []any) { r[1] = map[string]any{"error": "permission error", "details": "private"} }),
	} {
		if result, err := decodeStatisticsResult(body, req); err == nil || result != nil {
			t.Fatal("invalid native read leaked a partial sample", name, result)
		}
	}
	req.Interfaces = nil
	result, err = decodeStatisticsResult(body, req)
	if err != nil || !reflect.DeepEqual(result, map[string]map[string]*string{}) {
		t.Fatal("unrequested Interface values escaped the projection", result, err)
	}
}
