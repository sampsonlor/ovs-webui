package ovsdb

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Native transaction compilation is platform independent. Keep its complete
// persistent image fixture available to local tests as well as Linux gates.
func treeFenceFixture(t *testing.T, version string) (discovered, inventory.ExecutionView) {
	t.Helper()
	d, view, _ := executionFixture(t, version)
	for table, rows := range view.Observation.Rows {
		for id, row := range rows {
			row.Configuration = map[string]any{}
			for name, c := range d.native.Tables[table].Columns {
				if !graphConfigurationColumn(table, name, c) {
					continue
				}
				value, known := row.Values[name]
				if !known {
					raw, err := emptyNative(c)
					if err != nil {
						t.Fatal(err)
					}
					blob, err := json.Marshal(raw)
					if err != nil {
						t.Fatal(err)
					}
					var decoded any
					decoder := json.NewDecoder(bytes.NewReader(blob))
					decoder.UseNumber()
					if err := decoder.Decode(&decoded); err != nil {
						t.Fatal(err)
					}
					value, err = normalize(decoded, c)
					if err != nil {
						t.Fatal(err)
					}
				}
				row.Configuration[name] = value
			}
			if table == "Open_vSwitch" {
				row.Values["next_cfg"], row.Values["cur_cfg"] = "10", "10"
			}
			view.Observation.Rows[table][id] = row
		}
	}
	return d, view
}
