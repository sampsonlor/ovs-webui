package ovsdb

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestInterfaceConfigurationActualSchemasAndMonitor(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			found := 0
			for _, table := range d.public.Tables {
				if table.Name != "Interface" {
					continue
				}
				for _, c := range table.Columns {
					if slices.Contains(inventory.InterfaceConfigurationColumns, c.Name) {
						if !c.Monitored || !c.InterfaceConfigCompatible {
							t.Fatal(c)
						}
						found++
					}
				}
			}
			if found != 5 {
				t.Fatal("missing actual schema observations", found)
			}
			rows := inventory.Rows{}
			id := "00000000-0000-4000-8000-000000000001"
			wire := []byte(`{"Interface":{"00000000-0000-4000-8000-000000000001":{"new":{"name":"synthetic-config","ofport_request":["set",[]],"ingress_policing_rate":0,"ingress_policing_burst":0,"ingress_policing_kpkts_rate":0,"ingress_policing_kpkts_burst":9223372036854775807}}}}`)
			if err := update(d, rows, wire, true); err != nil {
				t.Fatal(err)
			}
			values := rows["Interface"][id].Values
			if inventory.Digest(values["ofport_request"]) != inventory.Digest([]any{}) || values["ingress_policing_rate"] != "0" || values["ingress_policing_kpkts_burst"] != "9223372036854775807" {
				t.Fatal("empty, zero or exact 64-bit value lost", values)
			}
			wire = []byte(`{"Interface":{"00000000-0000-4000-8000-000000000001":{"old":{"ofport_request":["set",[]],"ingress_policing_rate":0},"new":{"ofport_request":65279,"ingress_policing_rate":1000}}}}`)
			if err := update(d, rows, wire, false); err != nil {
				t.Fatal(err)
			}
			values = rows["Interface"][id].Values
			if inventory.Digest(values["ofport_request"]) != inventory.Digest([]any{"65279"}) || values["ingress_policing_rate"] != "1000" {
				t.Fatal("incremental configuration not observed", values)
			}
		})
	}
}

func TestInterfaceConfigurationUnknownSchemasDoNotInventSemantics(t *testing.T) {
	data, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	columns := raw["tables"].(map[string]any)["Interface"].(map[string]any)["columns"].(map[string]any)
	delete(columns, "ingress_policing_kpkts_rate")
	columns["ofport_request"].(map[string]any)["type"] = "integer"
	columns["ingress_policing_burst"].(map[string]any)["type"] = "string"
	raw["version"] = "999.0.0"
	data, _ = json.Marshal(raw)
	d, err := discover(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range d.public.Tables {
		if table.Name != "Interface" {
			continue
		}
		for _, c := range table.Columns {
			if c.Name == "ingress_policing_kpkts_rate" {
				t.Fatal("guessed missing column")
			}
			if (c.Name == "ofport_request" || c.Name == "ingress_policing_burst") && (c.InterfaceConfigCompatible || c.Monitored) {
				t.Fatal("guessed changed shape", c)
			}
		}
	}
	if err := update(d, inventory.Rows{}, []byte(`{"Interface":{"00000000-0000-4000-8000-000000000001":{"new":{"name":"synthetic-unrequested","ingress_policing_burst":"synthetic-unpublished"}}}}`), true); err == nil {
		t.Fatal("unrecognized column entered the monitor cache")
	}
}
