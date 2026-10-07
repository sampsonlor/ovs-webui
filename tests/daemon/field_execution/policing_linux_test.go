//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	linuxprovider "github.com/sampsonlor/ovs-webui/internal/provider/linux"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func nativePolicingFixture(t *testing.T) (*fixture, string, candidate.Binding) {
	f, parent := newBridgeFixture(t, true)
	name := "pi-" + repository.NewID()[:8]
	f.vs("add-br", parent, "--", "set", "Bridge", parent, "datapath_type=system", "--", "add-port", parent, name, "--", "set", "Interface", name, "type=internal", "external_ids:unrelated=keep")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", parent) })
	uuid := f.vs("get", "Interface", name, "_uuid")
	var b candidate.Binding
	waitFor(t, func() bool {
		return f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Interface' AND state='active' AND ovs_uuid=?", uuid).Scan(&b.ManagementID, &b.OVSUUID, &b.Table, &b.Generation)
		}) == nil
	})
	f.inventory.SetPolicingObserver(linuxprovider.New())
	must(t, f.inventory.SetLocalPolicingInterfaces([]string{b.ManagementID}))
	f.waitPolicing(b, candidate.PolicingConfig{})
	return f, name, b
}
func (f *fixture) waitPolicing(b candidate.Binding, want candidate.PolicingConfig) {
	f.t.Helper()
	var last inventory.PolicingSample
	var lastErr error
	var native candidate.InterfacePolicing
	defer func() {
		if f.t.Failed() {
			f.t.Logf("policing qualification: native=%+v sample=%+v err=%v", native, last, lastErr)
		}
	}()
	waitFor(f.t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
		if err != nil {
			lastErr = err
			return false
		}
		native = s.Policings[b.ManagementID]
		last, lastErr = f.inventory.PolicingEvidence(f.ctx, native)
		return lastErr == nil && native.Configuration == want && inventory.PolicingMatches(last, want)
	})
}
func policingIntent(b candidate.Binding, mode string, rate int) map[string]any {
	return map[string]any{"intent_id": repository.NewID(), "operation": candidate.InterfacePolicingSet, "object": b, "policing": map[string]any{"mode": mode, "rate": rate}}
}
func TestNativeInterfacePolicing(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated controlled ingress policing matrix")
	}
	for _, mode := range []string{"bandwidth", "packets", "disabled"} {
		t.Run("stage_confirm_installed_"+mode, func(t *testing.T) {
			f, name, b := nativePolicingFixture(t)
			var offset atomic.Int64
			f.configureSafety(&offset)
			if mode == "disabled" {
				f.vs("set", "Interface", name, "ingress_policing_rate=1000")
				f.waitPolicing(b, candidate.PolicingConfig{Rate: 1000})
			}
			rate := 1000
			want := candidate.PolicingConfig{Rate: 1000}
			if mode == "packets" {
				rate = 5
				want = candidate.PolicingConfig{PacketRate: 5}
			}
			if mode == "disabled" {
				rate = 0
				want = candidate.PolicingConfig{}
			}
			before := f.vs("get", "Interface", name, "ingress_policing_rate")
			in := f.prepareIntents([]any{policingIntent(b, mode, rate)})
			if f.proxy.sent.Load() != 0 || f.vs("get", "Interface", name, "ingress_policing_rate") != before {
				t.Fatal("draft live write")
			}
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			f.waitPolicing(b, want)
			f.decide(id, "confirm")
			f.waitSafety(id, "confirmed")
			f.assertMTUEvidence(b.ManagementID, id, "confirmed")
			must(t, f.engine.Recover(f.ctx))
			if f.proxy.sent.Load() != 1 || f.vs("get", "Interface", name, "_uuid") != b.OVSUUID || f.vs("get", "Interface", name, "ingress_policing_burst") != "0" || f.vs("get", "Interface", name, "ingress_policing_kpkts_burst") != "0" {
				t.Fatal("replay, burst or identity changed")
			}
		})
	}
	t.Run("switch_rate_domain_and_rollback_exact_original", func(t *testing.T) {
		f, name, b := nativePolicingFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		f.vs("set", "Interface", name, "ingress_policing_rate=1000")
		f.waitPolicing(b, candidate.PolicingConfig{Rate: 1000})
		id := f.safeApply(f.prepareIntents([]any{policingIntent(b, "packets", 5)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.waitPolicing(b, candidate.PolicingConfig{PacketRate: 5})
		f.vs("set", "Interface", name, "external_ids:added=preserve", "other_config:opaque=preserve")
		waitFor(t, func() bool {
			v, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{b})
			if err != nil {
				return false
			}
			labels, _ := v.Observation.Rows["Interface"][b.OVSUUID].Values["external_ids"].(map[string]any)
			return labels["added"] == "preserve"
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.waitPolicing(b, candidate.PolicingConfig{Rate: 1000})
		f.assertMTUEvidence(b.ManagementID, id, "rolled-back")
		if f.proxy.sent.Load() != 2 || !strings.Contains(f.vs("get", "Interface", name, "external_ids"), "added=preserve") || f.vs("get", "Interface", name, "other_config") != "{opaque=preserve}" {
			t.Fatal("compensation replaced unrelated metadata")
		}
	})
	t.Run("late_native_change_rejects_atomic_dispatch", func(t *testing.T) {
		f, name, b := nativePolicingFixture(t)
		in := f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)})
		f.proxy.hook(func() { f.vs("set", "Interface", name, "ingress_policing_rate=1800") })
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || f.vs("get", "Interface", name, "ingress_policing_rate") != "1800" {
			t.Fatal("external request overwritten", r)
		}
	})
	for _, kind := range []string{"clsact", "foreign-rule", "jit-rule"} {
		t.Run("reject_"+kind+"_before_sending", func(t *testing.T) {
			f, name, b := nativePolicingFixture(t)
			in := f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)})
			var plan execution.Plan
			if kind == "jit-rule" {
				var err error
				plan, err = f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("synthetic-policing"), in.Envelope)
				must(t, err)
			}
			if kind == "clsact" {
				f.run("tc", "qdisc", "add", "dev", name, "clsact")
			} else {
				f.run("tc", "qdisc", "add", "dev", name, "handle", "ffff:", "ingress")
				f.run("tc", "filter", "add", "dev", name, "parent", "ffff:", "protocol", "all", "pref", "7", "matchall", "action", "drop")
			}
			if kind == "jit-rule" {
				called := false
				out := f.executor.Commit(f.ctx, plan, func() error { called = true; return nil })
				if called || out.Commit != "rejected" || out.Reason != "policing-dispatch-unproven" || f.proxy.sent.Load() != 0 {
					t.Fatal("kernel race sent transaction", out)
				}
			} else {
				if _, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("synthetic-policing"), in.Envelope); err == nil {
					t.Fatal("foreign ingress admitted")
				}
			}
			if f.vs("get", "Interface", name, "ingress_policing_rate") != "0" {
				t.Fatal("foreign rule replaced")
			}
		})
	}
	t.Run("foreign_rule_blocks_confirmation_and_compensation", func(t *testing.T) {
		f, name, b := nativePolicingFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.run("tc", "filter", "add", "dev", name, "parent", "ffff:", "protocol", "all", "pref", "7", "matchall", "action", "drop")
		r, err := f.engine.Read(f.ctx, id)
		must(t, err)
		req := apitypes.RequestID(time.Now())
		body, _ := json.Marshal(map[string]any{"request_id": req, "expected_sequence": r.Sequence, "decision": "confirm"})
		_, err = f.auth.ExecuteAuth(f.ctx, f.login.Grant, authn.Command{Method: "POST", URI: "/api/v1/transactions/" + id + "/decisions", Epoch: f.login.Claims.RequestEpoch, RequestID: req, Payload: body})
		var problem *apitypes.Problem
		if !errors.As(err, &problem) || problem.Code != "CONFIRMATION_EVIDENCE_UNAVAILABLE" || f.safeState(id).State == "confirmed" {
			t.Fatal("foreign ingress was confirmed", err)
		}
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.proxy.sent.Load() != 1 || f.vs("get", "Interface", name, "ingress_policing_rate") != "1000" {
			t.Fatal("foreign ingress overwritten")
		}
	})
	t.Run("paused_daemon_compensates_original_uninstalled_rule", func(t *testing.T) {
		f, name, b := nativePolicingFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)})
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(f.pid("switch"), syscall.SIGCONT) })
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool {
			return r.Outcome.Commit == "committed" && r.Outcome.Reason == "awaiting-ovs-vswitchd"
		})
		t.Cleanup(func() {
			if t.Failed() {
				s := f.safeState(id)
				t.Logf("paused policing recovery: state=%s reason=%s outcome=%+v writes=%d", s.State, s.Reason, s.Outcome, f.proxy.sent.Load())
			}
		})
		_, err := f.executor.PrepareRollback(f.ctx, r.Plan, candidate.Digest("synthetic-read-only-compensation-check"))
		must(t, err)
		must(t, f.engine.SafetyTick(f.ctx))
		s := f.safeState(id)
		if r.Outcome.Applied == "applied" || s.Confirmation != nil || s.State == "awaiting-confirmation" {
			t.Fatal("uninstalled policing opened confirmation", s)
		}
		offset.Store(int64(31 * time.Second))
		must(t, f.engine.SafetyTick(f.ctx))
		waitFor(t, func() bool { must(t, f.engine.SafetyTick(f.ctx)); return f.proxy.sent.Load() == 2 })
		if f.vs("get", "Interface", name, "ingress_policing_rate") != "0" {
			t.Fatal("original not restored while daemon paused")
		}
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGCONT))
		f.waitSafety(id, "rolled-back")
		f.waitPolicing(b, candidate.PolicingConfig{})
	})
	t.Run("lost_reply_reconciles_without_replay_or_invented_target", func(t *testing.T) {
		f, _, b := nativePolicingFixture(t)
		in := f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)})
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Commit != "unknown" {
			t.Fatal(r)
		}
		must(t, f.engine.Recover(f.ctx))
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Applied != "unknown" || r.Outcome.Target != nil || f.proxy.sent.Load() != 1 {
			t.Fatal("invented target or replay", r)
		}
	})
	t.Run("lost_compensation_reply_preserves_uncertainty", func(t *testing.T) {
		f, _, b := nativePolicingFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		waitFor(t, func() bool { must(t, f.engine.SafetyTick(f.ctx)); return f.safeState(id).Outcome.Commit == "committed" })
		s := f.safeState(id)
		if s.Outcome.Target != nil || s.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 2 {
			t.Fatal("invented rollback success", s)
		}
	})
	t.Run("same_name_replacement_cannot_inherit_compensation", func(t *testing.T) {
		f, name, b := nativePolicingFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)}))
		f.waitSafety(id, "awaiting-confirmation")
		parent := f.vs("port-to-br", name)
		f.vs("del-port", parent, name, "--", "add-port", parent, name, "--", "set", "Interface", name, "type=internal", "ingress_policing_rate=1800")
		waitFor(t, func() bool {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			_, present := s.Policings[b.ManagementID]
			return err == nil && !present
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.proxy.sent.Load() != 1 || f.vs("get", "Interface", name, "_uuid") == b.OVSUUID || f.vs("get", "Interface", name, "ingress_policing_rate") != "1800" {
			t.Fatal("replacement inherited old authority")
		}
	})
	t.Run("revoked_exclusive_root_grant_blocks_execution", func(t *testing.T) {
		f, _, b := nativePolicingFixture(t)
		in := f.prepareIntents([]any{policingIntent(b, "bandwidth", 1000)})
		lease, err := f.workspace.ReserveExecution(f.ctx, publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}, in, isolatedSafety{f})
		must(t, err)
		must(t, f.inventory.SetLocalPolicingInterfaces(nil))
		_, err = f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), lease)
		if err == nil || f.proxy.sent.Load() != 0 {
			t.Fatal("revoked root grant bypassed", err)
		}
	})
}
