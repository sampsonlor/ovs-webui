package ipc

import (
	"encoding/json"
	"strings"
	"testing"
)

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
