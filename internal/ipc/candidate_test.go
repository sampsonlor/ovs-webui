package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func TestStrictCandidateCommandsRoundTripCapturedOriginals(t *testing.T) {
	// Exercise the actual wire encoding of a nonempty draft, including the
	// signed originals. Empty drafts cannot expose field-promotion mismatches.
	var envelope candidate.Envelope
	err := json.Unmarshal([]byte(`{"owner_id":"synthetic-owner","workspace_epoch":"synthetic-epoch","sequence":1,"candidate":{"id":"synthetic-candidate","revision":"synthetic-revision","instance_generation":"synthetic-generation","base_config_revision":"synthetic-config","state":"staged","intents":[{"intent_id":"synthetic-intent","operation":"port.vlan.set","object":{"management_id":"synthetic-port","ovs_uuid":"synthetic-ovs","table":"Port","instance_generation":"synthetic-generation"},"value":{"vlan_mode":"native-tagged","tag":67,"trunks":[],"cvlans":[]},"before":{"vlan_mode":null,"tag":38,"trunks":[38,67],"cvlans":[]},"dependency_revision":"synthetic-dependency","schema_digest":"synthetic-schema"}],"consumed_by":null},"seal":""}`), &envelope)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Sign(bytes.Repeat([]byte{7}, 32))
	command := authn.Command{Method: "POST", URI: "/api/v1/validations", Payload: json.RawMessage(`{"request_id":"synthetic-request","candidate_id":"synthetic-candidate","candidate_revision":"synthetic-revision"}`)}
	for _, tc := range []struct {
		name   string
		input  any
		output any
	}{
		{"prepare", candidate.PrepareRequest{Envelope: envelope, Command: command}, &candidate.PrepareRequest{}},
		{"read", candidate.ReadRequest{Envelope: envelope, ValidationID: "synthetic-validation"}, &candidate.ReadRequest{}},
		{"validate", candidate.ValidateRequest{Envelope: envelope, Command: command}, &candidate.ValidateRequest{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if err = DecodeStrict(encoded, tc.output); err != nil {
				t.Fatalf("nonempty candidate rejected by IPC: %v", err)
			}
			canonical, err := json.Marshal(tc.output)
			if err != nil || !bytes.Equal(encoded, canonical) {
				t.Fatal("candidate wire round trip changed signed originals")
			}
			for _, invalid := range []string{
				strings.ReplaceAll(string(encoded), `"intent_id":`, `"Intent_ID":`),
				strings.ReplaceAll(string(encoded), `"before":`, `"actor":"admin","before":`),
				strings.ReplaceAll(string(encoded), `"tag":38`, `"tag":38,"tag":67`),
			} {
				if DecodeStrict([]byte(invalid), tc.output) == nil {
					t.Fatal("candidate bypassed exact field or duplicate-key checks")
				}
			}
		})
	}
}
