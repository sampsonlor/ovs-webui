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
)

func TestFormalValidationBondAuthorityRevocationAndCredentialCeiling(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, p, i := planSetup(t, r, g)
	port := p.snapshot.Ports[i.Object.ManagementID]
	port.BondKnown, port.BondSupported, port.MemberKindsSupported = true, true, true
	port.BondAuthority, port.BondDependency = "local-managed", "members"
	port.Members = []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	p.snapshot.Ports[i.Object.ManagementID] = port
	id := planRequestID()
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": i.ID, "operation": "bond.configure", "object": i.Object, "mode": "balance-tcp", "lacp": "active", "fallback": "enabled", "member_interface_ids": port.Members}}})
	e, err := r.PrepareCandidate(testContext, g.Grant, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if v := readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID); !v.Usable {
		t.Fatal(v)
	}
	provider := &executionProvider{}
	engine, err := r.ConfigureExecution(provider)
	if err != nil {
		t.Fatal(err)
	}
	setCaps := func(caps []string) {
		t.Helper()
		encoded, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", encoded, g.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	caps := slices.DeleteFunc(append([]string{}, g.Claims.Capabilities...), func(c string) bool { return c == "ovs.port.bond.write" })
	setCaps(caps)
	if v := readPlanValidation(t, r, g.Grant, e, valid.Receipt.Resource.ID); v.Usable {
		t.Fatal("revoked field capability retained validation")
	}
	_, err = engine.Submit(testContext, execution.Request{ID: planRequestID(), ValidationID: valid.Receipt.Resource.ID, Envelope: e}, r.ExecutionAuthorizer(g.Grant), isolatedLease)
	wantCode(t, err, "CAPABILITY_DENIED")
	if provider.prepares != 0 || provider.sends != 0 {
		t.Fatal("Bond executed with only VLAN authority")
	}
	// Old journals have no field discriminator and must retain VLAN-only scope.
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }); err != nil {
		t.Fatal("legacy VLAN authority was widened", err)
	}
	a.FieldCapabilities = "ovs.port.bond.write"
	err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) })
	wantCode(t, err, "APPLY_AUTHORITY_REVOKED")
	limited := login(t, r, "admin", false)
	setCaps(g.Claims.Capabilities)
	blocked, err := r.ValidateCandidate(testContext, limited.Grant, validationRequest(e, limited.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if v := readPlanValidation(t, r, limited.Grant, e, blocked.Receipt.Resource.ID); v.Usable || v.State != "blocked" {
		t.Fatal("new role permission silently expanded an old credential", v)
	}
	_, err = engine.Submit(testContext, execution.Request{ID: planRequestID(), ValidationID: blocked.Receipt.Resource.ID, Envelope: e}, r.ExecutionAuthorizer(limited.Grant), isolatedLease)
	wantCode(t, err, "CAPABILITY_DENIED")
	if provider.prepares != 0 || provider.sends != 0 {
		t.Fatal("credential ceiling was ignored")
	}
}
