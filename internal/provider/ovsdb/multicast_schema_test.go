package ovsdb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

func TestMulticastDiscoveryAndCompilerRequireUnconstrainedParameterAndMarkerMaps(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		data, err := os.ReadFile(filepath.Join("testdata", "vswitch-"+version+".ovsschema"))
		if err != nil {
			t.Fatal(err)
		}
		d, err := discover(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"other_config", "external_ids"} {
			if !d.multicastMaps[name] {
				t.Fatal("qualified native map rejected", version, name)
			}
			found := false
			for _, table := range d.public.Tables {
				if table.Name == "Bridge" {
					for _, column := range table.Columns {
						if column.Name == name {
							found = column.MulticastCompatible
						}
					}
				}
			}
			if !found {
				t.Fatal("discovery and compiler differ", version, name)
			}
		}
	}
	for _, wire := range []string{
		`{"type":{"key":"string","value":"integer","min":0,"max":"unlimited"}}`,
		`{"type":{"key":{"type":"string","minLength":1},"value":"string","min":0,"max":"unlimited"}}`,
		`{"type":{"key":"string","value":{"type":"string","maxLength":3},"min":0,"max":"unlimited"}}`,
		`{"type":{"key":{"type":"string","enum":["set",["other-key"]]},"value":"string","min":0,"max":"unlimited"}}`,
		`{"type":{"key":"string","value":"string","min":0,"max":3}}`,
		`{"type":{"key":"string","value":"string","min":0,"max":"unlimited"},"mutable":false}`,
		`{"type":{"key":"string","value":"string","min":0,"max":"unlimited"},"ephemeral":true}`,
	} {
		var column native.ColumnSchema
		if json.Unmarshal([]byte(wire), &column) != nil {
			t.Fatal("invalid schema test fixture")
		}
		var raw map[string]any
		decoder := json.NewDecoder(strings.NewReader(wire))
		decoder.UseNumber()
		if decoder.Decode(&raw) != nil {
			t.Fatal("invalid raw schema fixture")
		}
		if multicastMapConstraint(&column, raw["type"]) {
			t.Fatal("unqualified native map became writable", wire)
		}
	}
	if multicastMapConstraint(nil, nil) {
		t.Fatal("missing map column became writable")
	}
}
