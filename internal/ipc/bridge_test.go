package ipc

import (
	"bytes"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestCreationGraphIsSealedAndStrictAcrossIPC(t *testing.T) {
	e := candidate.Envelope{Owner: repository.NewID(), Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{{Operation: candidate.BridgeCreate, Creation: &candidate.BridgeCreation{Name: "br-new", Root: repository.NewID(), AfterPresent: true}}}}}
	key := bytes.Repeat([]byte{7}, 32)
	e.Sign(key)
	body, err := json.Marshal(candidate.ValidateRequest{Envelope: e})
	if err != nil {
		t.Fatal(err)
	}
	var out candidate.ValidateRequest
	if err = DecodeStrict(body, &out); err != nil {
		t.Fatal(err)
	}
	if err = out.Envelope.Verify(key, e.Owner); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.Replace(string(body), `"before_present":false`, `"before_present":false,"before_present":true`, 1), strings.Replace(string(body), `"name":"br-new"`, `"name":"br-new","actor":"admin"`, 1)} {
		if DecodeStrict([]byte(invalid), &out) == nil {
			t.Fatal("creation graph bypassed closed IPC shape")
		}
	}
	forged := strings.Replace(string(body), `"after_present":true`, `"after_present":false`, 1)
	if err = DecodeStrict([]byte(forged), &out); err != nil {
		t.Fatal(err)
	}
	if out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("forged compensation accepted")
	}
}

func TestDeletionGraphSealAndStrictIPC(t *testing.T) {
	e := candidate.Envelope{Owner: repository.NewID(), Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{
		{Operation: candidate.BridgeDelete, Deletion: &candidate.BridgeDeletion{SourceMarker: strings.Repeat("a", 64), Source: candidate.BridgeGraph{Name: "original"}, Replacement: candidate.BridgeGraph{Name: "replacement"}}},
	}}}
	key := bytes.Repeat([]byte{7}, 32)
	e.Sign(key)
	b, _ := json.Marshal(candidate.ValidateRequest{Envelope: e})
	var out candidate.ValidateRequest
	if err := DecodeStrict(b, &out); err != nil {
		t.Fatal(err)
	}
	if err := out.Envelope.Verify(key, e.Owner); err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(b), `"name":"replacement"`, `"name":"forged"`, 1)
	if err := DecodeStrict([]byte(forged), &out); err != nil {
		t.Fatal(err)
	}
	if out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("unsealed compensation identities accepted")
	}
	forged = strings.Replace(string(b), `"name":"replacement"`, `"name":"replacement","name":"forged"`, 1)
	if DecodeStrict([]byte(forged), &out) == nil {
		t.Fatal("duplicate graph field accepted")
	}
}

func TestInternalPortGraphIsSealedAndStrictAcrossIPC(t *testing.T) {
	e := candidate.Envelope{Owner: repository.NewID(), Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{{Operation: candidate.InternalPortCreate, PortCreation: &candidate.InternalPortCreation{Name: "pi-new", Root: repository.NewID(), AfterPresent: true}}}}}
	key := bytes.Repeat([]byte{7}, 32)
	e.Sign(key)
	body, err := json.Marshal(candidate.ValidateRequest{Envelope: e})
	if err != nil {
		t.Fatal(err)
	}
	var out candidate.ValidateRequest
	if err = DecodeStrict(body, &out); err != nil {
		t.Fatal(err)
	}
	if err = out.Envelope.Verify(key, e.Owner); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.Replace(string(body), `"before_present":false`, `"before_present":false,"before_present":true`, 1), strings.Replace(string(body), `"name":"pi-new"`, `"name":"pi-new","actor":"admin"`, 1)} {
		if DecodeStrict([]byte(invalid), &out) == nil {
			t.Fatal("creation graph bypassed closed IPC shape")
		}
	}
	forged := strings.Replace(string(body), `"after_present":true`, `"after_present":false`, 1)
	if err = DecodeStrict([]byte(forged), &out); err != nil {
		t.Fatal(err)
	}
	if out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("forged compensation accepted")
	}
}
