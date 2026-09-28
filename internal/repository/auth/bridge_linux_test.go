//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestFormalValidationBridgeRequiresDistinctAuthorityAndCredentialCeiling(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, g)
	p.snapshot.Creation = plan.CreationSnapshot{Root: repository.NewID(), Supported: true, Capacity: true, Authority: "local-managed", AllowedNames: map[string]bool{"br-new": true}}
	id := planRequestID()
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": repository.NewID(), "operation": plan.BridgeCreate, "name": "br-new"}}})
	e, err := r.PrepareCandidate(testContext, g.Grant, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if !readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID).Usable {
		t.Fatal("creation validation blocked")
	}
	setCaps := func(caps []string) {
		t.Helper()
		b, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", b, g.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	caps := slices.DeleteFunc(append([]string{}, g.Claims.Capabilities...), func(s string) bool { return s == "ovs.bridge.create" })
	setCaps(caps)
	if readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID).Usable {
		t.Fatal("creation retained revoked permission")
	}
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: "ovs.bridge.create"}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
	limited := login(t, r, "admin", false)
	setCaps(g.Claims.Capabilities)
	ack, err := r.ValidateCandidate(testContext, limited.Grant, validationRequest(e, limited.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if v := readPlanValidation(t, r, limited.Grant, e, ack.Receipt.Resource.ID); v.Usable || v.State != "blocked" {
		t.Fatal("old credential widened", v)
	}
}

func TestFormalValidationDeletionHasSeparatePermissionAndCredentialCeiling(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, g)
	bind := func(table string) plan.Binding {
		return plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: p.snapshot.Generation}
	}
	graph := plan.BridgeGraph{Name: "br-owned", Root: repository.NewID(), Bridge: bind("Bridge"), Port: bind("Port"), Interface: bind("Interface")}
	p.snapshot.Creation = plan.CreationSnapshot{Root: graph.Root, Supported: true, Capacity: true, Authority: "local-managed"}
	p.snapshot.Deletion = plan.DeletionSnapshot{AllowedNames: map[string]bool{graph.Name: true}, Graphs: map[string]plan.ManagedBridge{
		graph.Bridge.ManagementID: {Graph: graph, Marker: strings.Repeat("a", 64), Dependency: "canonical", RestoreCapacity: true},
	}}
	id := planRequestID()
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": repository.NewID(), "operation": plan.BridgeDelete, "object": graph.Bridge}}})
	e, err := r.PrepareCandidate(testContext, g.Grant, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if !readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID).Usable {
		t.Fatal("authorized deletion blocked")
	}
	setCaps := func(caps []string) {
		t.Helper()
		b, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", b, g.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setCaps(slices.DeleteFunc(append([]string{}, g.Claims.Capabilities...), func(c string) bool { return c == "ovs.bridge.delete" }))
	if readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID).Usable {
		t.Fatal("creation or VLAN grant authorized deletion")
	}
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: "ovs.bridge.delete"}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
	limited := login(t, r, "admin", false)
	setCaps(g.Claims.Capabilities)
	ack, err := r.ValidateCandidate(testContext, limited.Grant, validationRequest(e, limited.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if readPlanValidation(t, r, limited.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("old credential widened to deletion")
	}
}
