package ovsdb

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestMirrorSchemaKeepsNativeReferencesDefaultsAndConstraints(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			if !d.public.MirrorConfiguration || len(d.public.MirrorDefaults) < 9 || len(d.public.GraphDefaults) != 2 || d.requests["Mirror"] == nil {
				t.Fatal("native Mirror schema, isolated defaults or monitor unqualified", d.public.MirrorConfiguration, d.public.MirrorDefaults)
			}
			cfg := d.public.MirrorDefaults
			if cfg["select_all"] != false || len(cfg["output_port"].([]any)) != 0 || len(cfg["output_vlan"].([]any)) != 0 || len(cfg["snaplen"].([]any)) != 0 || cfg["statistics"] != nil {
				t.Fatal("defaults inserted an output, truncation or observed counter", cfg)
			}
			_, filter := cfg["filter"]
			if filter != (version != "3.3.9") {
				t.Fatal("advanced column was guessed from a different schema")
			}
		})
	}
	data, err := os.ReadFile("testdata/vswitch-3.7.1.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"name-length", "all-immutable", "output-ephemeral", "source-strong", "selector-cap", "integer-range", "metadata-cap", "filter-length", "root-table", "unknown-column", "foreign-incoming-map"} {
		t.Run(scenario, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			table := raw["tables"].(map[string]any)["Mirror"].(map[string]any)
			columns := table["columns"].(map[string]any)
			column := func(name string) map[string]any { return columns[name].(map[string]any) }
			switch scenario {
			case "name-length":
				column("name")["type"] = map[string]any{"key": map[string]any{"type": "string", "minLength": 3}}
			case "all-immutable":
				column("select_all")["mutable"] = false
			case "output-ephemeral":
				column("output_port")["ephemeral"] = true
			case "source-strong":
				column("select_src_port")["type"].(map[string]any)["key"].(map[string]any)["refType"] = "strong"
			case "selector-cap":
				column("select_src_port")["type"].(map[string]any)["max"] = 1
			case "integer-range":
				column("output_vlan")["type"].(map[string]any)["key"].(map[string]any)["maxInteger"] = 100
			case "metadata-cap":
				column("external_ids")["type"].(map[string]any)["max"] = 3
			case "filter-length":
				column("filter")["type"] = map[string]any{"key": map[string]any{"type": "string", "minLength": 1}, "min": 0, "max": 1}
			case "root-table":
				table["isRoot"] = true
			case "unknown-column":
				columns["future_configuration"] = map[string]any{"type": "string"}
			case "foreign-incoming-map":
				raw["tables"].(map[string]any)["ForeignMirrorMap"] = map[string]any{"isRoot": true, "columns": map[string]any{"references": map[string]any{"type": map[string]any{"key": "string", "value": map[string]any{"type": "uuid", "refTable": "Mirror", "refType": "weak"}, "min": 0, "max": "unlimited"}}}}
			}
			encoded, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			d, err := discover(encoded)
			if err == nil && d.public.MirrorConfiguration {
				t.Fatal("restricted or unqualified native Mirror schema admitted writes")
			}
		})
	}
}

func TestMirrorMonitorKeepsNativeReferencesSeparateFromCoreParents(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d := schema(t, version)
			root, bridge, port, iface, session := repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID()
			uuid := func(id string) []any { return []any{"uuid", id} }
			empty := []any{"set", []any{}}
			initial := map[string]any{
				"Open_vSwitch": map[string]any{root: map[string]any{"new": map[string]any{"bridges": uuid(bridge)}}},
				"Bridge":       map[string]any{bridge: map[string]any{"new": map[string]any{"name": "br-span", "ports": uuid(port), "mirrors": uuid(session)}}},
				"Port":         map[string]any{port: map[string]any{"new": map[string]any{"name": "data", "interfaces": uuid(iface)}}},
				"Interface":    map[string]any{iface: map[string]any{"new": map[string]any{"name": "data", "type": "internal"}}},
				"Mirror":       map[string]any{session: map[string]any{"new": map[string]any{"name": "span", "select_all": false, "select_src_port": uuid(port), "select_dst_port": empty, "select_vlan": empty, "output_port": empty, "output_vlan": 200, "snaplen": empty}}},
			}
			rows := inventory.Rows{}
			blob, err := json.Marshal(initial)
			if err != nil {
				t.Fatal(err)
			}
			if err := update(d, rows, blob, true); err != nil {
				t.Fatal(err)
			}
			if err := validateRelations(rows); err != nil {
				t.Fatal("an attached native Mirror or weak selector was treated as an orphan/core parent", err)
			}
			// A native external configuration can share a session. Observation
			// must survive; the independent writer rejects ambiguous ownership.
			other := repository.NewID()
			rows["Bridge"][other] = inventory.Row{UUID: other, Values: map[string]any{"name": "br-other", "ports": []any{}, "mirrors": []any{session}}}
			rows["Open_vSwitch"][root].Values["bridges"] = []any{bridge, other}
			if err := validateRelations(rows); err != nil {
				t.Fatal("legal shared external Mirror made all inventory unavailable", err)
			}
			for _, column := range []string{"select_src_port", "select_dst_port", "output_port"} {
				old := rows["Mirror"][session].Values[column]
				rows["Mirror"][session].Values[column] = []any{repository.NewID()}
				if err := validateRelations(rows); err == nil || err.Error() != "OVSDB_REFERENCE_MISSING" {
					t.Fatal("dangling native Mirror Port reference accepted", column, err)
				}
				rows["Mirror"][session].Values[column] = old
			}
			delete(rows["Bridge"], other)
			rows["Open_vSwitch"][root].Values["bridges"] = []any{bridge}
			rows["Bridge"][bridge].Values["mirrors"] = []any{}
			if err := validateRelations(rows); err == nil || err.Error() != "OVSDB_ORPHAN_OBJECT" {
				t.Fatal("unattached non-root Mirror accepted", err)
			}
			delete(rows["Mirror"], session)
			if err := validateRelations(rows); err != nil {
				t.Fatal("complete native GC deletion rejected", err)
			}
			rows["Bridge"][bridge].Values["mirrors"] = []any{session}
			if err := validateRelations(rows); err == nil || err.Error() != "OVSDB_REFERENCE_MISSING" {
				t.Fatal("dangling strong Bridge Mirror reference accepted", err)
			}
		})
	}
}
