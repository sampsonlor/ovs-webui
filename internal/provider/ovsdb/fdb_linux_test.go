//go:build linux

package ovsdb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestFDBWireBoundsAndBridgeIdentity(t *testing.T) {
	req := inventory.FDBRequest{Bridge: inventory.Binding{UUID: repository.NewID()}, Name: "br-fdb"}
	at := time.Now()
	wrap := func(inner string) []byte {
		data, err := json.Marshal(map[string]any{"id": 1, "error": nil, "result": inner})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	message := func(id, name, rows string, total int) string {
		return fmt.Sprintf(`{"version":1,"bridge_uuid":%q,"bridge_name":%q,"total":%d,"rows":%s}`, id, name, total, rows)
	}
	// Printable native names with both JSON escape characters exercise even
	// the outer JSON-RPC frame's doubled escaping across a full 256-row sample.
	port := strings.Repeat("\\\"", 32)
	rows := make([]map[string]any, inventory.MaxFDBRows)
	for i := range rows {
		rows[i] = map[string]any{"mac": fmt.Sprintf("02:13:00:00:00:%02x", i), "vlan": 4095, "ofport": 65534, "port_name": port, "age_seconds": 4294967295}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := decodeFDBReply(wrap(message(req.Bridge.UUID, req.Name, string(encoded), len(rows)+1)), req, at)
	if err != nil || len(sample.Rows) != inventory.MaxFDBRows || sample.Total != inventory.MaxFDBRows+1 || sample.ObservedAt != at {
		t.Fatalf("full bounded reply: %d rows, total %d, %v", len(sample.Rows), sample.Total, err)
	}
	empty := message(req.Bridge.UUID, req.Name, "[]", 0)
	if sample, err := decodeFDBReply(wrap(empty), req, at); err != nil || sample.Rows == nil || sample.Total != 0 {
		t.Fatalf("qualified empty reply: %+v, %v", sample, err)
	}
	for name, inner := range map[string]string{
		"wrong bridge":                     message(repository.NewID(), req.Name, "[]", 0),
		"wrong name":                       message(req.Bridge.UUID, "br-other", "[]", 0),
		"null table":                       message(req.Bridge.UUID, req.Name, "null", 0),
		"undisclosed loss":                 message(req.Bridge.UUID, req.Name, "[]", 5),
		"duplicate count":                  strings.Replace(empty, `"total":0`, `"total":0,"total":0`, 1),
		"wrong case":                       strings.Replace(empty, `"rows"`, `"Rows"`, 1),
		"native error disguised as result": "unavailable",
		"absent age":                       message(req.Bridge.UUID, req.Name, `[{"mac":"02:13:00:00:00:00","vlan":101,"ofport":null,"port_name":null}]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeFDBReply(wrap(inner), req, at); err == nil {
				t.Fatal("unqualified reply was accepted")
			}
		})
	}
}
