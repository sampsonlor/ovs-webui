//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func newBondFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.vs("add-bond", "br-field", "field-bond", "field-b1", "field-b2", "--", "set", "Interface", "field-b1", "type=dummy", "--", "set", "Interface", "field-b2", "type=dummy", "--", "set", "Port", "field-bond", "lacp=off", "bond_mode=active-backup", "other_config:unrelated=keep")
	id := f.vs("get", "Port", "field-bond", "_uuid")
	waitFor(t, func() bool {
		var count int
		err := f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT count(*) FROM identities WHERE table_name='Port' AND ovs_uuid=? AND state='active'", id).Scan(&count)
		})
		return err == nil && count == 1
	})
	b := f.binding("field-bond")
	must(t, f.inventory.SetLocalBondPorts([]string{b.ManagementID, f.binding("field-p1").ManagementID}))
	waitFor(t, func() bool {
		p, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
		return err == nil && p.Ports[b.ManagementID].BondKnown
	})
	return f
}
func (f *fixture) bondIntent(name, operation, mode, lacp, fallback string) map[string]any {
	f.t.Helper()
	b := f.binding(name)
	s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
	must(f.t, err)
	value := map[string]any{"intent_id": repository.NewID(), "operation": operation, "object": b, "lacp": lacp, "fallback": fallback}
	if operation == "bond.configure" {
		value["mode"], value["member_interface_ids"] = mode, s.Ports[b.ManagementID].Members
	}
	return value
}
func (f *fixture) removeBondCapability() {
	caps := append([]string{}, f.login.Claims.Capabilities...)
	caps = slices.DeleteFunc(caps, func(c string) bool { return c == "ovs.port.bond.write" })
	body, _ := json.Marshal(caps)
	must(f.t, f.store.Write(f.ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, f.login.Claims.PrincipalID)
		return err
	}))
}

