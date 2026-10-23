//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func TestMulticastKeepsExplicitCapabilityCurrentCeilingAndRootAuthority(t *testing.T) {
	r, _ := fixture(t)
	old := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, old)
	b := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Bridge", Generation: p.snapshot.Generation}
	port := plan.MulticastBridge{Configuration: multicast.Configuration{Parameters: map[string]*string{multicast.Keys[0]: nil, multicast.Keys[1]: nil, multicast.Keys[2]: nil}}, Binding: b, Known: true, Supported: true, Authority: "local-managed", Dependency: "private-bridge"}
	p.snapshot.Multicasts = map[string]plan.MulticastBridge{b.ManagementID: port}
	prepare := func(g authn.LoginResult) (plan.Envelope, error) {
		id := planRequestID()
		body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": repository.NewID(), "operation": plan.BridgeMulticastSet, "object": b, "multicast": map[string]any{"enabled": true, "parameters": map[string]any{}}}}})
		return r.PrepareCandidate(testContext, g.Grant, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
	}
	_, err := prepare(old)
	wantCode(t, err, "CAPABILITY_DENIED")
	var role string
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", old.Claims.PrincipalID).Scan(&role)
	}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, r.SetMulticastExecutionRole(testContext, "invalid", true), "INVALID_ROLE")
	wantCode(t, r.SetMulticastExecutionRole(testContext, repository.NewID(), true), "ROLE_NOT_FOUND")
	for n := 0; n < 2; n++ {
		if err := r.SetMulticastExecutionRole(testContext, role, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		var n int
		err := q.QueryRowContext(ctx, "SELECT count(*) FROM evidence_records WHERE operation=?", "multicast-execution-role").Scan(&n)
		if err == nil && n != 1 {
			t.Fatal("root policy did not audit exactly one grant", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = prepare(old)
	wantCode(t, err, "CAPABILITY_DENIED")
	g := login(t, r, "admin", false)
	e, err = prepare(g)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	if !readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("qualified isolated field blocked")
	}
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: plan.BridgeMulticastCapability}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }); err != nil {
		t.Fatal(err)
	}
	port.Authority = "unknown"
	p.snapshot.Multicasts[b.ManagementID] = port
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("root revocation retained validation")
	}
	port.Authority = "local-managed"
	p.snapshot.Multicasts[b.ManagementID] = port
	if err := r.SetMulticastExecutionRole(testContext, role, false); err != nil {
		t.Fatal(err)
	}
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("current capability revocation retained validation")
	}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
}
