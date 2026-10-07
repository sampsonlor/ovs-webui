//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestInterfaceMTUValidationRequiresIndependentCapabilityAndCurrentCeiling(t *testing.T) {
	testInterfaceFieldCapability(t, false)
}
func TestInterfacePolicingValidationRequiresIndependentCapabilityAndCurrentCeiling(t *testing.T) {
	testInterfaceFieldCapability(t, true)
}
func testInterfaceFieldCapability(t *testing.T, policing bool) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, g)
	bind := func(table string) plan.Binding {
		return plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: p.snapshot.Generation}
	}
	i := plan.InterfaceMTU{Binding: bind("Interface"), Port: bind("Port"), Bridge: bind("Bridge"), Requested: plan.MTUPointer(1500), Known: true, Supported: true, Eligible: true, Authority: "local-managed", Dependency: "captured"}
	p.snapshot.Interfaces = map[string]plan.InterfaceMTU{i.Binding.ManagementID: i}
	id := planRequestID()
	capability := "ovs.interface.mtu.write"
	intent := map[string]any{"intent_id": repository.NewID(), "operation": plan.InterfaceMTUSet, "object": i.Binding, "mtu_request": 2000}
	if policing {
		capability = "ovs.interface.policing.write"
		p.snapshot.Policings = map[string]plan.InterfacePolicing{i.Binding.ManagementID: {Binding: i.Binding, Port: i.Port, Bridge: i.Bridge, Name: "synthetic-pi", Type: "internal", IfIndex: 7, Known: true, Supported: true, Eligible: true, Authority: "local-exclusive", Dependency: "captured"}}
		intent = map[string]any{"intent_id": repository.NewID(), "operation": plan.InterfacePolicingSet, "object": i.Binding, "policing": map[string]any{"mode": "bandwidth", "rate": 1000}}
	}
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{intent}})
	e, err := r.PrepareCandidate(testContext, g.Grant, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if !readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID).Usable {
		t.Fatal("MTU validation blocked")
	}
	setCaps := func(caps []string) {
		t.Helper()
		body, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, g.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: capability}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }); err != nil {
		t.Fatal("valid MTU Safe Apply authority rejected", err)
	}
	setCaps(slices.DeleteFunc(append([]string{}, g.Claims.Capabilities...), func(c string) bool { return c == capability }))
	if readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID).Usable {
		t.Fatal("MTU retained revoked capability")
	}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
	limited := login(t, r, "admin", false)
	setCaps(g.Claims.Capabilities)
	ack, err := r.ValidateCandidate(testContext, limited.Grant, validationRequest(e, limited.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if v := readPlanValidation(t, r, limited.Grant, e, ack.Receipt.Resource.ID); v.Usable || v.State != "blocked" {
		t.Fatal("old credential ceiling widened", v)
	}
}
