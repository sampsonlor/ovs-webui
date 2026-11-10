package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestQoSReadContractAndNativeRelationships(t *testing.T) {
	s, observation, decision, claims := fixture(t)
	observation.Schema.QoSConfiguration = true
	add := func(table string, values map[string]any) Binding {
		b := Binding{ManagementID: repository.NewID(), UUID: repository.NewID(), Table: table, State: "active"}
		if observation.Rows[table] == nil {
			observation.Rows[table] = map[string]Row{}
		}
		observation.Rows[table][b.UUID] = Row{UUID: b.UUID, Values: values}
		decision.Bindings[Key(table, b.UUID)] = b
		return b
	}
	queue := add("Queue", map[string]any{"dscp": []any{"0"}, "other_config": map[string]any{"max-rate": "9223372036854775807", "min-rate": "invalid", "private-key": "synthetic-private-option"}})
	qos := add("QoS", map[string]any{"type": "linux-htb", "queues": map[string]any{"4294967295": queue.UUID, "0": queue.UUID}, "other_config": map[string]any{}})
	add("QoS", map[string]any{"type": "linux-sfq", "queues": map[string]any{}, "other_config": map[string]any{}})
	add("QoS", map[string]any{"type": "linux-htb", "queues": map[string]any{"3": repository.NewID()}})
	add("Port", map[string]any{"name": "synthetic-aux"})
	for id, port := range observation.Rows["Port"] {
		port.Configuration = map[string]any{"qos": []any{qos.UUID}}
		observation.Rows["Port"][id] = port
	}
	s.install(observation, decision)
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string, query url.Values, c authn.Claims) map[string]any {
		t.Helper()
		op, params, _ := contract.Match("GET", "/api/v1"+path)
		if op == nil {
			t.Fatal("missing operation", path)
		}
		value, err := s.Read(context.Background(), op.ID, params, query, c)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = op.ValidateResponse(200, body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if bytes.Contains(body, []byte("synthetic-private-option")) {
			t.Fatal("private native options leaked")
		}
		return value.(map[string]any)
	}
	detail := read("/qos/"+qos.ManagementID, nil, claims)["native_qos"].(map[string]any)
	queues := detail["queues"].([]qosQueue)
	if detail["binding_state"] != "shared" || detail["port_count"] != 2 || detail["queue_count"] != 2 ||
		len(queues) != 2 || queues[0].ID != 0 || queues[1].ID != 4294967295 ||
		queues[0].Ref.ID != queue.ManagementID || queues[1].Ref.ID != queue.ManagementID ||
		queues[0].DSCP == nil || *queues[0].DSCP != 0 || queues[0].MinRate.State != "unknown" ||
		queues[0].MaxRate.BPS == nil || *queues[0].MaxRate.BPS != "9223372036854775807" ||
		detail["max_rate"].(qosRate).State != "unset" {
		t.Fatal("native Queue values or shared reference were lost", detail)
	}
	list := read("/qos", url.Values{"limit": {"1"}}, claims)
	next, hasNext := list["next_cursor"].(string)
	if len(list["items"].([]map[string]any)) != 1 || !hasNext {
		t.Fatal("QoS page lost bounded pagination", list)
	}
	read("/qos", url.Values{"limit": {"1"}, "cursor": {next}}, claims)
	empty := read("/qos", url.Values{"filter": {"linux-sfq"}}, claims)["items"].([]map[string]any)
	if len(empty) != 1 {
		t.Fatal("QoS discipline filter mismatch", empty)
	}
	unbound := empty[0]["native_qos"].(map[string]any)
	if unbound["binding_state"] != "unbound" || unbound["queue_count"] != 0 || unbound["max_rate"].(qosRate).State != "not-applicable" {
		t.Fatal("empty native relationships or unknown discipline synthesized", unbound)
	}
	for _, item := range read("/qos", url.Values{"filter": {"linux-htb"}}, claims)["items"].([]map[string]any) {
		if item["id"] != qos.ManagementID && item["native_qos"].(map[string]any)["queue_count"] != nil {
			t.Fatal("unresolved Queue presented as a complete mapping", item)
		}
	}
	withoutConfig := claims
	withoutConfig.Capabilities = []string{"inventory.read", "state.read"}
	_, err = s.Read(context.Background(), "readQos", map[string]string{"qos_id": qos.ManagementID}, nil, withoutConfig)
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Code != "CAPABILITY_DENIED" {
		t.Fatal("configuration permission missing from QoS gate", err)
	}
	for _, port := range observation.Rows["Port"] {
		if port.Values["name"] == "synthetic-aux" {
			continue
		}
		id := decision.Bindings[Key("Port", port.UUID)].ManagementID
		if bound := read("/ports/"+id, nil, claims)["qos_binding"].(map[string]any); bound["state"] != "bound" || bound["qos_ref"].(map[string]any)["id"] != qos.ManagementID {
			t.Fatal("Port does not link to immutable QoS identity", bound)
		}
		if hidden := read("/ports/"+id, nil, withoutConfig)["qos_binding"].(map[string]any); hidden["state"] != "withheld" || hidden["qos_ref"] != nil {
			t.Fatal("restricted Port leaks QoS identity", hidden)
		}
	}
}
