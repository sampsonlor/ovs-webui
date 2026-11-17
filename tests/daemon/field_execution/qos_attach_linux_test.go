//go:build linux

package fieldexecution

import (
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

func newQoSAttachmentFixture(t *testing.T) qosRateFixture {
	t.Helper()
	f := newQoSRateFixture(t, "linux-hfsc")
	f.vs("clear", "Port", f.target, "qos")
	f.waitNoQoS()
	return f
}

func (f qosRateFixture) waitNoQoS() {
	f.t.Helper()
	waitFor(f.t, func() bool {
		// Read the text header, since some tc versions return malformed JSON for
		// HFSC options during the transition.
		qdiscs := strings.TrimSpace(f.run("tc", "qdisc", "show", "dev", f.target))
		return strings.HasPrefix(qdiscs, "qdisc noqueue 0: root") && !strings.Contains(qdiscs, "\n") &&
			f.vs("get", "Port", f.target, "qos") == "[]"
	})
}

func (f qosRateFixture) attachment() execution.Request {
	f.t.Helper()
	must(f.t, f.inventory.SetLocalQoSAttach([]string{f.port.ManagementID + ":" + f.qosObject.ManagementID}))
	// Stage only after the manager has consumed the external OVSDB change
	// that set up this fixture. The live kernel can reach noqueue sooner.
	waitFor(f.t, func() bool {
		view, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{f.port})
		target := view.QoSTarget(candidate.QoSAttach, f.port)
		return err == nil && target.Unbound && target.Reason == "" && target.QoS == f.qosObject
	})
	return f.prepareIntents([]any{map[string]any{
		"intent_id": repository.NewID(), "operation": candidate.QoSAttach, "object": f.port, "qos": f.qosObject,
	}})
}

func (f qosRateFixture) qosConfiguration() string {
	f.t.Helper()
	return f.vs("get", "QoS", f.qos, "type", "queues", "other_config", "external_ids") + "\n" +
		f.vs("get", "Queue", f.queue, "dscp", "other_config", "external_ids")
}

func TestNativeQoSAttachment(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires an explicit disposable Linux QoS fixture")
	}
	t.Run("confirmed_attachment_installs_saved_rates_and_carries_traffic", func(t *testing.T) {
		f := newQoSAttachmentFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		before := f.qosConfiguration()
		in := f.attachment()
		f.waitNoQoS()
		if f.proxy.sent.Load() != 0 {
			t.Fatal("staging and validation changed the Port binding")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.assertQueueTraffic(f.original)
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		if f.proxy.sent.Load() != 1 || f.vs("get", "Port", f.target, "qos") != f.qos || f.qosConfiguration() != before {
			t.Fatal("confirmation did not retain the exact native QoS and Queue configuration")
		}
	})
	t.Run("rollback_restores_unbound_noqueue_and_retains_configuration", func(t *testing.T) {
		f := newQoSAttachmentFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		before := f.qosConfiguration()
		id := f.safeApply(f.attachment())
		f.waitSafety(id, "awaiting-confirmation")
		f.waitRates(f.original)
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.waitNoQoS()
		f.run("ip", "netns", "exec", f.tx, "ping", "-n", "-c", "2", "-i", "0.1", "-W", "1", "198.18.72.2")
		if f.proxy.sent.Load() != 2 || f.qosConfiguration() != before {
			t.Fatal("rollback changed the retained QoS or Queue configuration")
		}
	})
	t.Run("rate_and_detachment_grants_do_not_authorize_attachment", func(t *testing.T) {
		f := newQoSAttachmentFixture(t)
		must(t, f.inventory.SetLocalQoSDetach([]string{f.port.ManagementID + ":" + f.qosObject.ManagementID}))
		contract, err := apicontract.New()
		must(t, err)
		op, path, _ := contract.Match("GET", "/api/v1/qos/"+f.qosObject.ManagementID)
		available := func() bool {
			detail, err := f.inventory.Read(f.ctx, op.ID, path, nil, f.login.Claims)
			must(t, err)
			body, err := json.Marshal(detail)
			must(t, err)
			must(t, op.ValidateResponse(200, body))
			var resource struct {
				Attachment struct {
					Available bool `json:"available"`
				} `json:"qos_attach"`
			}
			must(t, json.Unmarshal(body, &resource))
			return resource.Attachment.Available
		}
		if available() {
			t.Fatal("rate and detach grants offered attachment")
		}
		in := f.attachment()
		if !available() {
			t.Fatal("independent exact-pair attachment grant did not offer attachment")
		}
		must(t, f.inventory.SetLocalQoSAttach(nil))
		_, err = f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("revoked-qos-attachment-grant"), in.Envelope)
		if err == nil || f.proxy.sent.Load() != 0 {
			t.Fatal("attachment was accepted after removing its exact-pair grant", err)
		}
	})
	t.Run("late_queue_sharing_blocks_atomic_binding", func(t *testing.T) {
		f := newQoSAttachmentFixture(t)
		in := f.attachment()
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("qos-attachment-atomic-wait"), in.Envelope)
		must(t, err)
		f.proxy.hook(func() { f.vs("--no-wait", "create", "QoS", "type=linux-hfsc", "queues:9="+f.queue) })
		out := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if out.Commit != "rejected" || f.proxy.sent.Load() != 1 || f.vs("get", "Port", f.target, "qos") != "[]" {
			t.Fatal("native transaction attached a QoS whose Queue had become shared", out)
		}
	})
	t.Run("foreign_qdisc_blocks_dispatch", func(t *testing.T) {
		f := newQoSAttachmentFixture(t)
		in := f.attachment()
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("qos-attachment-qdisc-fence"), in.Envelope)
		must(t, err)
		f.run("tc", "qdisc", "replace", "dev", f.target, "root", "handle", "18:", "pfifo", "limit", "7")
		out := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if out.Commit != "rejected" || f.proxy.sent.Load() != 0 || f.vs("get", "Port", f.target, "qos") != "[]" {
			t.Fatal("attachment attempted to replace a foreign kernel qdisc", out)
		}
	})
	t.Run("deadline_clears_binding_while_daemon_has_not_installed_it", func(t *testing.T) {
		f := newQoSAttachmentFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		before := f.qosConfiguration()
		in := f.attachment()
		pid := f.pid("switch")
		must(t, syscall.Kill(pid, syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool {
			return r.Outcome.Commit == "committed" && r.Outcome.Reason == "awaiting-ovs-vswitchd"
		})
		if s := f.safeState(id); s.Confirmation != nil || s.State == "awaiting-confirmation" {
			t.Fatal("database-only attachment enabled confirmation")
		}
		_, err := f.executor.PrepareRollback(f.ctx, r.Plan, candidate.Digest("qos-attachment-deadline-preflight"))
		must(t, err)
		offset.Store(int64(safety.PreparationBudget + time.Second))
		waitFor(t, func() bool {
			must(t, f.engine.SafetyTick(f.ctx))
			return f.proxy.sent.Load() == 2
		})
		f.waitNoQoS()
		must(t, syscall.Kill(pid, syscall.SIGCONT))
		f.waitSafety(id, "rolled-back")
		f.waitNoQoS()
		if f.qosConfiguration() != before {
			t.Fatal("paused-daemon recovery modified the saved QoS or Queue configuration")
		}
	})
}
