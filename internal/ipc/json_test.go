package ipc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
)

func TestStrictSecurityCommandDelegatesOnlyRawPayload(t *testing.T) {
	input := authn.Command{Method: "PATCH", URI: "/api/v1/users/synthetic", Payload: json.RawMessage(`{"disabled":true,"role_ids":[]}`)}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded authn.Command
	if err = DecodeStrict(encoded, &decoded); err != nil || string(decoded.Payload) != string(input.Payload) {
		t.Fatalf("raw command payload did not round trip: %v", err)
	}
	for _, raw := range []string{
		`{"method":"PATCH","actor":"admin","payload":{}}`,
		`{"Method":"PATCH","payload":{}}`,
		`{"payload":{"disabled":true,"disabled":false}}`,
		`{"payload":{"name":"\ud800"}}`,
		`{"payload":{"nested":` + strings.Repeat("[", MaxJSONDepth+1) + `0` + strings.Repeat("]", MaxJSONDepth+1) + `}}`,
	} {
		if DecodeStrict([]byte(raw), &decoded) == nil {
			t.Fatal("raw payload bypassed envelope or recursive JSON checks")
		}
	}
	// An arbitrary interface is not a delegated operation payload.
	var untyped struct {
		Payload any `json:"payload"`
	}
	if DecodeStrict([]byte(`{"payload":{"actor":"admin"}}`), &untyped) == nil {
		t.Fatal("untyped payload bypassed exact field matching")
	}
}

func TestStrictJSONAmbiguityAndBounds(t *testing.T) {
	type request struct {
		Name   string `json:"name"`
		Nested struct {
			Enabled bool `json:"enabled"`
		} `json:"nested"`
		Values []int `json:"values"`
	}
	for _, data := range []string{
		`{"name":"a","name":"b"}`, `{"name":"a","\u006eame":"b"}`, `{"Name":"a"}`,
		`{"nested":{"Enabled":true}}`, `{"nested":{"enabled":true,"enabled":false}}`,
		`{"role":"Administrator"}`, `{} {}`, `null`, `[]`, `{"name":"\ud800"}`, `{"name":"\udc00"}`,
		"{\"name\":\"\xff\"}", `{"values":[` + strings.Repeat(`1,`, MaxJSONArray) + `1]}`,
		`{"name":"` + strings.Repeat("x", MaxJSONString+1) + `"}`, strings.Repeat(" ", MaxBodyBytes) + `{}`,
		`{"values":` + strings.Repeat("[", MaxJSONDepth+1) + `0` + strings.Repeat("]", MaxJSONDepth+1) + `}`,
	} {
		var target request
		if DecodeStrict([]byte(data), &target) == nil {
			t.Errorf("accepted ambiguous or unbounded request of %d bytes", len(data))
		}
	}
	for _, data := range []string{`{}`, `{"name":"\ud83d\ude00"}`, `{"name":"literal \\ud800 and \"quote\"","nested":{"enabled":true},"values":[1,2]}`} {
		var target request
		if err := DecodeStrict([]byte(data), &target); err != nil {
			t.Errorf("rejected valid request: %v", err)
		}
	}
}

func TestProtocolManifestMatchesBounds(t *testing.T) {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &data); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]int{"body_bytes": MaxBodyBytes, "header_bytes": MaxHeaderBytes, "json_depth": MaxJSONDepth, "json_array_items": MaxJSONArray, "json_tokens": MaxJSONTokens, "json_string_bytes": MaxJSONString, "max_connections": MaxConnections, "response_bytes": MaxResponseBytes} {
		var actual int
		if err := json.Unmarshal(data[key], &actual); err != nil || actual != expected {
			t.Fatalf("manifest drift: %s", key)
		}
	}
}

func FuzzDecodeStrict(f *testing.F) {
	f.Add([]byte(`{"protocol_major":1,"protocol_minor":0,"software_version":"v1","schema_digest":"abc"}`))
	f.Add([]byte(`{"protocol_major":1,"protocol_major":2}`))
	f.Add([]byte(`{"software_version":"\ud800"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var protocol Protocol
		if DecodeStrict(data, &protocol) == nil {
			if !json.Valid(data) {
				t.Fatal("accepted invalid JSON")
			}
			canonical, err := json.Marshal(protocol)
			if err != nil {
				t.Fatal(err)
			}
			var again Protocol
			if DecodeStrict(canonical, &again) != nil || again != protocol {
				t.Fatal("typed round trip changed meaning")
			}
		}
	})
}
