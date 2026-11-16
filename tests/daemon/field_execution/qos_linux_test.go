//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	linuxprovider "github.com/sampsonlor/ovs-webui/internal/provider/linux"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

type qosRateFixture struct {
	*fixture
	port                   candidate.Binding
	qosObject              candidate.Binding
	target, queue, qos, tx string
	original, edited       candidate.QoSRates
}

func newQoSRateFixture(t *testing.T, discipline string) qosRateFixture {
	t.Helper()
	f, bridge := newBridgeFixture(t, true)
	suffix := repository.NewID()[:6]
	rx, tx := "qrx"+suffix, "qtx"+suffix
	sendNS, recvNS := "qos-tx-"+suffix, "qos-rx-"+suffix
	for _, node := range []struct{ port, ns, ip string }{
		{tx, sendNS, "198.18.72.1/24"}, {rx, recvNS, "198.18.72.2/24"},
	} {
		f.run("ip", "netns", "add", node.ns)
		t.Cleanup(func() { f.run("ip", "netns", "del", node.ns) })
		f.run("ip", "link", "add", node.port, "type", "veth", "peer", "name", "peer", "netns", node.ns)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", node.port).Run() })
		f.run("ip", "link", "set", node.port, "up")
		f.run("ip", "-n", node.ns, "link", "set", "peer", "up")
		f.run("ip", "-n", node.ns, "addr", "add", node.ip, "dev", "peer")
	}
	f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system", "--", "add-port", bridge, tx, "--", "add-port", bridge, rx)
	t.Cleanup(func() { f.vs("--if-exists", "del-br", bridge) })
	socket := filepath.Join(f.root, bridge+".mgmt")
	var ofport string
	waitFor(t, func() bool {
		info, err := os.Stat(socket)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return false
		}
		ofport = f.vs("get", "Interface", tx, "ofport")
		number, err := strconv.Atoi(ofport)
		return err == nil && number > 0
	})
	// Select Queue 7 using a fixture-only rule on this disposable Bridge.
	f.run("ovs-ofctl", "add-flow", "unix:"+socket, "priority=100,in_port="+ofport+",actions=set_queue:7,normal")
	f.vs("--", "--id=@queue", "create", "Queue", "other_config:min-rate=1000000", "other_config:max-rate=3000000",
		"other_config:fixture-opaque=keep", "external_ids:fixture=qos-write", "dscp=0",
		"--", "--id=@qos", "create", "QoS", "type="+discipline, "queues:7=@queue", "other_config:max-rate=8000000",
		"other_config:fixture-opaque=keep", "external_ids:fixture=qos-write",
		"--", "set", "Port", rx, "qos=@qos", "external_ids:fixture=qos-write")
	qos := f.vs("get", "Port", rx, "qos")
	queue := f.vs("get", "QoS", qos, "queues:7")
	var port candidate.Binding
	portUUID := f.vs("get", "Port", rx, "_uuid")
	var management, role string
	waitFor(t, func() bool {
		return f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			if err := q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Port' AND state='active' AND ovs_uuid=?", portUUID).
				Scan(&port.ManagementID, &port.OVSUUID, &port.Table, &port.Generation); err != nil {
				return err
			}
			return q.QueryRowContext(ctx, "SELECT management_id FROM identities WHERE table_name='QoS' AND ovs_uuid=? AND state='active'", qos).Scan(&management)
		}) == nil
	})
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
	}))
	must(t, f.auth.SetQoSExecutionRole(f.ctx, role, true))
	fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	f.login = fresh
	must(t, f.inventory.SetLocalQoS([]string{port.ManagementID + ":" + management}))
	f.inventory.SetQoSObserver(linuxprovider.New())
	result := qosRateFixture{
		fixture: f, port: port, target: rx, queue: queue, qos: qos, tx: sendNS,
		qosObject: candidate.Binding{Table: "QoS", OVSUUID: qos, ManagementID: management, Generation: port.Generation},
		original:  candidate.QoSRates{Type: discipline, Maximum: 8000000, Queues: []candidate.QoSQueueRate{{ID: 7, Minimum: 1000000, Maximum: 3000000}}},
		edited:    candidate.QoSRates{Type: discipline, Maximum: 12000000, Queues: []candidate.QoSQueueRate{{ID: 7, Minimum: 2000000, Maximum: 4000000}}},
	}
	result.waitRates(result.original)
	contract, err := apicontract.New()
	must(t, err)
	op, path, _ := contract.Match("GET", "/api/v1/qos/"+management)
	detail, err := f.inventory.Read(f.ctx, op.ID, path, nil, f.login.Claims)
	must(t, err)
	body, err := json.Marshal(detail)
	must(t, err)
	must(t, op.ValidateResponse(200, body))
	if detail.(map[string]any)["qos_rate_edit"].(map[string]any)["available"] != true {
		t.Fatal("authorized QoS detail did not offer rate editing")
	}
	return result
}

