//go:build linux

package fieldexecution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func newQinQFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.vs("--", "--id=@dp", "create", "Datapath", "external_ids:synthetic-qinq=fixture", "--", "set", "Open_vSwitch", ".", "datapaths:dummy=@dp", "other_config:vlan-limit=2")
	b := f.binding("field-p1")
	t.Cleanup(func() {
		if t.Failed() {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			p := s.Ports[b.ManagementID]
			t.Logf("QinQ projection: error=%v supported=%v context=%+v", err, p.QinQSupported, p.QinQ)
			t.Log("native datapaths", f.vs("get", "Open_vSwitch", ".", "datapaths"))
			t.Log("native capabilities", f.vs("--columns=capabilities", "list", "Datapath"))
			t.Log("native parser", f.vs("get", "Open_vSwitch", ".", "other_config:vlan-limit"))
		}
	})
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
		return err == nil && s.Ports[b.ManagementID].QinQSupported
	})
	return f
}

func (f *fixture) qinqIntent(cvlans ...int) candidate.Intent {
	mode, tag := "dot1q-tunnel", 200
	return candidate.Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: f.binding("field-p1"), Value: candidate.VLAN{Mode: &mode, Tag: &tag, Trunks: []int{}, CVLANs: append([]int{}, cvlans...)}}
}

func (f *fixture) waitQinQMode(mode string) {
	b := f.binding("field-p1")
	waitFor(f.t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
		p := s.Ports[b.ManagementID]
		return err == nil && p.VLAN.Mode != nil && *p.VLAN.Mode == mode
	})
}

func (f *fixture) changeQinQDependency(field string) {
	if field == "parser-limit" {
		f.vs("set", "Open_vSwitch", ".", "other_config:vlan-limit=1")
		return
	}
	f.vs("set", "Port", "field-p1", field)
}

