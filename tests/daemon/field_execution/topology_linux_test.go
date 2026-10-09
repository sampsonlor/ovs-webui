//go:build linux

package fieldexecution

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func topologyFixture(t *testing.T, kernel bool) (*fixture, string, string) {
	f, name := newBridgeFixture(t, kernel)
	other := "brt-" + repository.NewID()[:8]
	f.vs("add-br", name, "--", "set", "Bridge", name, "datapath_type=system", "--", "add-br", other, "--", "set", "Bridge", other, "datapath_type=system")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", name); f.vs("--if-exists", "del-br", other) })
	for _, suffix := range []string{"a", "b"} {
		p := "gt" + suffix + repository.NewID()[:7]
		f.vs("add-port", name, p, "--", "set", "Interface", p, "type=internal")
		f.topologyNode("Interface", p)
	}
	f.grantTopology(name, []string{"new-one", "new-bond"})
	return f, name, other
}
func (f *fixture) topologySnapshot() candidate.Snapshot {
	f.t.Helper()
	var s candidate.Snapshot
	waitFor(f.t, func() bool {
		var err error
		s, err = f.inventory.CandidateSnapshot(f.ctx, nil)
		return err == nil && s.Topology.Supported
	})
	return s
}
func (f *fixture) topologyNode(table, name string) candidate.TopologyNode {
	f.t.Helper()
	var n candidate.TopologyNode
	waitFor(f.t, func() bool {
		for _, p := range f.topologySnapshot().Topology.Nodes {
			if p.Binding.Table == table && p.Name == name {
				n = p
				return true
			}
		}
		return false
	})
	return n
}
func (f *fixture) topologyPorts(bridge string) []candidate.TopologyNode {
	f.t.Helper()
	b := f.topologyNode("Bridge", bridge)
	s := f.topologySnapshot()
	out := []candidate.TopologyNode{}
	for _, id := range b.Links {
		n := s.Topology.Nodes[id.ManagementID]
		if n.Name != bridge {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b candidate.TopologyNode) int { return strings.Compare(a.Name, b.Name) })
	return out
}
func (f *fixture) grantTopology(bridge string, names []string) {
	f.t.Helper()
	b := f.topologyNode("Bridge", bridge)
	s := f.topologySnapshot()
	ids := []string{}
	for id := range s.Topology.Nodes {
		ids = append(ids, id)
	}
	must(f.t, f.inventory.SetLocalTopologyObjects(ids))
	targets := []string{}
	for _, name := range names {
		targets = append(targets, b.Binding.ManagementID+":"+name)
	}
	must(f.t, f.inventory.SetLocalTopologyCreates(targets))
}
func (f *fixture) waitTopology(id, state string) safety.Record {
	f.t.Helper()
	defer func() {
		if !f.t.Failed() {
			return
		}
		s := f.safeState(id)
		r, err := f.engine.Read(f.ctx, id)
		if err != nil {
			return
		}
		f.t.Logf("topology state=%s reason=%s commit=%s applied=%s outcome=%s", s.State, s.Reason, r.Outcome.Commit, r.Outcome.Applied, r.Outcome.Reason)
		observed := r.Plan.Envelope.Candidate
		observed.Intents = append([]candidate.StoredIntent{}, observed.Intents...)
		for k := range observed.Intents {
			candidate.AfterImage(&observed.Intents[k])
		}
		checks, _ := candidate.Checks(observed, f.topologySnapshot())
		for _, c := range checks {
			if c.State != "allowed" {
				f.t.Logf("topology gate=%s", c.Code)
			}
		}
	}()
	return f.waitSafety(id, state)
}
func (f *fixture) prepareTopology(object candidate.Binding, op string, r candidate.TopologyRequest) execution.Request {
	f.t.Helper()
	return f.prepareIntents([]any{map[string]any{"intent_id": repository.NewID(), "operation": op, "object": object, "topology": r}})
}
func (f *fixture) topologyDevices(bridge string, count int) []candidate.TopologyNode {
	f.t.Helper()
	names := []string{}
	for k := 0; k < count; k++ {
		name := "gd" + repository.NewID()[:8]
		peer := "gp" + repository.NewID()[:8]
		names = append(names, name)
		f.run("ip", "link", "add", name, "type", "veth", "peer", "name", peer)
		f.run("ip", "link", "set", name, "up")
		f.run("ip", "link", "set", peer, "up")
		f.t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })
		f.vs("add-port", bridge, name, "--", "set", "Interface", name, "type=system")
	}
	for _, name := range names {
		f.topologyNode("Interface", name)
	}
	f.grantTopology(bridge, names)
	out := []candidate.TopologyNode{}
	for _, name := range names {
		out = append(out, f.topologyNode("Port", name))
	}
	return out
}
func TestNativeTopologyCore(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit native topology matrix")
	}
	t.Run("internal_create_confirm_and_recover_without_replay", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		b := f.topologyNode("Bridge", bridge)
		in := f.prepareTopology(b.Binding, "port.create", candidate.TopologyRequest{Name: "new-one", NativeType: "internal"})
		if f.proxy.sent.Load() != 0 {
			t.Fatal("stage wrote OVS")
		}
		id := f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		f.graphStates(in.Envelope.Candidate.Intents[0], "active")
		f.decide(id, "confirm")
		f.waitTopology(id, "confirmed")
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("replayed creation")
		}
	})
	t.Run("system_bond_create_rollback_preserves_host_devices", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		devices := []string{}
		for k := 0; k < 2; k++ {
			name := "bn" + repository.NewID()[:8]
			peer := "bp" + repository.NewID()[:8]
			f.run("ip", "link", "add", name, "type", "veth", "peer", "name", peer)
			f.run("ip", "link", "set", name, "up")
			t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })
			devices = append(devices, name)
		}
		f.grantTopology(bridge, append([]string{"new-bond"}, devices...))
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareTopology(f.topologyNode("Bridge", bridge).Binding, "bond.create", candidate.TopologyRequest{Name: "new-bond", NativeType: "system", InterfaceNames: devices})
		id := f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		if f.vs("get", "Port", "new-bond", "bond_mode") != "active-backup" {
			t.Fatal("wrong Bond mode")
		}
		f.decide(id, "rollback")
		f.waitTopology(id, "rolled-back")
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
		for _, name := range devices {
			if _, err := net.InterfaceByName(name); err != nil {
				t.Fatal("host device removed", err)
			}
		}
	})
	t.Run("move_preserves_unknown_configuration_and_identity", func(t *testing.T) {
		f, bridge, other := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		f.vs("set", "Port", p.Name, "other_config:synthetic-unknown=keep-me")
		waitFor(t, func() bool {
			return f.topologySnapshot().Topology.Configurations[p.Binding.ManagementID]["other_config"].(map[string]any)["synthetic-unknown"] == "keep-me"
		})
		var offset atomic.Int64
		f.configureSafety(&offset)
		to := f.topologyNode("Bridge", other).Binding
		in := f.prepareTopology(p.Binding, "port.move", candidate.TopologyRequest{Destination: &to})
		id := f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		if f.vs("port-to-br", p.Name) != other {
			t.Fatal("Port not moved")
		}
		f.decide(id, "rollback")
		f.waitTopology(id, "rolled-back")
		if f.vs("port-to-br", p.Name) != bridge || f.vs("get", "Port", p.Name, "_uuid") != p.Binding.OVSUUID || f.vs("get", "Port", p.Name, "other_config:synthetic-unknown") != "keep-me" {
			t.Fatal("identity or unknown configuration lost")
		}
	})
	t.Run("explicit_bond_merge_and_split_without_interface_recreation", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		ports := f.topologyDevices(bridge, 2)
		var offset atomic.Int64
		f.configureSafety(&offset)
		members := append(append([]candidate.Binding{}, ports[0].Links...), ports[1].Links...)
		in := f.prepareTopology(ports[0].Binding, "bond.members.set", candidate.TopologyRequest{Sources: []candidate.Binding{ports[1].Binding}, Members: members})
		id := f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		f.decide(id, "confirm")
		f.waitTopology(id, "confirmed")
		if f.vs("--if-exists", "get", "Port", ports[1].Binding.OVSUUID, "_uuid") != "" {
			t.Fatal("source Port not collected")
		}
		f.discardPortDraft()
		f.grantTopology(bridge, []string{ports[1].Name})
		in = f.prepareTopology(ports[0].Binding, "bond.members.set", candidate.TopologyRequest{Members: ports[0].Links})
		id = f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		split := f.topologyNode("Port", ports[1].Name)
		if split.Binding == ports[1].Binding || !slices.Equal(split.Links, ports[1].Links) {
			t.Fatal("split stole or recreated Interface")
		}
		f.decide(id, "confirm")
		f.waitTopology(id, "confirmed")
	})
	for _, op := range []string{"port.delete", "bridge.delete-tree"} {
		t.Run(op+"_rollback_fresh_ids", func(t *testing.T) {
			f, bridge, _ := topologyFixture(t, true)
			object := f.topologyPorts(bridge)[0].Binding
			if op == "bridge.delete-tree" {
				object = f.topologyNode("Bridge", bridge).Binding
			}
			var offset atomic.Int64
			f.configureSafety(&offset)
			in := f.prepareTopology(object, op, candidate.TopologyRequest{})
			i := in.Envelope.Candidate.Intents[0]
			id := f.safeApply(in)
			f.waitTopology(id, "awaiting-confirmation")
			f.decide(id, "rollback")
			f.waitTopology(id, "rolled-back")
			for old, replacement := range i.Topology.Replacements {
				if f.vs("--if-exists", "get", replacement.Table, old, "_uuid") != "" || f.vs("get", replacement.Table, replacement.OVSUUID, "_uuid") != replacement.OVSUUID {
					t.Fatal("deleted identity reused or restoration missing")
				}
			}
		})
	}
	t.Run("ofport_actual_allocation_and_exact_compensation", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		iface := f.topologySnapshot().Topology.Nodes[p.Links[0].ManagementID]
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareTopology(iface.Binding, "interface.ofport.set", candidate.TopologyRequest{Ofport: 23})
		id := f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		if f.vs("get", "Interface", iface.Name, "ofport") != "23" {
			t.Fatal("only request was checked")
		}
		f.decide(id, "rollback")
		f.waitTopology(id, "rolled-back")
		if f.vs("get", "Interface", iface.Name, "ofport_request") != "[]" {
			t.Fatal("native empty request not restored")
		}
	})
	t.Run("paired_patch_conversion_and_exact_type_recovery", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		ports := f.topologyPorts(bridge)
		a, b := ports[0].Links[0], ports[1].Links[0]
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareTopology(a, "interface.patch.connect", candidate.TopologyRequest{Peer: &b})
		id := f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		if f.vs("get", "Interface", a.OVSUUID, "type") != "patch" || f.vs("get", "Interface", b.OVSUUID, "options:peer") != ports[0].Name {
			t.Fatal("nonreciprocal Patch type")
		}
		f.decide(id, "rollback")
		f.waitTopology(id, "rolled-back")
		if f.vs("get", "Interface", a.OVSUUID, "type") != "internal" || f.vs("get", "Interface", a.OVSUUID, "options") != "{}" {
			t.Fatal("type not restored")
		}
	})
}

