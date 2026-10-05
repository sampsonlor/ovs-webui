package ovsdb

import (
	"encoding/json"
	"testing"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

func TestInterfacePatchActualSchemasAndUnknownShapes(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			for table, names := range map[string][]string{"Interface": {"type", "options"}, "Bridge": {"datapath_type"}} {
				for _, name := range names {
					if !patchColumnConstraint(table, name, d.native.Tables[table].Columns[name]) {
						t.Fatalf("%s.%s not recognized", table, name)
					}
					found := false
					for _, tt := range d.public.Tables {
						if tt.Name == table {
							for _, c := range tt.Columns {
								if c.Name == name {
									found = c.PatchCompatible
								}
							}
						}
					}
					if !found {
						t.Fatal("private compatibility proof missing")
					}
				}
			}
		})
	}
	for _, raw := range []string{`{"type":"integer"}`, `{"type":{"key":"string","min":0,"max":1}}`, `{"type":{"key":"string","value":"integer","min":0,"max":"unlimited"}}`, `{"type":{"key":"string","value":"string","min":0,"max":1}}`} {
		var c native.ColumnSchema
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if patchColumnConstraint("Interface", "type", &c) || patchColumnConstraint("Interface", "options", &c) || patchColumnConstraint("Bridge", "datapath_type", &c) {
			t.Fatal("unknown schema interpreted", raw)
		}
	}
	if patchColumnConstraint("Interface", "other_config", nil) {
		t.Fatal("nil schema accepted")
	}
}
