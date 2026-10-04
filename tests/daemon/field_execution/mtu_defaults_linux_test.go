//go:build linux

package fieldexecution

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func nativeMTUDefaultFixture(t *testing.T, automatic bool) (*fixture, string, string, candidate.Binding) {
	f, name, b := nativeMTUFixture(t)
	peer := "md-" + repository.NewID()[:8]
	parent := f.vs("port-to-br", name)
	f.vs("add-port", parent, peer, "--", "set", "Interface", peer, "type=internal", "mtu_request=1800", "--", "set", "Interface", name, "mtu_request=2400")
	if automatic {
		f.vs("clear", "Interface", name, "mtu_request")
	}
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
		p := s.Interfaces[b.ManagementID]
		return err == nil && p.Default != nil && p.Default.MTU == 1800 && candidate.MTUEditable(p) && (automatic && p.Requested == nil && p.Observed == 1800 || !automatic && p.Requested != nil && *p.Requested == 2400 && p.Observed == 2400)
	})
	return f, name, peer, b
}
func mtuClearIntent(b candidate.Binding) map[string]any {
	return map[string]any{"intent_id": repository.NewID(), "operation": candidate.InterfaceMTUClear, "object": b}
}

func TestNativeInterfaceMTUDefaults(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated automatic Interface MTU matrix")
	}
	t.Run("set_from_automatic_rollback_restores_empty_request_and_device", func(t *testing.T) {
		f, name, _, b := nativeMTUDefaultFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{mtuIntent(b, 2400)})
		if in.Envelope.Candidate.Intents[0].MTU.Before != nil || f.vs("get", "Interface", name, "mtu_request") != "[]" || f.proxy.sent.Load() != 0 {
			t.Fatal("empty original normalized or draft wrote")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Interface", name, "mtu_request") != "[]" || f.vs("get", "Interface", name, "mtu") != "1800" || f.vs("get", "Interface", name, "_uuid") != b.OVSUUID || f.proxy.sent.Load() != 2 {
			t.Fatal("automatic original not precisely restored")
		}
	})
	for _, decision := range []string{"confirm", "rollback"} {
		t.Run("clear_to_automatic_"+decision, func(t *testing.T) {
			f, name, _, b := nativeMTUDefaultFixture(t, false)
			var offset atomic.Int64
			f.configureSafety(&offset)
			in := f.prepareIntents([]any{mtuClearIntent(b)})
			if f.proxy.sent.Load() != 0 {
				t.Fatal("draft wrote")
			}
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			if f.vs("get", "Interface", name, "mtu_request") != "[]" || f.vs("get", "Interface", name, "mtu") != "1800" {
				t.Fatal("automatic device value unproven")
			}
			f.decide(id, decision)
			want := "confirmed"
			writes := int32(1)
			if decision == "rollback" {
				want = "rolled-back"
				writes = 2
			}
			f.waitSafety(id, want)
			if decision == "rollback" && (f.vs("get", "Interface", name, "mtu_request") != "2400" || f.vs("get", "Interface", name, "mtu") != "2400") {
				t.Fatal("explicit original not restored")
			}
			must(t, f.engine.Recover(f.ctx))
			if f.proxy.sent.Load() != writes {
				t.Fatal("write replayed")
			}
		})
	}
	t.Run("late_peer_mtu_change_aborts_atomic_dispatch", func(t *testing.T) {
		f, name, peer, b := nativeMTUDefaultFixture(t, false)
		in := f.prepareIntents([]any{mtuClearIntent(b)})
		f.proxy.hook(func() { f.vs("set", "Interface", peer, "mtu_request=2600") })
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || f.vs("get", "Interface", name, "mtu_request") != "2400" {
			t.Fatal("late default dependency overwritten", r)
		}
	})
	t.Run("original_device_drift_rejects_validated_execution", func(t *testing.T) {
		f, name, _, b := nativeMTUDefaultFixture(t, true)
		in := f.prepareIntents([]any{mtuIntent(b, 2400)})
		subject := publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}
		lease, err := f.workspace.ReserveExecution(f.ctx, subject, in, isolatedSafety{f})
		must(t, err)
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(f.pid("switch"), syscall.SIGCONT) })
		// Fault the native observation while its publisher is paused. Kernel
		// mismatch is independently covered by the formal browser fixture.
		f.vs("set", "Interface", name, "mtu=1500")
		waitFor(t, func() bool {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			return err == nil && s.Interfaces[b.ManagementID].Observed == 1500
		})
		_, err = f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), lease)
		if err == nil || f.proxy.sent.Load() != 0 || f.vs("get", "Interface", name, "mtu_request") != "[]" {
			t.Fatal("original device drift inherited validated execution", err)
		}
	})
	t.Run("late_original_device_drift_aborts_atomic_dispatch", func(t *testing.T) {
		f, name, _, b := nativeMTUDefaultFixture(t, true)
		in := f.prepareIntents([]any{mtuIntent(b, 2400)})
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(f.pid("switch"), syscall.SIGCONT) })
		f.proxy.hook(func() { f.vs("set", "Interface", name, "mtu=1500") })
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || f.vs("get", "Interface", name, "mtu_request") != "[]" {
			t.Fatal("late original device drift overwritten", r)
		}
	})
	t.Run("peer_drift_blocks_confirmation_and_compensation", func(t *testing.T) {
		f, _, peer, b := nativeMTUDefaultFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2400)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.vs("set", "Interface", peer, "mtu_request=2600")
		waitFor(t, func() bool {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			p := s.Interfaces[b.ManagementID]
			return err == nil && p.Default != nil && p.Default.MTU == 2600
		})
		r, err := f.engine.Read(f.ctx, id)
		must(t, err)
		req := apitypes.RequestID(time.Now())
		body, _ := json.Marshal(map[string]any{"request_id": req, "expected_sequence": r.Sequence, "decision": "confirm"})
		_, err = f.auth.ExecuteAuth(f.ctx, f.login.Grant, authn.Command{Method: "POST", URI: "/api/v1/transactions/" + id + "/decisions", Epoch: f.login.Claims.RequestEpoch, RequestID: req, Payload: body})
		if err == nil || f.safeState(id).State == "confirmed" {
			t.Fatal("changed default confirmed", err)
		}
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.proxy.sent.Load() != 1 {
			t.Fatal("changed default overwritten")
		}
	})
	for _, automatic := range []bool{false, true} {
		name := "clear"
		if automatic {
			name = "set-from-automatic"
		}
		t.Run("lost_"+name+"_reply_does_not_replay_or_invent_target", func(t *testing.T) {
			f, _, _, b := nativeMTUDefaultFixture(t, automatic)
			intent := mtuClearIntent(b)
			if automatic {
				intent = mtuIntent(b, 2400)
			}
			in := f.prepareIntents([]any{intent})
			f.proxy.dropReply.Store(true)
			r := f.submit(in)
			if r.Outcome.Commit != "unknown" {
				t.Fatal(r)
			}
			must(t, f.engine.Recover(f.ctx))
			r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
			if r.Outcome.Applied != "unknown" || r.Outcome.Target != nil || f.proxy.sent.Load() != 1 {
				t.Fatal("unknown result normalized", r)
			}
		})
	}
	t.Run("lost_automatic_compensation_reply_requires_recovery", func(t *testing.T) {
		f, _, _, b := nativeMTUDefaultFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2400)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		if f.proxy.sent.Load() != 2 {
			t.Fatal("compensation replayed")
		}
	})
	t.Run("paused_daemon_does_not_prove_automatic_device_mtu", func(t *testing.T) {
		f, name, _, b := nativeMTUDefaultFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents([]any{mtuClearIntent(b)})
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(f.pid("switch"), syscall.SIGCONT) })
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		must(t, f.engine.SafetyTick(f.ctx))
		if r.Outcome.Applied == "applied" || f.safeState(id).Confirmation != nil || f.vs("get", "Interface", name, "mtu") != "2400" {
			t.Fatal("automatic confirmation fabricated")
		}
		f.decide(id, "rollback")
		waitFor(t, func() bool {
			must(t, f.engine.SafetyTick(f.ctx))
			return f.proxy.sent.Load() == 2 && f.vs("get", "Interface", name, "mtu_request") == "2400"
		})
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGCONT))
		f.waitSafety(id, "rolled-back")
	})
	t.Run("same_name_peer_replacement_blocks_old_compensation", func(t *testing.T) {
		f, name, peer, b := nativeMTUDefaultFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]any{mtuIntent(b, 2400)}))
		f.waitSafety(id, "awaiting-confirmation")
		old := f.vs("get", "Interface", peer, "_uuid")
		parent := f.vs("port-to-br", name)
		f.vs("del-port", parent, peer, "--", "add-port", parent, peer, "--", "set", "Interface", peer, "type=internal", "mtu_request=1800")
		waitFor(t, func() bool {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			p := s.Interfaces[b.ManagementID]
			if err != nil || p.Default == nil {
				return false
			}
			for _, binding := range p.Default.Bindings {
				if binding.OVSUUID == old {
					return false
				}
			}
			return true
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.proxy.sent.Load() != 1 {
			t.Fatal("new peer inherited old recovery")
		}
	})
	for _, kind := range []string{"root-revocation", "missing-contributor"} {
		t.Run(kind+"_rejects_clear_execution", func(t *testing.T) {
			f, _, peer, b := nativeMTUDefaultFixture(t, false)
			in := f.prepareIntents([]any{mtuClearIntent(b)})
			subject := publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}
			lease, err := f.workspace.ReserveExecution(f.ctx, subject, in, isolatedSafety{f})
			must(t, err)
			if kind == "root-revocation" {
				must(t, f.inventory.SetLocalMTUInterfaces(nil))
			} else {
				f.vs("clear", "Interface", peer, "mtu_request")
				waitFor(t, func() bool {
					s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
					return err == nil && s.Interfaces[b.ManagementID].Default == nil
				})
			}
			_, err = f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), lease)
			if err == nil || f.proxy.sent.Load() != 0 {
				t.Fatal("invalid authority/default executed", err)
			}
		})
	}
}
