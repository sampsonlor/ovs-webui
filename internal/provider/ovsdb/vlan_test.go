package ovsdb

import (
	"encoding/json"
	"slices"
	"testing"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

func TestVLANValidationUsesDiscoveredSchemaConstraints(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			found := 0
			for _, table := range d.public.Tables {
				if table.Name == "Port" {
					for _, col := range table.Columns {
						if col.VLANCompatible {
							found++
							if col.Name == "vlan_mode" && (!slices.Contains(col.VLANModes, "native-tagged") || !slices.Contains(col.VLANModes, "native-untagged")) {
								t.Fatal("native mode support lost")
							}
						}
					}
				}
			}
			if found != 4 {
				t.Fatal("expected four supported typed VLAN columns", found)
			}
		})
	}
	for _, raw := range []string{
		`{"type":{"key":{"type":"integer","minInteger":100,"maxInteger":4095},"min":0,"max":1}}`,
		`{"type":{"key":{"type":"integer","minInteger":0,"maxInteger":4095},"min":0,"max":1},"mutable":false}`,
		`{"type":{"key":{"type":"string"},"min":0,"max":1}}`,
	} {
		var col native.ColumnSchema
		if err := json.Unmarshal([]byte(raw), &col); err != nil {
			t.Fatal(err)
		}
		if ok, _ := vlanConstraint("tag", &col); ok {
			t.Fatal("unsupported constraint allowed", raw)
		}
	}
}
