//go:build linux

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
)

type workspaceProvider struct {
	snapshot candidate.Snapshot
	failure  error
}

func (p *workspaceProvider) Read(context.Context, string, map[string]string, url.Values, authn.Claims) (any, error) {
	return nil, apitypes.Fail(503, "UNUSED")
}
func (p *workspaceProvider) CandidateSnapshot(context.Context, []candidate.Binding) (candidate.Snapshot, error) {
	return p.snapshot, p.failure
}
func workspaceID() string            { id, _ := uuid.NewV7(); return id.String() }
func workspacePointer[T any](v T) *T { return &v }
func TestWorkspaceHTTPConcurrentSavesLostRepliesValidationAndRevocation(t *testing.T) {
	h, a, store := browserFixture(t)
	manager := a.manager.(*auth.Repository)
	binding := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: repository.NewID()}
	p := &workspaceProvider{snapshot: candidate.Snapshot{Generation: binding.Generation, Revision: "native-1", Schema: "schema-1", Policy: "reviewed", Ports: map[string]candidate.Port{binding.ManagementID: {Binding: binding, Known: true, SchemaSupported: true, Modes: []string{"access", "native-tagged", "native-untagged", "trunk"}, Authority: "local-managed", Dependency: "structural-dependency", VLAN: candidate.VLAN{Mode: workspacePointer("access"), Tag: workspacePointer(10), Trunks: []int{}, CVLANs: []int{}}}}}}
	manager.WithInventory(p)
	w, err := NewWorkspace(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	a.WithWorkspace(w)
	cookie, csrf, session := browserLogin(t, h)
	epochs := session["request_epochs"].(map[string]any)
	call := func(method, path string, body any, etag string, domain string) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "https://console.example/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Cookie", sessionCookie+"="+cookie)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://console.example")
			r.Header.Set("X-OVS-CSRF-Token", csrf)
			var cmd map[string]any
			_ = json.Unmarshal(data, &cmd)
			if id, ok := cmd["request_id"].(string); ok {
				r.Header.Set("Idempotency-Key", id)
				r.Header.Set("X-OVS-Request-Epoch", epochs[domain].(string))
			}
		}
		if etag != "" {
			r.Header.Set("If-Match", etag)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	read := func() candidate.View {
		t.Helper()
		out := call("GET", "/candidate", nil, "", "")
		if out.Code != 200 {
			t.Fatal(out.Code, out.Body.String())
		}
		var v candidate.View
		_ = json.Unmarshal(out.Body.Bytes(), &v)
		if out.Header().Get("ETag") != `"`+v.Revision+`"` {
			t.Fatal("strong revision missing")
		}
		return v
	}
	initial := read()
	if initial.State != "empty" {
		t.Fatal(initial)
	}
	intent := candidate.Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: binding, Value: candidate.VLAN{Mode: workspacePointer("native-tagged"), Tag: workspacePointer(20), Trunks: []int{}, CVLANs: []int{}}}
	bodies := []map[string]any{{"request_id": workspaceID(), "operation": "stage", "intents": []candidate.Intent{intent}}, {"request_id": workspaceID(), "operation": "stage", "intents": []candidate.Intent{intent}}}
	responses := make([]*httptest.ResponseRecorder, 2)
	var group sync.WaitGroup
	for n := range bodies {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			responses[n] = call("PATCH", "/candidate", bodies[n], `"`+initial.Revision+`"`, "workspace")
		}(n)
	}
	group.Wait()
	winner := -1
	for n, r := range responses {
		if r.Code == 200 {
			if winner >= 0 {
				t.Fatal("both stale saves won")
			}
			winner = n
		} else if r.Code != 412 && r.Code != 409 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if winner < 0 {
		t.Fatal("no save succeeded", responses[0].Body.String(), responses[1].Body.String())
	}
	e := read()
	if *e.Intents[0].Before.Tag != 10 || *p.snapshot.Ports[binding.ManagementID].VLAN.Tag != 10 {
		t.Fatal("save changed original/live")
	}
	validationCommand := map[string]any{"request_id": workspaceID(), "candidate_id": e.ID, "candidate_revision": e.Revision}
	out := call("POST", "/validations", validationCommand, "", "management")
	if out.Code != 202 {
		t.Fatal(out.Code, out.Body.String())
	}
	var accepted apitypes.Accepted
	_ = json.Unmarshal(out.Body.Bytes(), &accepted)
	out = call("GET", "/validations/"+accepted.Resource.ID, nil, "", "")
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var v candidate.Validation
	_ = json.Unmarshal(out.Body.Bytes(), &v)
	if !v.Usable || v.ExecutionReady {
		t.Fatal(v)
	}
	p.failure = apitypes.Fail(503, "PROVIDER_UNAVAILABLE")
	replay := call("PATCH", "/candidate", bodies[winner], `"`+initial.Revision+`"`, "workspace")
	if replay.Code != 200 || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("lost save reply required provider or re-executed", replay.Code, replay.Body.String())
	}
	lookup := call("GET", "/requests/"+bodies[winner]["request_id"].(string)+"?domain=workspace&epoch="+epochs["workspace"].(string), nil, "", "")
	if lookup.Code != 200 {
		t.Fatal("workspace receipt incorrectly queried manager.db", lookup.Code, lookup.Body.String())
	}
	out = call("POST", "/validations", validationCommand, "", "management")
	if out.Code != 202 || out.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("validation handoff recovery lost original", out.Code, out.Body.String())
	}
	out = call("GET", "/workspace", nil, "", "")
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	discard := map[string]any{"request_id": workspaceID(), "operation": "discard"}
	out = call("PATCH", "/candidate", discard, `"`+e.Revision+`"`, "workspace")
	if out.Code != 200 {
		t.Fatal("offline discard failed", out.Code, out.Body.String())
	}
	p.failure = nil
	out = call("GET", "/validations/"+accepted.Resource.ID, nil, "", "")
	_ = json.Unmarshal(out.Body.Bytes(), &v)
	if out.Code != 200 || v.Usable {
		t.Fatal("discarded revision remained usable", out.Code, out.Body.String())
	}
	// A replacement web service reconstructs the same private workspace from DB.
	w2, err := NewWorkspace(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	a.WithWorkspace(w2)
	if again := read(); again.ID != e.ID || again.State != "empty" {
		t.Fatal("workspace restart changed identity", again)
	}
	r := httptest.NewRequest("GET", "https://console.example/api/v1/session", nil)
	r.Header.Set("Cookie", sessionCookie+"="+cookie)
	subject, err := a.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.RevokeAuth(context.Background(), subject.Credential); err != nil {
		t.Fatal(err)
	}
	if out = call("GET", "/candidate", nil, "", ""); out.Code != 401 {
		t.Fatal("revoked principal read private draft", out.Code, out.Body.String())
	}
}
