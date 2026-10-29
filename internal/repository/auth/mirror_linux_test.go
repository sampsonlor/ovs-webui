//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func TestMirrorKeepsExplicitCapabilityCurrentCeilingAndRootAuthority(t *testing.T) {
	t.Run("read_reference_is_current_mirror_identity", func(t *testing.T) {
		r, _ := fixture(t)
		g := login(t, r, "admin", false)
		generation, oldGeneration := repository.NewID(), repository.NewID()
		port, iface, retired, oldPort := repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID()
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			for _, gen := range []string{generation, oldGeneration} {
				if _, err := tx.ExecContext(ctx, "INSERT INTO generations(id,evidence,created_at) VALUES(?,?,?)", gen, []byte("{}"), r.now().Unix()); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO inventory_state(singleton,generation,state,reason,evidence) VALUES(1,?,'confirmed','synthetic-read-scope',?)", generation, []byte("{}")); err != nil {
				return err
			}
			for _, row := range []struct{ id, table, gen, state string }{{port, "Mirror", generation, "active"}, {iface, "Interface", generation, "active"}, {retired, "Mirror", generation, "retired"}, {oldPort, "Mirror", oldGeneration, "active"}} {
				if _, err := tx.ExecContext(ctx, "INSERT INTO identities(management_id,generation,table_name,ovs_uuid,state) VALUES(?,?,?,?,?)", row.id, row.gen, row.table, repository.NewID(), row.state); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		check := func(id string, claims authn.Claims) error {
			return r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
				return checkRefs(ctx, q, claims, []apitypes.Ref{{Kind: "mirror", ID: id}})
			})
		}
		if err := check(port, g.Claims); err != nil {
			t.Fatal("current Mirror did not resolve through its native read boundary", err)
		}
		for _, id := range []string{iface, retired, oldPort, repository.NewID()} {
			wantCode(t, check(id, g.Claims), "NOT_FOUND")
		}
		wantCode(t, check(port, authn.Claims{Capabilities: []string{"state.read", "inventory.read"}}), "CAPABILITY_DENIED")
		_, err := r.ReadAuth(testContext, g.Grant, authn.Query{Method: "GET", URI: "/api/v1/mirrors/" + port})
		wantCode(t, err, "DOMAIN_SERVICE_UNAVAILABLE") // Scope alone cannot invent native data.
	})
	r, _ := fixture(t)
	old := login(t, r, "admin", false)
	e, p, _ := planSetup(t, r, old)
	b := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: p.snapshot.Generation}
	bridge := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Bridge", Generation: p.snapshot.Generation}
	iface := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Interface", Generation: p.snapshot.Generation}
	m := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Mirror", Generation: p.snapshot.Generation}
	configs := map[string]map[string]any{
		bridge.ManagementID: {"name": "br-mirror", "datapath_type": "system", "ports": []any{b.OVSUUID}, "mirrors": []any{m.OVSUUID}, "controller": []any{}, "stp_enable": false, "rstp_enable": false},
		b.ManagementID:      {"name": "data-1", "interfaces": []any{iface.OVSUUID}},
		iface.ManagementID:  {"name": "data-1", "type": "internal", "options": map[string]any{}},
		m.ManagementID:      {"name": "span-1", "select_all": false, "select_src_port": []any{b.OVSUUID}, "select_dst_port": []any{}, "select_vlan": []any{}, "output_port": []any{}, "output_vlan": []any{"200"}, "snaplen": []any{}, "external_ids": map[string]any{}},
	}
	nodes := map[string]plan.TopologyNode{}
	authority := map[string]bool{}
	for _, binding := range []plan.Binding{bridge, b, iface, m} {
		links, typ := []plan.Binding{}, ""
		if binding == bridge {
			links, typ = []plan.Binding{b}, "system"
		}
		if binding == b {
			links = []plan.Binding{iface}
		}
		if binding == iface {
			typ = "internal"
		}
		if binding == m {
			typ = "mirror"
		}
		nodes[binding.ManagementID] = plan.TopologyNode{Binding: binding, Name: configs[binding.ManagementID]["name"].(string), Type: typ, Links: links, Digest: plan.ConfigurationDigest(configs[binding.ManagementID])}
		authority[binding.ManagementID] = true
	}
	p.snapshot.Topology = plan.TopologySnapshot{Root: repository.NewID(), RootDependency: "private-root", Supported: true, Capacity: true, MirrorSupported: true, MirrorAuthorityRevision: repository.NewID(), Nodes: nodes, Configurations: configs, MirrorAuthority: authority}

	prepare := func(g authn.LoginResult) (plan.Envelope, error) {
		id := planRequestID()
		body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []any{map[string]any{"intent_id": repository.NewID(), "operation": plan.MirrorDelete, "object": m, "topology": map[string]any{}}}})
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
	wantCode(t, r.SetMirrorExecutionRole(testContext, "invalid", true), "INVALID_ROLE")
	wantCode(t, r.SetMirrorExecutionRole(testContext, repository.NewID(), true), "ROLE_NOT_FOUND")
	for n := 0; n < 2; n++ {
		if err := r.SetMirrorExecutionRole(testContext, role, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		var n int
		err := q.QueryRowContext(ctx, "SELECT count(*) FROM evidence_records WHERE operation=?", "mirror-execution-role").Scan(&n)
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
	a := execution.Authorization{Owner: g.Claims.PrincipalID, Credential: g.Claims.CredentialID, Epoch: g.Claims.RequestEpoch, FieldCapabilities: plan.MirrorCapability}
	if err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }); err != nil {
		t.Fatal(err)
	}
	p.snapshot.Topology.MirrorAuthority[bridge.ManagementID] = false
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("root revocation retained validation")
	}
	p.snapshot.Topology.MirrorAuthority[bridge.ManagementID] = true
	if err := r.SetMirrorExecutionRole(testContext, role, false); err != nil {
		t.Fatal(err)
	}
	if readPlanValidation(t, r, g.Grant, e, ack.Receipt.Resource.ID).Usable {
		t.Fatal("current capability revocation retained validation")
	}
	wantCode(t, r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error { return r.safeAuthority(ctx, q, a) }), "APPLY_AUTHORITY_REVOKED")
}
