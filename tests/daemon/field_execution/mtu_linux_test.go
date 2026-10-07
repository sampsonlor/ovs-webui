//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
)

func nativeMTUFixture(t *testing.T) (*fixture, string, candidate.Binding) {
	f, parent := newBridgeFixture(t, false)
	name := "mi-" + repository.NewID()[:8]
	f.vs("add-br", parent, "--", "set", "Bridge", parent, "datapath_type=system", "--", "add-port", parent, name, "--", "set", "Interface", name, "type=internal", "mtu_request=1500", "external_ids:unrelated=keep")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", parent) })
	uuid := f.vs("get", "Interface", name, "_uuid")
	var b candidate.Binding
	waitFor(t, func() bool {
		return f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Interface' AND state='active' AND ovs_uuid=?", uuid).Scan(&b.ManagementID, &b.OVSUUID, &b.Table, &b.Generation)
		}) == nil
	})
	must(t, f.inventory.SetLocalMTUInterfaces([]string{b.ManagementID}))
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
		return err == nil && candidate.MTUEditable(s.Interfaces[b.ManagementID]) && f.vs("get", "Interface", name, "mtu") == "1500"
	})
	return f, name, b
}
func mtuIntent(b candidate.Binding, n int) map[string]any {
	return map[string]any{"intent_id": repository.NewID(), "operation": candidate.InterfaceMTUSet, "object": b, "mtu_request": n}
}

