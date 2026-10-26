//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/tunnel"
	"github.com/sampsonlor/ovs-webui/internal/web"
)

func (f *fixture) grantStaticTunnel(bridge, name string) candidate.Binding {
	f.t.Helper()
	b := f.topologyNode("Bridge", bridge).Binding
	ids := []string{}
	for id := range f.topologySnapshot().Topology.Nodes {
		ids = append(ids, id)
	}
	must(f.t, f.inventory.SetLocalTunnelObjects(ids))
	must(f.t, f.inventory.SetLocalTunnelCreates([]string{b.ManagementID + ":" + name}))
	waitFor(f.t, func() bool {
		s := f.topologySnapshot()
		return s.Topology.TunnelSupported && s.Topology.TunnelAuthority[b.ManagementID] && f.vs("get", "Open_vSwitch", ".", "next_cfg") == f.vs("get", "Open_vSwitch", ".", "cur_cfg")
	})
	return b
}

func TestNativeStaticTunnels(t *testing.T) {
	if os.Getenv("OVS_STATIC_TUNNEL_NATIVE_TEST") != "1" {
		t.Skip("requires explicit isolated static tunnel kernel fixture")
	}
	for _, row := range []struct{ typ, family string }{{"vxlan", "4"}, {"geneve", "4"}, {"gre", "4"}, {"vxlan", "6"}, {"geneve", "6"}} {
		t.Run(row.typ+"_ipv"+row.family, func(t *testing.T) {
			f := newFixture(t)
			f.run("modprobe", "openvswitch")
			suffix := repository.NewID()[:6]
			bridge, name, peerNS, underlay, peerDevice := "bt"+suffix, "tn"+suffix, "nt"+suffix, "ut"+suffix, "up"+suffix
			f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system", "other_config:tunnel-foreign=preserve", "other_config:hwaddr=02:00:00:77:01:01")
			t.Cleanup(func() { f.vs("--if-exists", "del-br", bridge) })
			f.run("ip", "netns", "add", peerNS)
			t.Cleanup(func() { f.run("ip", "netns", "del", peerNS) })
			f.run("ip", "link", "add", underlay, "type", "veth", "peer", "name", peerDevice)
			t.Cleanup(func() { _ = exec.Command("ip", "link", "del", underlay).Run() })
			f.run("ip", "link", "set", peerDevice, "netns", peerNS)
			f.run("ip", "link", "set", underlay, "up")
			f.run("ip", "netns", "exec", peerNS, "ip", "link", "set", peerDevice, "up")
			f.run("ip", "netns", "exec", peerNS, "ip", "link", "set", "lo", "up")
			local, remote := "198.18.77.1", "198.18.77.2"
			if row.family == "4" {
				f.run("ip", "addr", "add", local+"/30", "dev", underlay)
				f.run("ip", "netns", "exec", peerNS, "ip", "addr", "add", remote+"/30", "dev", peerDevice)
			} else {
				local, remote = "2001:db8:77::1", "2001:db8:77::2"
				f.run("sysctl", "-q", "-w", "net.ipv6.conf."+underlay+".disable_ipv6=0")
				f.run("ip", "netns", "exec", peerNS, "sysctl", "-q", "-w", "net.ipv6.conf."+peerDevice+".disable_ipv6=0")
				f.run("ip", "-6", "addr", "add", local+"/64", "nodad", "dev", underlay)
				f.run("ip", "netns", "exec", peerNS, "ip", "-6", "addr", "add", remote+"/64", "nodad", "dev", peerDevice)
			}
			waitFor(t, func() bool { _, err := net.InterfaceByName(bridge); return err == nil })
			f.run("ip", "link", "set", bridge, "up")
			f.run("ip", "addr", "add", "10.219.55.1/30", "dev", bridge)
			peerTunnel := func(key string, replace bool) {
				if replace {
					f.run("ip", "netns", "exec", peerNS, "ip", "link", "del", "peer-tunnel")
				}
				args := []string{"ip", "netns", "exec", peerNS, "ip", "link", "add", "peer-tunnel", "type", row.typ}
				if row.typ == "gre" {
					args[len(args)-1] = "gretap"
					args = append(args, "local", remote, "remote", local, "key", key, "dev", peerDevice)
				} else {
					args = append(args, "id", key, "remote", local)
					if row.typ == "vxlan" {
						args = append(args, "local", remote, "dev", peerDevice, "dstport", "4789")
					} else {
						args = append(args, "dstport", "6081")
					}
					if row.family == "6" {
						// Keep the native OVS checksum default. Only this isolated
						// Linux peer is configured to accept IPv6 zero checksums.
						args = append(args, "udp6zerocsumrx")
					}
				}
				f.run(args...)
				f.run("ip", "netns", "exec", peerNS, "ip", "link", "set", "peer-tunnel", "address", "02:00:00:77:02:02")
				f.run("ip", "netns", "exec", peerNS, "ip", "link", "set", "peer-tunnel", "up")
				f.run("ip", "netns", "exec", peerNS, "ip", "addr", "add", "10.219.55.2/30", "dev", "peer-tunnel")
			}
			peerTunnel("1001", false)
			ping := func() bool {
				return exec.Command("ping", "-n", "-c", "1", "-W", "1", "-I", bridge, "10.219.55.2").Run() == nil
			}
			var role string
			must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
				return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
			}))
			must(t, f.auth.SetStaticTunnelExecutionRole(f.ctx, role, true))
			fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
			must(t, err)
			f.login = fresh
			binding := f.grantStaticTunnel(bridge, name)
			var offset atomic.Int64
			f.configureSafety(&offset)
			nativeKey := "1001"
			r := candidate.TopologyRequest{Name: name, Tunnel: &tunnel.Request{Type: row.typ, Options: map[string]*string{"remote_ip": &remote, "local_ip": &local, "key": &nativeKey}}}
			id := f.safeApply(f.prepareTopology(binding, candidate.TunnelCreate, r))
			f.waitTopology(id, "awaiting-confirmation")
			waitFor(t, ping)
			f.decide(id, "confirm")
			f.waitTopology(id, "confirmed")
			originalPort := f.topologyNode("Port", name).Binding
			originalInterface := f.topologyNode("Interface", name).Binding
			f.vs("set", "Interface", name, "external_ids:tunnel-foreign=preserve")
			waitFor(t, func() bool {
				return f.topologySnapshot().Topology.Configurations[originalInterface.ManagementID]["external_ids"].(map[string]any)["tunnel-foreign"] == "preserve"
			})
			original := f.vs("get", "Interface", name, "type", "options", "external_ids")
			originalConfiguration := f.vs("get", "Interface", name, "type", "options")
			f.grantStaticTunnel(bridge, name)
			f.discardPortDraft()
			nativeKey = "2002"
			id = f.safeApply(f.prepareTopology(originalPort, candidate.TunnelConfigure, candidate.TopologyRequest{Tunnel: &tunnel.Request{Options: map[string]*string{"key": &nativeKey}}}))
			f.waitTopology(id, "awaiting-confirmation")
			// Configuration Applied is not a claim of immediate packet-path
			// convergence. Independently observe the bounded kernel transition.
			waitFor(t, func() bool { return !ping() })
			peerTunnel(nativeKey, true)
			waitFor(t, ping)
			if f.topologyNode("Interface", name).Binding != originalInterface {
				t.Fatal("configuration edit replaced native identity")
			}
			f.decide(id, "rollback")
			f.waitTopology(id, "rolled-back")
			if f.vs("get", "Interface", name, "type", "options", "external_ids") != original {
				t.Fatal("native option group did not recover exactly")
			}
			peerTunnel("1001", true)
			waitFor(t, ping)
			f.discardPortDraft()
			id = f.safeApply(f.prepareTopology(originalPort, candidate.TunnelDelete, candidate.TopologyRequest{}))
			f.waitTopology(id, "awaiting-confirmation")
			waitFor(t, func() bool { return !ping() })
			// Reopen the actual private manager DB with new repository, monitor,
			// executor and engine instances; do not pass the in-memory plan across.
			before := f.proxy.sent.Load()
			f.cancel()
			<-f.monitorDone
			f.cancel = nil
			must(t, f.store.Close())
			f.store = nil
			f.store, err = sqlite.Open(context.Background(), f.options)
			must(t, err)
			f.auth, err = auth.New(f.store, key)
			must(t, err)
			f.startMonitor()
			f.grantStaticTunnel(bridge, name)
			f.workspace, err = web.NewWorkspace(f.webStore, f.auth)
			must(t, err)
			offset.Store(int64(121 * time.Second))
			f.configureSafety(&offset)
			f.waitTopology(id, "rolled-back")
			if f.proxy.sent.Load() != before+1 {
				t.Fatal("reopened manager did not compensate exactly once")
			}
			port := f.topologyNode("Port", name).Binding
			iface := f.topologyNode("Interface", name).Binding
			if port == originalPort || iface == originalInterface || port.OVSUUID == originalPort.OVSUUID || iface.OVSUUID == originalInterface.OVSUUID {
				t.Fatal("deleted identities were resurrected")
			}
			if f.vs("get", "Interface", name, "type", "options") != originalConfiguration || f.vs("get", "Interface", name, "external_ids:tunnel-foreign") != "preserve" {
				t.Fatal("fresh-identity recovery lost original tunnel configuration")
			}
			waitFor(t, ping)
			settled := f.vs("get", "Open_vSwitch", ".", "next_cfg")
			must(t, f.engine.Recover(f.ctx))
			must(t, f.engine.SafetyTick(f.ctx))
			if settled != f.vs("get", "Open_vSwitch", ".", "next_cfg") {
				t.Fatal("terminal recovery replayed a mutation")
			}
			offset.Store(0)
			f.grantStaticTunnel(bridge, name)
			f.discardPortDraft()
			nativeKey = "3003"
			in := f.prepareTopology(port, candidate.TunnelConfigure, candidate.TopologyRequest{Tunnel: &tunnel.Request{Options: map[string]*string{"key": &nativeKey}}})
			plan, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("d", 64), in.Envelope)
			must(t, err)
			f.proxy.dropReply.Store(true)
			sent := f.proxy.sent.Load()
			outcome := f.executor.Commit(f.ctx, plan, func() error { return nil })
			if outcome.Commit != "unknown" || outcome.Target != nil || outcome.Applied == "applied" {
				t.Fatal("lost native reply invented Applied evidence", outcome)
			}
			waitFor(t, func() bool { return strings.Contains(f.vs("get", "Interface", name, "options"), "3003") })
			for n := 0; n < 3; n++ {
				outcome = f.executor.Observe(f.ctx, plan, outcome)
				if outcome.Target != nil || outcome.Applied == "applied" {
					t.Fatal("observation invented a lost target")
				}
			}
			f.vs("set", "Interface", name, "options:key=4004")
			waitFor(t, func() bool {
				return f.topologySnapshot().Topology.Configurations[iface.ManagementID]["options"].(map[string]any)["key"] == "4004"
			})
			if _, err := f.executor.PrepareRollback(f.ctx, plan, strings.Repeat("e", 64)); err == nil {
				t.Fatal("external configuration was adopted for compensation")
			}
			if f.proxy.sent.Load() != sent+1 || !strings.Contains(f.vs("get", "Interface", name, "options"), "4004") {
				t.Fatal("conflicting compensation overwrote native state or replayed")
			}
			t.Log("native static tunnel create/configure/delete, packet delivery/key change, exact recovery, durable reopen, fresh identity, lost reply/no replay and external conflict passed")
		})
	}
}
