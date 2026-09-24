//go:build linux

package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
)

func TestWorkspaceHTTPBondReviewAndClosedIntentBoundary(t *testing.T) {
	h, a, store := browserFixture(t)
	manager := a.manager.(*auth.Repository)
	b := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: repository.NewID()}
	members := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	p := &workspaceProvider{snapshot: candidate.Snapshot{Generation: b.Generation, Revision: "native-1", Schema: "schema-1", Policy: "reviewed", Ports: map[string]candidate.Port{b.ManagementID: {Binding: b, BondKnown: true, BondSupported: true, BondAuthority: "local-managed", BondDependency: "member-identities", Members: members, MemberKindsSupported: true}}}}
	manager.WithInventory(p)
	w, err := NewWorkspace(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	a.WithWorkspace(w)
	cookie, csrf, session := browserLogin(t, h)
	epochs := session["request_epochs"].(map[string]any)
	call := func(method, path, revision, domain string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			body["request_id"] = workspaceID()
			data, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "https://console.example/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Cookie", sessionCookie+"="+cookie)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://console.example")
			r.Header.Set("X-OVS-CSRF-Token", csrf)
			r.Header.Set("Idempotency-Key", body["request_id"].(string))
			r.Header.Set("X-OVS-Request-Epoch", epochs[domain].(string))
		}
		if revision != "" {
			r.Header.Set("If-Match", `"`+revision+`"`)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	read := func() candidate.View {
		t.Helper()
		out := call("GET", "/candidate", "", "", nil)
		var v candidate.View
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &v) != nil {
			t.Fatal(out.Code, out.Body.String())
		}
		return v
	}
	v := read()
	intent := map[string]any{"intent_id": repository.NewID(), "operation": "bond.configure", "object": b, "mode": "balance-tcp", "lacp": "active", "fallback": "enabled", "member_interface_ids": members}
	stage := func() *httptest.ResponseRecorder {
		return call("PATCH", "/candidate", v.Revision, "workspace", map[string]any{"operation": "stage", "intents": []any{intent}})
	}
	if out := stage(); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	v = read()
	if len(v.Diff) != 3 || v.Intents[0].BeforeBond == nil || v.Intents[0].BeforeBond.LACP != nil || *v.Intents[0].Bond.Mode != "balance-tcp" || p.snapshot.Ports[b.ManagementID].Bond.LACP != nil {
		t.Fatal("lost native originals or Candidate wrote provider", v)
	}
	validate := func() candidate.Validation {
		t.Helper()
		out := call("POST", "/validations", "", "management", map[string]any{"candidate_id": v.ID, "candidate_revision": v.Revision})
		var ack apitypes.Accepted
		if out.Code != 202 || json.Unmarshal(out.Body.Bytes(), &ack) != nil {
			t.Fatal(out.Code, out.Body.String())
		}
		out = call("GET", "/validations/"+ack.Resource.ID, "", "", nil)
		var result candidate.Validation
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil {
			t.Fatal(out.Code, out.Body.String())
		}
		return result
	}
	if result := validate(); !result.Usable || len(result.Diff) != 3 {
		t.Fatal(result)
	}
	intent["value"] = map[string]any{"tag": 20}
	if out := stage(); out.Code < 400 || out.Code >= 500 {
		t.Fatal("VLAN field smuggled into Bond intent", out.Code, out.Body.String())
	}
	delete(intent, "value")
	intent["member_interface_ids"] = []string{members[0], repository.NewID()}
	if out := stage(); out.Code != 409 {
		t.Fatal("member takeover accepted", out.Code, out.Body.String())
	}
	intent["member_interface_ids"], intent["lacp"] = members, "off"
	if out := stage(); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	v = read()
	if result := validate(); result.Usable || result.State != "blocked" {
		t.Fatal("unsafe native combination was usable", result)
	}
}