type topologyPingProbe struct{ namespace, target, root string }

func (p topologyPingProbe) Domain() string { return p.root }
func (p topologyPingProbe) Check(ctx context.Context) error {
	return exec.CommandContext(ctx, "ip", "netns", "exec", p.namespace, "ping", "-c", "1", "-W", "1", p.target).Run()
}
func TestNativeTopologySafety(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit native topology safety matrix")
	}
	t.Run("same_name_host_replacement_blocks_unsent_creation", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		name, peer := "hi"+repository.NewID()[:8], "hp"+repository.NewID()[:8]
		f.run("ip", "link", "add", name, "type", "veth", "peer", "name", peer)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })
		original, err := net.InterfaceByName(name)
		must(t, err)
		f.grantTopology(bridge, []string{name})
		in := f.prepareTopology(f.topologyNode("Bridge", bridge).Binding, "port.create", candidate.TopologyRequest{Name: name, NativeType: "system"})
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("synthetic-host-identity"), in.Envelope)
		must(t, err)
		f.run("ip", "link", "del", name)
		f.run("ip", "link", "add", name, "type", "veth", "peer", "name", peer)
		replacement, err := net.InterfaceByName(name)
		must(t, err)
		if original.Index == replacement.Index {
			t.Fatal("fixture did not replace the host identity")
		}
		out := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if out.Commit != "rejected" || out.Reason != "topology-host-identity-changed" || f.proxy.sent.Load() != 0 || f.vs("--if-exists", "get", "Interface", name, "_uuid") != "" {
			t.Fatal("same-name host replacement inherited creation authority", out)
		}
	})
	t.Run("same_name_host_replacement_blocks_applied_and_compensation", func(t *testing.T) {
		f, bridge, other := topologyFixture(t, true)
		p := f.topologyDevices(bridge, 1)[0]
		to := f.topologyNode("Bridge", other).Binding
		in := f.prepareTopology(p.Binding, "port.move", candidate.TopologyRequest{Destination: &to})
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("synthetic-host-recovery"), in.Envelope)
		must(t, err)
		out := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if out.Commit != "committed" {
			t.Fatal("fixture move failed", out)
		}
		waitFor(t, func() bool { out = f.executor.Observe(f.ctx, plan, out); return out.Applied == "applied" })
		f.run("ip", "link", "del", p.Name)
		peer := "hr" + repository.NewID()[:8]
		f.run("ip", "link", "add", p.Name, "type", "veth", "peer", "name", peer)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", p.Name).Run() })
		observed := f.executor.Observe(f.ctx, plan, out)
		if observed.Applied == "applied" {
			t.Fatal("replacement inherited an Applied proof")
		}
		if _, err = f.executor.PrepareRollback(f.ctx, plan, candidate.Digest("synthetic-host-compensation")); err == nil || f.proxy.sent.Load() != 1 || f.vs("port-to-br", p.Name) != other {
			t.Fatal("replacement inherited compensation authority", err)
		}
	})
	t.Run("revoked_root_topology_grant_blocks_dispatch", func(t *testing.T) {
		f, bridge, other := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		to := f.topologyNode("Bridge", other).Binding
		in := f.prepareTopology(p.Binding, "port.move", candidate.TopologyRequest{Destination: &to})
		lease, err := f.workspace.ReserveExecution(f.ctx, publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}, in, isolatedSafety{f})
		must(t, err)
		must(t, f.inventory.SetLocalTopologyObjects(nil))
		_, err = f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), lease)
		if err == nil || f.proxy.sent.Load() != 0 || f.vs("port-to-br", p.Name) != bridge {
			t.Fatal("revoked root topology grant admitted native mutation", err)
		}
	})
	for _, kind := range []string{"late-native-field", "late-foreign-weak-reference", "late-membership"} {
		t.Run(kind+"_rejects_atomic_mutation", func(t *testing.T) {
			f, bridge, other := topologyFixture(t, true)
			p := f.topologyPorts(bridge)[0]
			to := f.topologyNode("Bridge", other).Binding
			in := f.prepareTopology(p.Binding, "port.move", candidate.TopologyRequest{Destination: &to})
			f.proxy.hook(func() {
				switch kind {
				case "late-native-field":
					f.vs("set", "Port", p.Name, "other_config:late=preserved")
				case "late-membership":
					f.vs("add-port", bridge, "late-one", "--", "set", "Interface", "late-one", "type=internal")
				case "late-foreign-weak-reference":
					f.vs("--", "--id=@p", "get", "Port", p.Name, "--", "--id=@m", "create", "Mirror", "name=synthetic", "select_src_port=@p", "select_all=false", "--", "add", "Bridge", bridge, "mirrors", "@m")
				}
			})
			r := f.submit(in)
			if r.Outcome.Commit != "rejected" || f.vs("port-to-br", p.Name) != bridge {
				t.Fatal("late dependency committed", r.Outcome)
			}
		})
	}
	t.Run("rollback_refuses_external_change", func(t *testing.T) {
		f, bridge, other := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		to := f.topologyNode("Bridge", other).Binding
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareTopology(p.Binding, "port.move", candidate.TopologyRequest{Destination: &to}))
		f.waitTopology(id, "awaiting-confirmation")
		f.vs("set", "Port", p.Name, "other_config:external=preserve")
		f.decide(id, "rollback")
		f.waitTopology(id, "rollback-conflict")
		if f.vs("port-to-br", p.Name) != other {
			t.Fatal("external change overwritten")
		}
	})
	t.Run("lost_reply_is_not_replayed_or_reported_applied", func(t *testing.T) {
		f, bridge, other := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		to := f.topologyNode("Bridge", other).Binding
		in := f.prepareTopology(p.Binding, "port.move", candidate.TopologyRequest{Destination: &to})
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Applied == "applied" {
			t.Fatal("lost target invented")
		}
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("unknown mutation replayed")
		}
	})
	t.Run("real_management_path_loss_restores_attachment", func(t *testing.T) {
		f, bridge, other := topologyFixture(t, true)
		ns := "gn" + repository.NewID()[:8]
		host := "gh" + repository.NewID()[:8]
		peer := "gp" + repository.NewID()[:8]
		f.run("ip", "netns", "add", ns)
		t.Cleanup(func() {
			_ = exec.Command("ip", "netns", "del", ns).Run()
			_ = exec.Command("ip", "link", "del", host).Run()
		})
		f.run("ip", "link", "add", host, "type", "veth", "peer", "name", peer)
		f.run("ip", "link", "set", peer, "netns", ns)
		f.run("ip", "link", "set", host, "up")
		f.run("ip", "link", "set", bridge, "up")
		f.run("ip", "addr", "add", "10.233.71.1/30", "dev", bridge)
		f.run("ip", "netns", "exec", ns, "ip", "link", "set", peer, "up")
		f.run("ip", "netns", "exec", ns, "ip", "addr", "add", "10.233.71.2/30", "dev", peer)
		f.vs("add-port", bridge, host, "--", "set", "Interface", host, "type=system")
		f.topologyNode("Port", host)
		f.grantTopology(bridge, nil)
		probe := topologyPingProbe{namespace: ns, target: "10.233.71.1", root: f.root}
		waitFor(t, func() bool { return probe.Check(f.ctx) == nil })
		var offset atomic.Int64
		must(t, f.auth.ConfigureSafety(executions.SafetyOptions{Probe: probe, Clock: func() tlscontrol.Clock {
			c := tlscontrol.Now()
			d := time.Duration(offset.Load())
			c.NS += int64(d)
			c.Wall = c.Wall.Add(d)
			return c
		}}))
		to := f.topologyNode("Bridge", other).Binding
		in := f.prepareTopology(f.topologyNode("Port", host).Binding, "port.move", candidate.TopologyRequest{Destination: &to})
		id := f.safeApply(in)
		f.waitTopology(id, "rolled-back")
		if f.vs("port-to-br", host) != bridge || probe.Check(f.ctx) != nil {
			t.Fatal("management forwarding not restored")
		}
	})
}

