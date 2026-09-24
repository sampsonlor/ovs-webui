package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
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

func TestStrictBondOriginalsRetainSealAndNativeAbsence(t *testing.T) {
	active, mode, fallback := "active", "balance-tcp", "true"
	e := candidate.Envelope{Owner: "synthetic-owner", Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{{Operation: "bond.configure", Bond: &candidate.Bond{LACP: &active, Mode: &mode, Fallback: &fallback}, BeforeBond: &candidate.Bond{}}}}}
	key := bytes.Repeat([]byte{7}, 32)
	e.Sign(key)
	in := candidate.ValidateRequest{Envelope: e}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out candidate.ValidateRequest
	if err = DecodeStrict(body, &out); err != nil {
		t.Fatal(err)
	}
	if err = out.Envelope.Verify(key, e.Owner); err != nil {
		t.Fatal("wire encoding changed signed originals", err)
	}
	if candidate.Digest(out.Envelope.Candidate.Intents[0].BeforeBond) != candidate.Digest(candidate.Bond{}) {
		t.Fatal("absence was normalized")
	}
	for _, invalid := range []string{
		strings.Replace(string(body), `"lacp":"active"`, `"lacp":"active","lacp":"off"`, 1),
		strings.Replace(string(body), `"lacp":null`, `"lacp":null,"actor":"admin"`, 1),
		strings.Replace(string(body), `"before_bond":`, `"Before_Bond":`, 1),
	} {
		if DecodeStrict([]byte(invalid), &out) == nil {
			t.Fatal("nested field bypassed strict decoding")
		}
	}
	if err = DecodeStrict([]byte(strings.Replace(string(body), `"lacp":null`, `"lacp":"off"`, 1)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("forged original retained valid seal")
	}
}
