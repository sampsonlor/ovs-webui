package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestStrictMirrorPreservesNativeDirectionUnsetAndSealedReferences(t *testing.T) {
	object := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Mirror", Generation: repository.NewID()}
	port := repository.NewID()
	output := "200"
	before := mirror.Configuration{SourcePorts: []string{port}, DestinationPorts: []string{}, VLANs: []string{"0"}, OutputVLAN: &output}
	after := mirror.Clone(before)
	after.SourcePorts, after.DestinationPorts = []string{}, []string{port}
	e := candidate.Envelope{Owner: "synthetic-owner", Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{{ID: repository.NewID(), Object: object, Operation: candidate.MirrorConfigure, Topology: &candidate.TopologyChange{Mirror: &candidate.MirrorChange{Object: object, Before: &before, After: &after}, MirrorAuthorityRevision: repository.NewID(), MirrorDependency: candidate.Digest("private-read-set"), Request: candidate.TopologyRequest{Mirror: &after}}}}}}
	key := bytes.Repeat([]byte{11}, 32)
	e.Sign(key)
	body, err := json.Marshal(candidate.ValidateRequest{Envelope: e})
	if err != nil {
		t.Fatal(err)
	}
	var out candidate.ValidateRequest
	if err := DecodeStrict(body, &out); err != nil || out.Envelope.Verify(key, e.Owner) != nil {
		t.Fatal("sealed Mirror rejected", err)
	}
	canonical, _ := json.Marshal(out)
	if !bytes.Equal(body, canonical) || out.Envelope.Candidate.Intents[0].Topology.Mirror.Before.Snaplen != nil {
		t.Fatal("native null or direction changed")
	}
	for _, fragment := range []string{`"select_all":false,"select_all":true`, `"Select_all":false`, `"select_all":false,"filter":"ip"`} {
		invalid := strings.Replace(string(body), `"select_all":false`, fragment, 1)
		if DecodeStrict([]byte(invalid), &out) == nil {
			t.Fatal("ambiguous or arbitrary Mirror selector accepted")
		}
	}
	forged := strings.Replace(string(body), port, repository.NewID(), 1)
	if err := DecodeStrict([]byte(forged), &out); err != nil || out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("changed selector reference retained valid seal", err)
	}
}