func (f qosRateFixture) intent() []any {
	return []any{map[string]any{"intent_id": repository.NewID(), "operation": candidate.QoSConfigure, "object": f.port,
		"type": f.edited.Type, "max_rate_bps": f.edited.Maximum, "queues": f.edited.Queues}}
}

func (f qosRateFixture) sample() (candidate.QoSPort, inventory.QoSRuntimeSample, error) {
	view, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{f.port})
	if err != nil {
		return candidate.QoSPort{}, inventory.QoSRuntimeSample{}, err
	}
	p := view.QoSPorts[f.port.ManagementID]
	sample, err := f.inventory.QoSRateEvidence(f.ctx, p)
	return p, sample, err
}

func (f qosRateFixture) waitRates(want candidate.QoSRates) inventory.QoSRuntimeSample {
	f.t.Helper()
	var sample inventory.QoSRuntimeSample
	var target candidate.QoSPort
	var lastErr error
	defer func() {
		if f.t.Failed() {
			f.t.Logf("QoS admission: reason=%s rates=%+v installed=%+v scheduler=%s kernel reason=%s error=%v", target.Reason, target.Rates, sample.Rates, sample.Scheduler, sample.Reason, lastErr)
			for _, object := range []string{"qdisc", "class"} {
				out, _ := exec.Command("tc", "-d", "-j", object, "show", "dev", f.target).CombinedOutput()
				f.t.Logf("fixture %s: %s", object, out)
			}
		}
	}()
	waitFor(f.t, func() bool {
		target, sample, lastErr = f.sample()
		return lastErr == nil && candidate.Digest(want) == candidate.Digest(target.Rates) && inventory.QoSRatesMatch(sample.Rates, want, target.MTU)
	})
	return sample
}

func (f qosRateFixture) assertQueueTraffic(want candidate.QoSRates) {
	f.t.Helper()
	packets := func(observation inventory.QoSRuntimeSample) uint64 {
		for _, q := range observation.Queues {
			if q.QueueID == 7 && q.Packets != nil {
				value, err := strconv.ParseUint(*q.Packets, 10, 64)
				must(f.t, err)
				return value
			}
		}
		f.t.Fatal("Queue 7 packet count unavailable")
		return 0
	}
	before := packets(f.waitRates(want))
	f.run("ip", "netns", "exec", f.tx, "ping", "-n", "-c", "3", "-i", "0.1", "-W", "1", "198.18.72.2")
	if packets(f.waitRates(want)) < before+3 {
		f.t.Fatal("same native Queue did not count the isolated packets")
	}
}

