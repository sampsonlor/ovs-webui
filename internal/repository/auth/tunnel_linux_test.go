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

func TestStaticTunnelKeepsExplicitCapabilityCurrentCeilingAndRootAuthority(t *testing.T) {
	r, _ := fixture(t)
	old := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, old)
	b := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: p.snapshot.Generation}
	bridge := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Bridge", Generation: p.snapshot.Generation}
	iface := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Interface", Generation: p.snapshot.Generation}
	configs := map[string]map[string]any{
		bridge.ManagementID: {"name": "br-tunnel", "datapath_type": "system", "ports": []any{b.OVSUUID}, "controller": []any{}, "stp_enable": false, "rstp_enable": false},
		b.ManagementID:      {"name": "tun0", "interfaces": []any{iface.OVSUUID}},
		iface.ManagementID:  {"name": "tun0", "type": "vxlan", "options": map[string]any{"remote_ip": "192.0.2.20"}},
	}
	nodes := map[string]plan.TopologyNode{}
	for _, binding := range []plan.Binding{bridge, b, iface} {
		links := []plan.Binding{}
		typ := ""
		if binding == bridge {
			links = []plan.Binding{b}
			typ = "system"
		}
		if binding == b {
			links = []plan.Binding{iface}
		}
		if binding == iface {
			typ = "vxlan"
		}
		nodes[binding.ManagementID] = plan.TopologyNode{Binding: binding, Name: configs[binding.ManagementID]["name"].(string), Type: typ, Links: links, Digest: plan.ConfigurationDigest(configs[binding.ManagementID])}
	}
	p.snapshot.Topology = plan.TopologySnapshot{Root: repository.NewID(), RootDependency: "private-root", Supported: true, Capacity: true, TunnelSupported: true, TunnelTypes: []string{"vxlan"}, TunnelAuthorityRevision: repository.NewID(), Nodes: nodes, Configurations: configs, TunnelAuthority: map[string]bool{bridge.ManagementID: true, b.ManagementID: true, iface.ManagementID: true}}

	prepare := func(g authn.LoginResult) (plan.Envelope, error) {
		id := planRequestID()
		body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": repository.NewID(), "operation": plan.TunnelDelete, "object": b, "topology": map[string]any{}}}})
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
	wantCode(t, r.SetStaticTunnelExecutionRole(testContext, "invalid", true), "INVALID_ROLE")
	wantCode(t, r.SetStaticTunnelExecutionRole(testContext, repository.NewID(), true), "ROLE_NOT_FOUND")
	for n := 0; n < 2; n++ {
		if err := r.SetStaticTunnelExecutionRole(testContext, role, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		var n int
		err := q.QueryRowContext(ctx, "SELECT count(*) FROM evidence_records WHERE operation=?", "static-tunnel-execution-role").Scan(&n)
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
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: plan.TunnelCapability}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }); err != nil {
		t.Fatal(err)
	}
	p.snapshot.Topology.TunnelAuthority[bridge.ManagementID] = false
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("root revocation retained validation")
	}
	p.snapshot.Topology.TunnelAuthority[bridge.ManagementID] = true
	if err := r.SetStaticTunnelExecutionRole(testContext, role, false); err != nil {
		t.Fatal(err)
	}
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("current capability revocation retained validation")
	}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
}