func TestNativeQinQ(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated native QinQ matrix")
	}
	for _, tpid := range []string{"default", "802.1q", "802.1ad"} {
		t.Run("confirm_and_packet_semantics_"+tpid, func(t *testing.T) {
			f := newQinQFixture(t)
			if tpid != "default" {
				f.vs("set", "Port", "field-p1", "other_config:qinq-ethtype="+tpid)
			}
			f.vs("set", "Port", "field-p2", "vlan_mode=trunk", "tag=[]")
			b := f.binding("field-p1")
			waitFor(t, func() bool {
				s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
				p := s.Ports[b.ManagementID]
				return err == nil && p.QinQ != nil && (tpid == "default" && p.QinQ.EtherType == nil || p.QinQ.EtherType != nil && *p.QinQ.EtherType == tpid)
			})
			var offset atomic.Int64
			f.configureSafety(&offset)
			in := f.prepareIntents([]candidate.Intent{f.qinqIntent(30, 20)})
			if f.proxy.sent.Load() != 0 || f.vs("get", "Port", "field-p1", "tag") != "10" {
				t.Fatal("stage wrote live fields")
			}
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			if f.vs("get", "Port", "field-p1", "cvlans") != "[20, 30]" {
				t.Fatal("customer VLANs lost")
			}
			ofport := f.vs("get", "Interface", "field-p1", "ofport")
			trace := func(vlan string) string {
				return f.run("ovs-appctl", "-t", filepath.Join(f.root, "switch.ctl"), "ofproto/trace", "br-field", "in_port="+ofport+",dl_src=02:00:00:00:00:01,dl_dst=ff:ff:ff:ff:ff:ff,dl_type=0x0800,dl_vlan="+vlan)
			}
			allowed := trace("30")
			tag := "push_vlan(tpid=0x88a8,vid=200,"
			if tpid == "802.1q" {
				tag = "push_vlan(vid=200,"
			}
			if !strings.Contains(allowed, tag) || !strings.Contains(allowed, "vid=200") {
				t.Fatal("service encapsulation unproven", allowed)
			}
			if denied := trace("31"); !strings.Contains(denied, "Datapath actions: drop") {
				t.Fatal("customer filter unproven", denied)
			}
			// Trace a real encoded double-tagged Ethernet frame in the reverse
			// direction so the global parser limit is exercised, not inferred.
			outer := "88a8"
			if tpid == "802.1q" {
				outer = "8100"
			}
			packet := "ffffffffffff020000000002" + outer + "00c88100001e08004500001c00000000401100000a0000010a0000020035003500080000"
			reverse := f.run("ovs-appctl", "-t", filepath.Join(f.root, "switch.ctl"), "ofproto/trace", "br-field", "in_port="+f.vs("get", "Interface", "field-p2", "ofport"), packet)
			if !strings.Contains(reverse, "pop_vlan") || strings.Contains(reverse, "Datapath actions: drop") {
				t.Fatal("reverse QinQ decapsulation unproven", reverse)
			}
			f.decide(id, "confirm")
			f.waitSafety(id, "confirmed")
			if f.binding("field-p1") != b || f.proxy.sent.Load() != 1 {
				t.Fatal("identity replaced or write replayed")
			}
		})
	}
	t.Run("all_customer_vlans_and_rollback_preserve_unrelated_map", func(t *testing.T) {
		f := newQinQFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]candidate.Intent{f.qinqIntent()}))
		f.waitSafety(id, "awaiting-confirmation")
		if f.vs("get", "Port", "field-p1", "cvlans") != "[]" {
			t.Fatal("empty customer VLAN scope normalized")
		}
		f.vs("set", "Port", "field-p1", "other_config:unrelated=keep")
		b := f.binding("field-p1")
		waitFor(t, func() bool {
			v, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{b})
			if err != nil {
				return false
			}
			m, _ := v.Observation.Rows["Port"][b.OVSUUID].Values["other_config"].(map[string]any)
			return m["unrelated"] == "keep"
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Port", "field-p1", "vlan_mode") != "access" || f.vs("get", "Port", "field-p1", "tag") != "10" || f.vs("get", "Port", "field-p1", "other_config") != "{unrelated=keep}" {
			t.Fatal("rollback changed original or unrelated values")
		}
	})
	t.Run("exit_qinq_and_restore_exact_original", func(t *testing.T) {
		f := newQinQFixture(t)
		f.vs("set", "Port", "field-p1", "vlan_mode=dot1q-tunnel", "tag=200", "cvlans=30,20")
		f.waitQinQMode("dot1q-tunnel")
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1"}, 40))
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Port", "field-p1", "vlan_mode") != "dot1q-tunnel" || f.vs("get", "Port", "field-p1", "tag") != "200" || f.vs("get", "Port", "field-p1", "cvlans") != "[20, 30]" {
			t.Fatal("QinQ original not restored")
		}
	})
	for _, field := range []string{"cvlans=70", "other_config:qinq-ethtype=802.1q", "parser-limit"} {
		t.Run("late_external_change_aborts_atomic_batch_"+field, func(t *testing.T) {
			f := newQinQFixture(t)
			other := f.qinqIntent(30)
			other.ID = repository.NewID()
			other.Object = f.binding("field-p2")
			in := f.prepareIntents([]candidate.Intent{f.qinqIntent(30), other})
			f.proxy.mu.Lock()
			f.proxy.before = func() { f.changeQinQDependency(field) }
			f.proxy.mu.Unlock()
			r := f.submit(in)
			if r.Outcome.Commit != "rejected" || f.vs("get", "Port", "field-p2", "tag") != "10" {
				t.Fatal("late change partially committed", r)
			}
		})
		t.Run("external_change_blocks_compensation_"+field, func(t *testing.T) {
			f := newQinQFixture(t)
			var offset atomic.Int64
			f.configureSafety(&offset)
			id := f.safeApply(f.prepareIntents([]candidate.Intent{f.qinqIntent(30)}))
			f.waitSafety(id, "awaiting-confirmation")
			f.changeQinQDependency(field)
			b := f.binding("field-p1")
			waitFor(t, func() bool {
				s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
				p := s.Ports[b.ManagementID]
				return err == nil && (len(p.VLAN.CVLANs) == 1 && p.VLAN.CVLANs[0] == 70 || p.QinQ != nil && (p.QinQ.EtherType != nil || p.QinQ.VLANLimit == "1"))
			})
			f.decide(id, "rollback")
			f.waitSafety(id, "rollback-conflict")
			if f.proxy.sent.Load() != 1 || f.vs("get", "Port", "field-p1", "vlan_mode") != "dot1q-tunnel" {
				t.Fatal("external writer overwritten")
			}
		})
	}
	t.Run("lost_reply_recovers_without_replay_or_invented_target", func(t *testing.T) {
		f := newQinQFixture(t)
		in := f.prepareIntents([]candidate.Intent{f.qinqIntent(30)})
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Commit != "unknown" {
			t.Fatal(r)
		}
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Target != nil || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 1 {
			t.Fatal("invented target or replay", r)
		}
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("replayed mutation")
		}
	})
	t.Run("lost_compensation_reply_stays_unknown", func(t *testing.T) {
		f := newQinQFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]candidate.Intent{f.qinqIntent(30)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		if f.proxy.sent.Load() != 2 {
			t.Fatal("compensation replayed")
		}
	})
	t.Run("native_default_mode_restored_without_normalization", func(t *testing.T) {
		f := newQinQFixture(t)
		f.vs("clear", "Port", "field-p1", "vlan_mode")
		b := f.binding("field-p1")
		waitFor(t, func() bool {
			s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{b})
			return err == nil && s.Ports[b.ManagementID].VLAN.Mode == nil
		})
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareIntents([]candidate.Intent{f.qinqIntent(30)}))
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Port", "field-p1", "vlan_mode") != "[]" || f.vs("get", "Port", "field-p1", "tag") != "10" {
			t.Fatal("native default normalized")
		}
		// Public historical evidence retains the captured TPID dependency.
		r, err := f.engine.Read(f.ctx, id)
		must(t, err)
		body, _ := json.Marshal(r.Plan.Envelope)
		if !strings.Contains(string(body), "qinq_context") {
			t.Fatal("lost shared evidence")
		}
	})
}
