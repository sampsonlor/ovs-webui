//go:build linux

package fieldexecution

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

func (f qosRateFixture) detachment() execution.Request {
	f.t.Helper()
	must(f.t, f.inventory.SetLocalQoSDetach([]string{f.port.ManagementID + ":" + f.qosObject.ManagementID}))
	return f.prepareIntents([]any{map[string]any{
		"intent_id": repository.NewID(), "operation": candidate.QoSDetach, "object": f.port, "qos": f.qosObject,
	}})
}

func (f qosRateFixture) detachmentSample(unbound bool) inventory.QoSRuntimeSample {
	f.t.Helper()
	var sample inventory.QoSRuntimeSample
	var observed candidate.QoSPort
	var err error
	defer func() {
		if f.t.Failed() {
			f.t.Logf("QoS detachment: unbound=%t reason=%s scheduler=%s root=%s error=%v",
				observed.Unbound, observed.Reason, sample.Scheduler, sample.RootKind, err)
			for _, object := range []string{"qdisc", "class"} {
				f.t.Logf("fixture %s: %s", object, f.run("tc", "-d", "-j", object, "show", "dev", f.target))
			}
		}
	}()
	waitFor(f.t, func() bool {
		view, viewErr := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{f.port})
		if viewErr != nil {
			err = viewErr
			return false
		}
		observed = view.QoSTarget(candidate.QoSDetach, f.port)
		sample, err = f.inventory.QoSKernelEvidence(f.ctx, candidate.QoSDetach, observed)
		if err != nil || observed.Unbound != unbound {
			return false
		}
		if unbound {
			return sample.Scheduler == "unshaped" && sample.RootKind == "noqueue"
		}
		return inventory.QoSRatesMatch(sample.Rates, f.original, observed.MTU)
	})
	return sample
}

func TestNativeQoSDetachment(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires an explicit disposable Linux QoS fixture")
	}
	t.Run("confirmed_detachment_retains_native_objects", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-hfsc")
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.detachment()
		before := f.detachmentSample(false)
		if f.proxy.sent.Load() != 0 || f.vs("get", "Port", f.target, "qos") != f.qos {
			t.Fatal("staging and validation wrote to the Port")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		detached := f.detachmentSample(true)
		f.run("ip", "netns", "exec", f.tx, "ping", "-n", "-c", "2", "-i", "0.1", "-W", "1", "198.18.72.2")
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		if detached.LinkDigest != before.LinkDigest || f.proxy.sent.Load() != 1 ||
			f.vs("get", "Port", f.target, "qos") != "[]" || f.vs("get", "QoS", f.qos, "queues:7") != f.queue ||
			f.vs("get", "Queue", f.queue, "dscp") != "0" ||
			f.vs("get", "QoS", f.qos, "other_config:fixture-opaque") != "keep" ||
			f.vs("get", "Queue", f.queue, "other_config:fixture-opaque") != "keep" {
			t.Fatal("detachment changed the device, retained native rows or metadata")
		}
	})
	t.Run("rollback_reinstalls_exact_original_scheduler", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-hfsc")
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.detachment()
		original := f.detachmentSample(false)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.detachmentSample(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.assertQueueTraffic(f.original)
		if restored := f.detachmentSample(false); restored.ConfigurationDigest != original.ConfigurationDigest ||
			f.proxy.sent.Load() != 2 || f.vs("get", "Port", f.target, "qos") != f.qos {
			t.Fatal("rollback did not restore the original binding and scheduler")
		}
	})
	t.Run("late_sharing_blocks_dispatch_transaction", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-hfsc")
		in := f.detachment()
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("test-qos-detach"), in.Envelope)
		must(t, err)
		f.proxy.hook(func() { f.vs("set", "Port", "field-p1", "qos="+f.qos) })
		out := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if out.Commit != "rejected" || f.proxy.sent.Load() != 1 || f.vs("get", "Port", f.target, "qos") != f.qos {
			t.Fatal("late Port sharing did not fence detachment", out)
		}
	})
	t.Run("foreign_leaf_blocks_before_dispatch", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-hfsc")
		in := f.detachment()
		f.run("tc", "qdisc", "replace", "dev", f.target, "parent", "1:8", "handle", "18:", "pfifo", "limit", "7")
		_, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("test-qos-detach"), in.Envelope)
		if err == nil || f.proxy.sent.Load() != 0 || f.vs("get", "Port", f.target, "qos") != f.qos {
			t.Fatal("foreign qdisc should block before dispatch", err)
		}
	})
	t.Run("deadline_restores_original_binding_while_daemon_paused", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-hfsc")
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.detachment()
		original := f.detachmentSample(false)
		pid := f.pid("switch")
		must(t, syscall.Kill(pid, syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool {
			return r.Outcome.Commit == "committed" && r.Outcome.Reason == "awaiting-ovs-vswitchd"
		})
		if s := f.safeState(id); s.Confirmation != nil || s.State == "awaiting-confirmation" {
			t.Fatal("database-only detachment entered confirmation")
		}
		_, err := f.executor.PrepareRollback(f.ctx, r.Plan, candidate.Digest("test-qos-detach-preflight"))
		must(t, err)
		offset.Store(int64(safety.PreparationBudget + time.Second))
		waitFor(t, func() bool {
			must(t, f.engine.SafetyTick(f.ctx))
			return f.proxy.sent.Load() == 2
		})
		if f.vs("get", "Port", f.target, "qos") != f.qos {
			t.Fatal("paused daemon lost the original binding")
		}
		must(t, syscall.Kill(pid, syscall.SIGCONT))
		f.waitSafety(id, "rolled-back")
		if f.detachmentSample(false).ConfigurationDigest != original.ConfigurationDigest {
			t.Fatal("paused daemon recovery did not restore the original scheduler")
		}
	})
}