func TestNativeQoSRateExecution(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires explicit disposable kernel QoS fixture")
	}
	for _, discipline := range []string{"linux-htb", "linux-hfsc"} {
		t.Run(discipline+"_safe_apply_and_rollback", func(t *testing.T) {
			f := newQoSRateFixture(t, discipline)
			var offset atomic.Int64
			f.configureSafety(&offset)
			before := f.waitRates(f.original)
			in := f.prepareIntents(f.intent())
			if f.proxy.sent.Load() != 0 {
				t.Fatal("stage or validation wrote to OVSDB")
			}
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			f.assertQueueTraffic(f.edited)
			f.decide(id, "rollback")
			f.waitSafety(id, "rolled-back")
			f.assertQueueTraffic(f.original)
			restored := f.waitRates(f.original)
			if restored.ConfigurationDigest != before.ConfigurationDigest || f.proxy.sent.Load() != 2 ||
				f.vs("get", "Port", f.target, "qos") != f.qos || f.vs("get", "QoS", f.qos, "queues:7") != f.queue ||
				f.vs("get", "Queue", f.queue, "dscp") != "0" ||
				f.vs("get", "QoS", f.qos, "other_config:fixture-opaque") != "keep" ||
				f.vs("get", "Queue", f.queue, "other_config:fixture-opaque") != "keep" {
				t.Fatal("compensation did not restore rates or preserve native objects and metadata")
			}
		})
	}
	t.Run("late_queue_sharing_fences_atomic_write", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-htb")
		in := f.prepareIntents(f.intent())
		p, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("qos-atomic-test"), in.Envelope)
		must(t, err)
		f.proxy.hook(func() { f.vs("--no-wait", "create", "QoS", "type=linux-htb", "queues:9="+f.queue) })
		out := f.executor.Commit(f.ctx, p, func() error { return nil })
		if out.Commit != "rejected" || f.proxy.sent.Load() != 1 || f.vs("get", "QoS", f.qos, "other_config:max-rate") != `"8000000"` {
			t.Fatal("late Queue sharing did not block native transaction", out)
		}
	})
	t.Run("foreign_egress_filter_blocks_dispatch", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-htb")
		in := f.prepareIntents(f.intent())
		p, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("qos-kernel-test"), in.Envelope)
		must(t, err)
		f.run("tc", "filter", "add", "dev", f.target, "parent", "1:", "protocol", "all", "pref", "20", "matchall", "classid", "1:8")
		out := f.executor.Commit(f.ctx, p, func() error { return nil })
		if out.Commit != "rejected" || out.Reason != "qos-dispatch-unproven" || f.proxy.sent.Load() != 0 {
			t.Fatal("external kernel filter did not block dispatch", out)
		}
	})
	t.Run("deadline_restores_before_image_while_vswitchd_is_paused", func(t *testing.T) {
		f := newQoSRateFixture(t, "linux-htb")
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareIntents(f.intent())
		pid := f.pid("switch")
		must(t, syscall.Kill(pid, syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
		id := f.safeApply(in)
		t.Cleanup(func() {
			if t.Failed() {
				r, _ := f.engine.Read(f.ctx, id)
				s := f.safeState(id)
				t.Logf("paused QoS recovery: state=%s reason=%s trigger=%s apply=%+v rollback=%+v writes=%d", s.State, s.Reason, s.Trigger, r.Outcome, s.Outcome, f.proxy.sent.Load())
			}
		})
		r := f.observe(id, func(r execution.Record) bool {
			return r.Outcome.Commit == "committed" && r.Outcome.Reason == "awaiting-ovs-vswitchd"
		})
		if s := f.safeState(id); s.Confirmation != nil || s.State == "awaiting-confirmation" {
			t.Fatal("confirmed runtime based on database configuration alone")
		}
		_, err := f.executor.PrepareRollback(f.ctx, r.Plan, candidate.Digest("synthetic-qos-compensation-preflight"))
		must(t, err)
		offset.Store(int64(safety.PreparationBudget + time.Second))
		waitFor(t, func() bool {
			must(t, f.engine.SafetyTick(f.ctx))
			return f.proxy.sent.Load() == 2
		})
		if strings.Trim(f.vs("get", "QoS", f.qos, "other_config:max-rate"), `"`) != "8000000" {
			t.Fatal("original rate not restored while daemon paused")
		}
		must(t, syscall.Kill(pid, syscall.SIGCONT))
		f.waitSafety(id, "rolled-back")
		f.waitRates(f.original)
	})
}
