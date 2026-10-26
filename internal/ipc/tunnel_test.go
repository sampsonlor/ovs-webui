package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/tunnel"
)

func TestStrictStaticTunnelPreservesOptionalAbsenceDependencyAndSeal(t *testing.T) {
	object := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Interface", Generation: repository.NewID()}
	before := tunnel.Configuration{Type: "geneve", Options: map[string]string{"remote_ip": "2001:db8::20"}}
	after := tunnel.Clone(before)
	after.Options["key"] = "0"
	nativeKey := "0"
	e := candidate.Envelope{Owner: "synthetic-owner", Epoch: repository.NewID(), Sequence: 1,
		Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{{ID: repository.NewID(), Operation: candidate.TunnelConfigure, Object: object,
			Topology: &candidate.TopologyChange{Tunnel: &candidate.TunnelChange{Interface: object, Before: &before, After: &after}, TunnelAuthorityRevision: repository.NewID(), TunnelDependency: candidate.Digest("captured-persistent-read-set"), Request: candidate.TopologyRequest{Tunnel: &tunnel.Request{Options: map[string]*string{"key": &nativeKey, "local_ip": nil}}}}}}}}
	key := bytes.Repeat([]byte{7}, 32)
	e.Sign(key)
	body, err := json.Marshal(candidate.ValidateRequest{Envelope: e})
	if err != nil {
		t.Fatal(err)
	}
	var out candidate.ValidateRequest
	if err = DecodeStrict(body, &out); err != nil || out.Envelope.Verify(key, e.Owner) != nil {
		t.Fatal("signed tunnel originals rejected", err)
	}
	canonical, _ := json.Marshal(out)
	if !bytes.Equal(body, canonical) || out.Envelope.Candidate.Intents[0].Topology.Request.Tunnel.Options["local_ip"] != nil {
		t.Fatal("native unset or signed dependency changed")
	}
	for _, replacement := range []string{
		`"options":{"private_key":"must-not-accept",`,
		`"options":{"key":"0","key":"1",`,
		`"Options":{`,
	} {
		invalid := strings.Replace(string(body), `"options":{`, replacement, 1)
		// Options are a deliberately closed application group. Arbitrary map keys
		// are rejected by Merge/Validate; strict IPC rejects duplicate/case fields.
		if strings.Contains(replacement, "private_key") {
			if DecodeStrict([]byte(invalid), &out) == nil {
				if tunnel.Validate(*out.Envelope.Candidate.Intents[0].Topology.Tunnel.Before) == nil {
					t.Fatal("unknown native option retained writer eligibility")
				}
			}
		} else if DecodeStrict([]byte(invalid), &out) == nil {
			t.Fatal("duplicate or case-ambiguous tunnel field accepted")
		}
	}
	forged := strings.Replace(string(body), `"key":"0"`, `"key":"1"`, 1)
	if err = DecodeStrict([]byte(forged), &out); err != nil || out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("modified tunnel retained a valid seal", err)
	}
}
