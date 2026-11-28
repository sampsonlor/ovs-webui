package openflowview

import (
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestOpenFlowSnapshotAccessAndExpiryDoNotChangeOriginalEvidence(t *testing.T) {
	store := New()
	scope := Scope{repository.NewID(), repository.NewID(), repository.NewID()}
	at := time.Now().UTC()
	published := ovsdb.OpenFlowSnapshot{BridgeID: repository.NewID(), InstanceGeneration: repository.NewID(),
		ObservedAt: at, Source: "openflow-1.3-unix", Ownership: "unknown",
		Rows: []ovsdb.OpenFlowRow{{Priority: 100, Cookie: "18446744073709551615"}}}
	ref, err := store.Put(at, scope, published)
	if err != nil || ref.Rows != 1 || ref.BridgeID != published.BridgeID {
		t.Fatalf("unable to retain original observation: %+v, %v", ref, err)
	}
	published.Rows[0].Priority = 1
	read := func() ovsdb.OpenFlowSnapshot {
		t.Helper()
		metadata, got, err := store.Read(at, scope, ref.ID)
		if err != nil || metadata != ref || len(got.Rows) != 1 || got.Rows[0].Priority != 100 ||
			got.Rows[0].Cookie != "18446744073709551615" {
			t.Fatalf("changed query evidence: %+v, %v", got, err)
		}
		return got
	}
	first := read()
	first.Rows[0].Priority = 5
	read()

	for _, foreign := range []Scope{
		{repository.NewID(), scope.Credential, scope.PermissionRevision},
		{scope.Principal, repository.NewID(), scope.PermissionRevision},
		{scope.Principal, scope.Credential, repository.NewID()},
	} {
		if _, _, err := store.Read(at, foreign, ref.ID); err == nil {
			t.Fatal("snapshot crossed its original authorization context")
		}
	}
	if _, _, err := store.Read(ref.ExpiresAt, scope, ref.ID); err == nil {
		t.Fatal("expired original evidence remained readable")
	}
}
