package ovsdb

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func schema(t *testing.T, version string) discovered {
	t.Helper()
	b, err := os.ReadFile("testdata/vswitch-" + version + ".ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	d, err := discover(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestActualSchemaVersionsAndMetadata(t *testing.T) {
	for _, v := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(v, func(t *testing.T) {
			d := schema(t, v)
			if len(d.requests) != 4 {
				t.Fatal("monitor coverage")
			}
			found := false
			for _, table := range d.public.Tables {
				if table.Name == "Port" {
					for _, c := range table.Columns {
						if c.Name == "interfaces" {
							found = len(c.References) == 1 && c.References[0].Table == "Interface" && c.References[0].Strength == "strong" && c.Mutable && c.Monitored
						}
					}
					if len(table.Indexes) != 1 || table.Indexes[0][0] != "name" {
						t.Fatal("indexes missing")
					}
				}
			}
			if !found {
				t.Fatal("column reference/mutability discovery")
			}
		})
	}
}
func TestDiscoveryUsesColumnsNotVersion(t *testing.T) {
	b, _ := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	raw["version"] = "999.0.0"
	tables := raw["tables"].(map[string]any)
	port := tables["Port"].(map[string]any)["columns"].(map[string]any)
	delete(port, "cvlans")
	b, _ = json.Marshal(raw)
	d, err := discover(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range d.public.Tables {
		if table.Name == "Port" {
			for _, c := range table.Columns {
				if c.Name == "cvlans" {
					t.Fatal("guessed missing capability")
				}
			}
		}
	}
	delete(port, "interfaces")
	b, _ = json.Marshal(raw)
	if _, err = discover(b); err == nil {
		t.Fatal("missing core references accepted")
	}
}
func TestNativeIntegersSetsAndSecretOptions(t *testing.T) {
	d := schema(t, "3.3.9")
	rows := inventory.Rows{}
	data := []byte(`{"Open_vSwitch":{"00000000-0000-4000-8000-000000000001":{"new":{"bridges":["set",[]],"next_cfg":9223372036854775807,"cur_cfg":-9223372036854775808}}},"Interface":{"00000000-0000-4000-8000-000000000002":{"new":{"name":"synthetic","type":"dummy","options":["map",[["psk","never-publish"],["peer","synthetic-peer"]]]}}}}`)
	if err := update(d, rows, data, true); err != nil {
		t.Fatal(err)
	}
	root := rows["Open_vSwitch"]["00000000-0000-4000-8000-000000000001"]
	if root.Values["next_cfg"] != "9223372036854775807" || root.Values["cur_cfg"] != "-9223372036854775808" {
		t.Fatal("64-bit precision lost")
	}
	b, _ := json.Marshal(rows)
	if bytes.Contains(b, []byte("never-publish")) || !bytes.Contains(b, []byte("synthetic-peer")) {
		t.Fatal("options allowlist")
	}
	for _, bad := range []string{`["set",[1,1]]`, `["set",[1.5]]`, `9223372036854775808`} {
		var v any
		dec := json.NewDecoder(strings.NewReader(bad))
		dec.UseNumber()
		_ = dec.Decode(&v)
		if _, err := normalize(v, d.native.Tables["Port"].Columns["tag"]); err == nil {
			t.Fatal("invalid native value accepted", bad)
		}
	}
}
func TestMonitorUpdateAtomicRelationsAndDeletion(t *testing.T) {
	d := schema(t, "3.3.9")
	rows := inventory.Rows{}
	root := "00000000-0000-4000-8000-000000000001"
	bridge := "00000000-0000-4000-8000-000000000002"
	initial := map[string]any{"Open_vSwitch": map[string]any{root: map[string]any{"new": map[string]any{"bridges": []any{"uuid", bridge}}}}, "Bridge": map[string]any{bridge: map[string]any{"new": map[string]any{"name": "br-synthetic", "ports": []any{"set", []any{}}}}}}
	b, _ := json.Marshal(initial)
	if err := update(d, rows, b, true); err != nil {
		t.Fatal(err)
	}
	if err := validateRelations(rows); err != nil {
		t.Fatal(err)
	}
	// A complete update deletes the bridge and removes its root reference.
	changes := map[string]any{"Open_vSwitch": map[string]any{root: map[string]any{"old": map[string]any{"bridges": []any{"uuid", bridge}}, "new": map[string]any{"bridges": []any{"set", []any{}}}}}, "Bridge": map[string]any{bridge: map[string]any{"old": map[string]any{"name": "br-synthetic"}}}}
	b, _ = json.Marshal(changes)
	if err := update(d, rows, b, false); err != nil {
		t.Fatal(err)
	}
	if err := validateRelations(rows); err != nil {
		t.Fatal(err)
	}
	if len(rows["Bridge"]) != 0 {
		t.Fatal("deletion ignored")
	}
	if err := update(d, rows, b, false); err == nil {
		t.Fatal("duplicate deletion accepted")
	}
}
func TestBoundedFramesAndDuplicateKeys(t *testing.T) {
	good := `{"method":"echo","params":["escaped } bracket"],"id":1}`
	reader := bufio.NewReader(strings.NewReader(good + good))
	for i := 0; i < 2; i++ {
		b, err := frame(reader)
		if err != nil || string(b) != good {
			t.Fatal("stream framing", err)
		}
	}
	for _, data := range []string{`{"id":1,"id":2}`, `{"result":{"a":1,"a":2}}`, `{"a":1} true`, `{"a":` + strings.Repeat("[", 34) + strings.Repeat("]", 34) + `}`, `{"a":"` + strings.Repeat("x", 65537) + `"}`} {
		if validateJSON([]byte(data)) == nil {
			t.Fatal("accepted malformed or unbounded JSON")
		}
	}
	if _, err := frame(bufio.NewReader(strings.NewReader(`{"a":"` + strings.Repeat("x", maxFrame) + `"}`))); err == nil {
		t.Fatal("frame limit ignored")
	}
}
func TestMonitorRejectsMismatchedBeforeImage(t *testing.T) {
	d := schema(t, "3.3.9")
	rows := inventory.Rows{"Port": {"00000000-0000-4000-8000-000000000001": {UUID: "00000000-0000-4000-8000-000000000001", Values: map[string]any{"name": "current"}}}}
	if err := update(d, rows, []byte(`{"Port":{"00000000-0000-4000-8000-000000000001":{"old":{"name":"unobserved"},"new":{"name":"changed"}}}}`), false); err == nil {
		t.Fatal("lost update silently accepted")
	}
	for _, method := range []string{"transact", "lock", "steal", "convert", "monitor_cond_change"} {
		if request(nil, 1, method, []any{}) == nil {
			t.Fatal("write/unknown method allowed")
		}
	}
	if value := sanitize("Interface", "error", []any{"synthetic-password-must-not-leak"}); inventory.Digest(value) != inventory.Digest([]any{"provider-reported-error"}) {
		t.Fatal("free-form diagnostic leaked")
	}
}
func FuzzBoundedMonitorJSON(f *testing.F) {
	f.Add([]byte(`{"method":"echo","params":[],"id":1}`))
	f.Add([]byte(`{"id":1,"id":2}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 65536 {
			return
		}
		_ = validateJSON(b)
	})
}
