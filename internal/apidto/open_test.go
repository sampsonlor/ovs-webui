package apidto

import (
	"encoding/json"
	"testing"
)

func TestUnknownResponseFieldsAndStatesRoundTrip(t *testing.T) {
	body := []byte(`{"id":"00000000-0000-4000-8000-000000000001","state":"future-state","sequence":"18446744073709551615","resource_kind":"future-resource","source":{"state":"unknown","reason":"new-reason","observed_at":null},"allowed_actions":["future-action"],"future":{"nested":[1,"x"]}}`)
	var v Resource
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.State != "future-state" || v.ExtraFields["future"] == nil {
		t.Fatal("unknown response discarded")
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(encoded, &got)
	if got["future"].(map[string]any)["nested"].([]any)[1] != "x" || got["sequence"] != "18446744073709551615" {
		t.Fatal(string(encoded))
	}
	var out Resource
	_ = json.Unmarshal(encoded, &out)
	if out.AllowedActions[0] != "future-action" {
		t.Fatal("unknown action lost")
	}
}

func TestUnionDTOsRemainJSON(t *testing.T) {
	var command CandidateCommand
	source := []byte(`{"request_id":"01994000-0000-7000-8000-000000000001","operation":"discard"}`)
	if err := json.Unmarshal(source, &command); err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(command)
	if err != nil || string(value) != string(source) {
		t.Fatal("union encoded as base64", string(value), err)
	}
}