func TestNativeTopologyFields(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit native topology fields matrix")
	}
	t.Run("clear_native_ofport_then_restore_explicit_request", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		a := p.Links[0]
		f.vs("set", "Interface", a.OVSUUID, "ofport_request=23")
		waitFor(t, func() bool { return f.topologySnapshot().Topology.Allocations[a.ManagementID] == "23" })
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareTopology(a, "interface.ofport.clear", candidate.TopologyRequest{}))
		f.waitTopology(id, "awaiting-confirmation")
		if f.vs("get", "Interface", a.OVSUUID, "ofport_request") != "[]" {
			t.Fatal("clear became zero or missing")
		}
		f.decide(id, "rollback")
		f.waitTopology(id, "rolled-back")
		if f.vs("get", "Interface", a.OVSUUID, "ofport_request") != "23" {
			t.Fatal("explicit request not restored")
		}
	})
	t.Run("disconnect_patch_pair_and_restore_both_peers", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		p := f.topologyPorts(bridge)
		a, b := p[0].Links[0], p[1].Links[0]
		f.vs("set", "Interface", a.OVSUUID, "type=patch", "options:peer="+p[1].Name, "--", "set", "Interface", b.OVSUUID, "type=patch", "options:peer="+p[0].Name)
		waitFor(t, func() bool { return f.topologySnapshot().Topology.Nodes[a.ManagementID].Type == "patch" })
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareTopology(a, "interface.patch.disconnect", candidate.TopologyRequest{Peer: &b}))
		f.waitTopology(id, "awaiting-confirmation")
		if f.vs("get", "Interface", a.OVSUUID, "type") != "internal" {
			t.Fatal("type not disconnected")
		}
		f.decide(id, "rollback")
		f.waitTopology(id, "rolled-back")
		if f.vs("get", "Interface", a.OVSUUID, "type") != "patch" || f.vs("get", "Interface", b.OVSUUID, "options:peer") != p[0].Name {
			t.Fatal("Patch pair not restored")
		}
	})
	t.Run("confirmed_delete_same_name_recreate_never_reuses_ids", func(t *testing.T) {
		f, bridge, _ := topologyFixture(t, true)
		p := f.topologyPorts(bridge)[0]
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepareTopology(p.Binding, "port.delete", candidate.TopologyRequest{}))
		f.waitTopology(id, "awaiting-confirmation")
		f.decide(id, "confirm")
		f.waitTopology(id, "confirmed")
		f.discardPortDraft()
		f.grantTopology(bridge, []string{p.Name})
		in := f.prepareTopology(f.topologyNode("Bridge", bridge).Binding, "port.create", candidate.TopologyRequest{Name: p.Name, NativeType: "internal"})
		id = f.safeApply(in)
		f.waitTopology(id, "awaiting-confirmation")
		again := f.topologyNode("Port", p.Name)
		if again.Binding == p.Binding || slices.Equal(again.Links, p.Links) {
			t.Fatal("same-name object adopted old identity")
		}
		f.decide(id, "confirm")
		f.waitTopology(id, "confirmed")
	})
}

// JSON here is test evidence, not a substitute for three independently executed
// schema transactions or real system-device/management-path observations.