func TestNativeBondLACP(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated native Bond/LACP matrix")
	}
	t.Run("existing_bond_safe_apply_confirmation_and_identity", func(t *testing.T) {
		f := newBondFixture(t)
		before := f.binding("field-bond")
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{f.bondIntent("field-bond", "bond.configure", "balance-tcp", "active", "enabled")})
		if f.vs("get", "Port", "field-bond", "lacp") != "off" || f.proxy.sent.Load() != 0 {
			t.Fatal("Candidate wrote live state")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		var field string
		must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT field_path FROM operation_protections WHERE transaction_id=?", id).Scan(&field)
		}))
		if field != "port.bond" {
			t.Fatal("wrong protected field group", field)
		}
		if f.vs("get", "Port", "field-bond", "bond_mode") != "balance-tcp" || f.vs("get", "Port", "field-bond", "lacp") != "active" || f.vs("get", "Port", "field-bond", "other_config:unrelated") != "keep" {
			t.Fatal("wrong native configuration")
		}
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		if f.binding("field-bond") != before || f.proxy.sent.Load() != 1 {
			t.Fatal("identity replaced or command replayed")
		}
		r, err := f.engine.Read(f.ctx, id)
		must(t, err)
		if r.Authorization.FieldCapabilities != "ovs.port.bond.write" {
			t.Fatal("wrong recovery authority", r.Authorization)
		}
	})
	t.Run("single_port_LACP_rollback_preserves_unrelated_VLAN_and_map", func(t *testing.T) {
		f := newBondFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{f.bondIntent("field-p1", "port.lacp.set", "", "active", "disabled")})
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.vs("set", "Port", "field-p1", "tag=90", "other_config:unrelated=external")
		b := f.binding("field-p1")
		waitFor(t, func() bool {
			v, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{b})
			if err != nil {
				return false
			}
			p := v.Candidate.Ports[b.ManagementID]
			m, _ := v.Observation.Rows["Port"][b.OVSUUID].Values["other_config"].(map[string]any)
			return p.VLAN.Tag != nil && *p.VLAN.Tag == 90 && m["unrelated"] == "external"
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Port", "field-p1", "lacp") != "[]" || f.vs("get", "Port", "field-p1", "bond_mode") != "[]" || f.vs("get", "Port", "field-p1", "tag") != "90" || f.vs("get", "Port", "field-p1", "other_config:unrelated") != "external" {
			t.Fatal("compensation lost native absence or unrelated writer")
		}
		if f.vs("get", "Port", "field-p1", "other_config") != "{unrelated=external}" || f.proxy.sent.Load() != 2 {
			t.Fatal("fallback key was not removed exactly once")
		}
	})
	t.Run("late_external_change_aborts_mixed_field_transaction", func(t *testing.T) {
		f := newBondFixture(t)
		mode, tag := "access", 20
		vlan := candidate.Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: f.binding("field-p1"), Value: candidate.VLAN{Mode: &mode, Tag: &tag, Trunks: []int{}, CVLANs: []int{}}}
		in := f.prepareIntents([]any{f.bondIntent("field-bond", "bond.configure", "balance-tcp", "active", "enabled"), vlan})
		f.proxy.mu.Lock()
		f.proxy.before = func() { f.vs("set", "Port", "field-bond", "lacp=passive") }
		f.proxy.mu.Unlock()
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || f.vs("get", "Port", "field-p1", "tag") != "10" || f.vs("get", "Port", "field-bond", "bond_mode") != "active-backup" {
			t.Fatal("partial native transaction", r)
		}
	})
	t.Run("lost_reply_recovers_original_commit_without_replay", func(t *testing.T) {
		f := newBondFixture(t)
		in := f.prepareIntents([]any{f.bondIntent("field-bond", "bond.configure", "balance-tcp", "active", "enabled")})
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Commit != "unknown" {
			t.Fatal(r)
		}
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Target != nil || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 1 {
			t.Fatal("invented target or repeated write", r)
		}
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("recovery replayed native mutation")
		}
	})
	t.Run("field_capability_revocation_compensates_admitted_bond", func(t *testing.T) {
		f := newBondFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{f.bondIntent("field-bond", "bond.configure", "balance-tcp", "active", "enabled")}))
		f.waitSafety(id, "awaiting-confirmation")
		f.removeBondCapability()
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Port", "field-bond", "lacp") != "off" || f.vs("get", "Port", "field-bond", "bond_mode") != "active-backup" || f.proxy.sent.Load() != 2 {
			t.Fatal("revocation did not restore original fields")
		}
	})
	t.Run("external_overlap_blocks_rollback_without_overwrite", func(t *testing.T) {
		f := newBondFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{f.bondIntent("field-bond", "bond.configure", "balance-tcp", "active", "enabled")}))
		f.waitSafety(id, "awaiting-confirmation")
		f.vs("set", "Port", "field-bond", "lacp=passive")
		b := f.binding("field-bond")
		waitFor(t, func() bool {
			v, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			return err == nil && v.Ports[b.ManagementID].Bond.LACP != nil && *v.Ports[b.ManagementID].Bond.LACP == "passive"
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.vs("get", "Port", "field-bond", "lacp") != "passive" || f.proxy.sent.Load() != 1 {
			t.Fatal("external field overwritten")
		}
	})
	t.Run("member_removal_invalidates_saved_identity_dependencies", func(t *testing.T) {
		f := newBondFixture(t)
		in := f.prepareIntents([]any{f.bondIntent("field-bond", "bond.configure", "balance-tcp", "active", "enabled")})
		member := f.vs("get", "Interface", "field-b2", "_uuid")
		f.vs("remove", "Port", "field-bond", "interfaces", member)
		b := f.binding("field-bond")
		waitFor(t, func() bool {
			v, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			return err == nil && len(v.Ports[b.ManagementID].Members) == 1
		})
		if _, err := f.executor.Prepare(f.ctx, repository.NewID(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", in.Envelope); err == nil {
			t.Fatal("member replacement admitted old plan")
		}
		if f.proxy.sent.Load() != 0 {
			t.Fatal("invalidated plan mutated OVS")
		}
	})
}