// The shared public read model must associate the actual admitted Interface,
// while preserving transaction/job identity and each independent outcome.
func (f *fixture) assertMTUEvidence(target, transaction, state string) {
	f.t.Helper()
	must(f.t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		for _, op := range []string{"listEvents", "listAudit"} {
			value, err := evidence.Read(ctx, q, f.login.Claims, op, nil, url.Values{"object_id": {target}}, make([]byte, 32), time.Now())
			if err != nil {
				return err
			}
			records := value.(map[string]any)["items"].([]map[string]any)
			created, terminal, decision := false, false, false
			for _, r := range records {
				if r["transaction_id"] != transaction {
					continue
				}
				refs := r["object_refs"].([]apitypes.Ref)
				exact := false
				for _, ref := range refs {
					exact = exact || ref.Kind == "interface" && ref.ID == target
				}
				if !exact {
					f.t.Fatal("scoped record lacks exact immutable target")
				}
				if op == "listEvents" {
					created = created || r["operation"] == "job-created"
					terminal = terminal || r["result"] == "succeeded" || r["result"] == "failed"
				} else {
					created = created || r["operation"] == "execute-fields"
					terminal = terminal || r["operation"] == "safe-apply-state" && r["result"] == state
					decision = decision || r["operation"] == "safe-apply-decision"
				}
			}
			if !created || !terminal || op == "listAudit" && !decision {
				f.t.Fatal("incomplete object-associated MTU lifecycle", op, state)
			}
		}
		return nil
	}))
}
func TestNativeInterfaceMTU(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated Interface MTU matrix")
	}
	t.Run("stage_confirm_and_actual_mtu_proof", func(t *testing.T) {
		f, name, b := nativeMTUFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{mtuIntent(b, 2000)})
		if f.proxy.sent.Load() != 0 || f.vs("get", "Interface", name, "mtu_request") != "1500" {
			t.Fatal("draft wrote live fields")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		if f.vs("get", "Interface", name, "mtu") != "2000" || f.vs("get", "Interface", name, "_uuid") != b.OVSUUID {
			t.Fatal("actual MTU/identity unproven")
		}
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		f.assertMTUEvidence(b.ManagementID, id, "confirmed")
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("write replayed")
		}
	})
	t.Run("rollback_restores_explicit_request_and_preserves_unknown_fields", func(t *testing.T) {
		f, name, b := nativeMTUFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2000)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.vs("set", "Interface", name, "external_ids:added=preserve", "other_config:opaque=preserve")
		// Establish the new metadata baseline before testing compensation. The
		// atomic guard must still reject an unobserved change at dispatch.
		waitFor(t, func() bool {
			v, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{b})
			if err != nil {
				return false
			}
			labels, _ := v.Observation.Rows["Interface"][b.OVSUUID].Values["external_ids"].(map[string]any)
			return labels["added"] == "preserve"
		})
		defer func() {
			if t.Failed() {
				s := f.safeState(id)
				t.Logf("compensation: state=%s reason=%s outcome=%+v request=%s actual=%s writes=%d", s.State, s.Reason, s.Outcome, f.vs("get", "Interface", name, "mtu_request"), f.vs("get", "Interface", name, "mtu"), f.proxy.sent.Load())
			}
		}()
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.assertMTUEvidence(b.ManagementID, id, "rolled-back")
		if f.vs("get", "Interface", name, "mtu_request") != "1500" || f.vs("get", "Interface", name, "mtu") != "1500" || f.vs("get", "Interface", name, "_uuid") != b.OVSUUID || !strings.Contains(f.vs("get", "Interface", name, "external_ids"), "added=preserve") || f.vs("get", "Interface", name, "other_config") != "{opaque=preserve}" {
			t.Fatal("exact compensation failed")
		}
		if f.proxy.sent.Load() != 2 {
			t.Fatal("compensation replayed", f.proxy.sent.Load())
		}
	})
	t.Run("late_external_request_aborts_atomic_dispatch", func(t *testing.T) {
		f, name, b := nativeMTUFixture(t)
		in := f.prepareIntents([]any{mtuIntent(b, 2000)})
		f.proxy.hook(func() { f.vs("set", "Interface", name, "mtu_request=1800") })
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || f.vs("get", "Interface", name, "mtu_request") != "1800" {
			t.Fatal("late external writer overwritten", r)
		}
	})
	for _, change := range []string{"mtu_request=1800", "options:unpublished=opaque", "external_ids:iface-id=external"} {
		t.Run("external_change_blocks_compensation_"+change, func(t *testing.T) {
			f, name, b := nativeMTUFixture(t)
			var offset atomic.Int64
			f.configureSafety(&offset)
			id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2000)}))
			f.waitSafety(id, "awaiting-confirmation")
			f.vs("set", "Interface", name, change)
			waitFor(t, func() bool {
				s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
				p := s.Interfaces[b.ManagementID]
				return err == nil && (p.Requested != nil && *p.Requested == 1800 || !p.Eligible || p.Authority == "externally-controlled")
			})
			f.decide(id, "rollback")
			f.waitSafety(id, "rollback-conflict")
			if f.proxy.sent.Load() != 1 {
				t.Fatal("external state overwritten")
			}
		})
	}
	t.Run("lost_reply_reconciles_marker_without_replay_or_invented_target", func(t *testing.T) {
		f, name, b := nativeMTUFixture(t)
		in := f.prepareIntents([]any{mtuIntent(b, 2000)})
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Commit != "unknown" {
			t.Fatal(r)
		}
		must(t, f.engine.Recover(f.ctx))
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Commit != "committed" || r.Outcome.Applied != "unknown" || r.Outcome.Target != nil || f.proxy.sent.Load() != 1 || f.vs("get", "Interface", name, "mtu_request") != "2000" {
			t.Fatal("unknown target manufactured", r)
		}
	})
	t.Run("paused_daemon_does_not_open_confirmation", func(t *testing.T) {
		f, name, b := nativeMTUFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{mtuIntent(b, 2000)})
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(f.pid("switch"), syscall.SIGCONT) })
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		must(t, f.engine.SafetyTick(f.ctx))
		s := f.safeState(id)
		if r.Outcome.Applied == "applied" || s.Confirmation != nil || s.State == "awaiting-confirmation" || f.vs("get", "Interface", name, "mtu") != "1500" {
			t.Fatal("confirmation opened without actual MTU", s)
		}
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGCONT))
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
	})
	t.Run("lost_compensation_reply_requires_recovery", func(t *testing.T) {
		f, _, b := nativeMTUFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2000)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		if f.proxy.sent.Load() != 2 {
			t.Fatal("compensation replayed")
		}
	})
	t.Run("same_name_replacement_does_not_inherit_compensation", func(t *testing.T) {
		f, name, b := nativeMTUFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2000)}))
		f.waitSafety(id, "awaiting-confirmation")
		parent := f.vs("port-to-br", name)
		f.vs("del-port", parent, name, "--", "add-port", parent, name, "--", "set", "Interface", name, "type=internal", "mtu_request=1800")
		waitFor(t, func() bool {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			_, present := s.Interfaces[b.ManagementID]
			return err == nil && !present
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.proxy.sent.Load() != 1 || f.vs("get", "Interface", name, "mtu_request") != "1800" || f.vs("get", "Interface", name, "_uuid") == b.OVSUUID {
			t.Fatal("same-name object adopted")
		}
	})
	t.Run("revoked_root_authority_rejects_execution", func(t *testing.T) {
		f, _, b := nativeMTUFixture(t)
		in := f.prepareIntents([]any{mtuIntent(b, 2000)})
		subject := publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}
		lease, err := f.workspace.ReserveExecution(f.ctx, subject, in, isolatedSafety{f})
		must(t, err)
		must(t, f.inventory.SetLocalMTUInterfaces(nil))
		_, err = f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), lease)
		if err == nil || f.proxy.sent.Load() != 0 {
			t.Fatal("root authority revocation bypassed", err)
		}
	})
}
