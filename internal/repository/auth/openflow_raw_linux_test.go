package auth

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestOpenFlowRawReadsRequireCurrentConfigurationCapability(t *testing.T) {
	r, _ := fixture(t)
	admin := login(t, r, "admin", true)
	limited := execute(t, r, admin, "POST", "/tokens", map[string]any{
		"name": "openflow-state-only", "scopes": []string{"state.read", "inventory.read"},
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}, "")
	var access struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(limited.Body, &access); err != nil || access.Secret == "" {
		t.Fatal("failed to mint the scoped synthetic credential")
	}
	bridge, generation := repository.NewID(), repository.NewID()
	action := authn.Query{Method: "GET", URI: "/api/v1/openflow/bridges/" + bridge + "/snapshots?table=7&raw_fields=true"}
	_, err := r.ReadAuth(t.Context(), access.Secret, action)
	wantCode(t, err, "CAPABILITY_DENIED")

	// Exercise an independent retrieval gate. A scoped credential matching a
	// retained raw record still needs current configuration authority.
	claims, err := r.InspectAuth(t.Context(), access.Secret)
	if err != nil {
		t.Fatal(err)
	}
	r.WithOpenFlow(&ovsdb.OpenFlowObserver{})
	table := uint8(7)
	now := r.now()
	view, err := r.openFlow.views.Put(now, openFlowScope(claims), ovsdb.OpenFlowSnapshot{
		BridgeID: bridge, InstanceGeneration: generation, ObservedAt: now, Source: "openflow-1.3-unix",
		Query: ovsdb.OpenFlowCapturedQuery{Table: &table, Limit: 1, RawFields: true},
		Rows:  []ovsdb.OpenFlowRow{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"?offset=0", "?export=true"} {
		action.URI = "/api/v1/openflow/snapshots/" + view.ID + mode
		_, err := r.ReadAuth(t.Context(), access.Secret, action)
		wantCode(t, err, "CAPABILITY_DENIED")
	}
}
