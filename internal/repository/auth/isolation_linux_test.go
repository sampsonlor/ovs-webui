//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func TestIsolationRequiresExplicitCapabilityCurrentCeilingAndRootAuthority(t *testing.T) {
	r, _ := fixture(t)
	old := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, old)
	b := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: p.snapshot.Generation}
	port := plan.IsolationPort{Binding: b, Known: true, Supported: true, Authority: "local-managed", Dependency: "private-bridge"}
	p.snapshot.Isolations = map[string]plan.IsolationPort{b.ManagementID: port}
	prepare := func(g authn.LoginResult) (plan.Envelope, error) {
		id := planRequestID()
		body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": repository.NewID(), "operation": plan.PortIsolationSet, "object": b, "protected": true}}})
		return r.PrepareCandidate(testContext, g.Grant, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}})
	}
	_, err := prepare(old)
	wantCode(t, err, "CAPABILITY_DENIED")
	setCaps := func(caps []string) {
		body, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, old.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setCaps(append(append([]string{}, old.Claims.Capabilities...), plan.PortIsolationCapability))
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
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: plan.PortIsolationCapability}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }); err != nil {
		t.Fatal(err)
	}
	port.Authority = "unknown"
	p.snapshot.Isolations[b.ManagementID] = port
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("root revocation retained validation")
	}
	port.Authority = "local-managed"
	p.snapshot.Isolations[b.ManagementID] = port
	setCaps(old.Claims.Capabilities)
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("current capability revocation retained validation")
	}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
}
