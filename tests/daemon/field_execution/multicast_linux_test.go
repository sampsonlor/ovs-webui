//go:build linux

package fieldexecution

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/web"
)

func TestNativeMulticast(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires explicit isolated native kernel OVS fixture")
	}
	f := newFixture(t)
	suffix := repository.NewID()[:6]
	bridge := "bm" + suffix
	f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system", "other_config:multicast-opaque=preserve")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", bridge) })
	namespaces, ports, peers := []string{}, []string{}, []string{}
	for n := 1; n <= 3; n++ {
		ns, p, peer := "nm"+suffix+strconv.Itoa(n), "pm"+suffix+strconv.Itoa(n), "cm"+suffix+strconv.Itoa(n)
		f.run("ip", "netns", "add", ns)
		namespaces = append(namespaces, ns)
		t.Cleanup(func() { f.run("ip", "netns", "del", ns) })
		f.run("ip", "link", "add", p, "type", "veth", "peer", "name", peer)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", p).Run() })
		f.run("ip", "link", "set", peer, "netns", ns)
		f.run("ip", "netns", "exec", ns, "sysctl", "-q", "-w", "net.ipv6.conf."+peer+".disable_ipv6=0")
		f.run("ip", "netns", "exec", ns, "ip", "addr", "add", "198.19.202."+strconv.Itoa(n)+"/24", "dev", peer)
		f.run("ip", "netns", "exec", ns, "ip", "link", "set", peer, "up")
		f.run("ip", "netns", "exec", ns, "ip", "-6", "addr", "add", "2001:db8:202::"+strconv.Itoa(n)+"/64", "nodad", "dev", peer)
		f.run("ip", "netns", "exec", ns, "ip", "-6", "addr", "add", "fe80::202:"+strconv.Itoa(n)+"/64", "nodad", "dev", peer)
		f.run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
		f.run("ip", "netns", "exec", ns, "ip", "route", "add", "224.0.0.0/4", "dev", peer)
		f.run("ip", "netns", "exec", ns, "sysctl", "-q", "-w", "net.ipv4.conf."+peer+".force_igmp_version=2")
		f.run("ip", "netns", "exec", ns, "sysctl", "-q", "-w", "net.ipv6.conf."+peer+".force_mld_version=2")
		f.vs("add-port", bridge, p)
		f.run("ip", "link", "set", p, "up")
		ports = append(ports, p)
		peers = append(peers, peer)
	}
	uuid := f.vs("get", "Bridge", bridge, "_uuid")
	var binding candidate.Binding
	waitFor(t, func() bool {
		return f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Bridge' AND state='active' AND ovs_uuid=?", uuid).Scan(&binding.ManagementID, &binding.OVSUUID, &binding.Table, &binding.Generation)
		}) == nil
	})
	must(t, f.inventory.SetLocalMulticastBridges([]string{binding.ManagementID}))
	var role string
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
	}))
	must(t, f.auth.SetMulticastExecutionRole(f.ctx, role, true))
	fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	f.login = fresh
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{binding})
		return err == nil && candidate.MulticastEditable(s.Multicasts[binding.ManagementID]) && f.vs("get", "Open_vSwitch", ".", "next_cfg") == f.vs("get", "Open_vSwitch", ".", "cur_cfg")
	})
	// Establish the real kernel datapath before starting the bounded multicast
	// captures. This setup barrier grants no production retry or extra deadline.
	waitFor(t, func() bool {
		for n := 2; n <= 3; n++ {
			if exec.Command("ip", "netns", "exec", namespaces[0], "ping", "-n", "-c", "1", "-W", "1", "198.19.202."+strconv.Itoa(n)).Run() != nil {
				return false
			}
		}
		return true
	})
	// These bounded captures run only in the three disposable test namespaces.
	// The listener joins through the kernel; no flow/script emulates snooping.
	capture := func(index int, join bool, token, family string) func() bool {
		ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
		script := `import socket,sys,time,struct
dev,ip,join,token,family=sys.argv[1:]
membership=None
if join=='true':
 membership=socket.socket(socket.AF_INET6 if family=='6' else socket.AF_INET,socket.SOCK_DGRAM)
 membership.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
 membership.bind(('::' if family=='6' else '',20000))
 if family=='6':membership.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_JOIN_GROUP,socket.inet_pton(socket.AF_INET6,'ff3e::7')+struct.pack('=I',socket.if_nametoindex(dev)))
 else:membership.setsockopt(socket.IPPROTO_IP,socket.IP_ADD_MEMBERSHIP,socket.inet_aton('239.192.0.7')+socket.inet_aton(ip))
s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(0x86dd if family=='6' else 0x0800));s.bind((dev,0));s.settimeout(.2)
print('ready',flush=True)
end=time.monotonic()+5;seen=False
while time.monotonic()<end:
 try:
  if token.encode() in s.recv(2048):seen=True;break
 except socket.timeout:pass
print('seen' if seen else 'miss',flush=True)
`
		cmd := exec.CommandContext(ctx, "ip", "netns", "exec", namespaces[index], "python3", "-u", "-c", script, peers[index], "198.19.202."+strconv.Itoa(index+1), strconv.FormatBool(join), token, family)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		must(t, err)
		must(t, cmd.Start())
		reader := bufio.NewReader(stdout)
		done := false
		t.Cleanup(func() {
			cancel()
			if !done {
				_ = cmd.Wait()
			}
		})
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "ready" {
			t.Fatal("native multicast capture did not become ready", err, stderr.String())
		}
		return func() bool {
			line, err := reader.ReadString('\n')
			waitErr := cmd.Wait()
			done = true
			cancel()
			if err != nil || waitErr != nil {
				t.Fatal("native multicast capture failed", err, waitErr, stderr.String())
			}
			return strings.TrimSpace(line) == "seen"
		}
	}
	probe := func(snooping bool) {
		for _, family := range []string{"4", "6"} {
			token := repository.NewID()
			listener := capture(1, true, token, family)
			outsider := capture(2, false, token, family)
			if snooping {
				waitFor(t, func() bool {
					wire := f.run("ovs-appctl", "-t", filepath.Join(f.root, "switch.ctl"), "mdb/show", bridge)
					groups, err := multicast.DecodeGroups([]byte(wire))
					if err != nil {
						return false
					}
					want, _ := strconv.ParseUint(strings.Trim(f.vs("get", "Interface", ports[1], "ofport"), "[]"), 10, 32)
					address, protocol := "239.192.0.7", "IGMPv2"
					if family == "6" {
						address, protocol = "ff3e::7", "MLDv2"
					}
					for _, g := range groups {
						if g.Address == address && g.Port == uint32(want) && g.Protocol == protocol {
							return true
						}
					}
					return false
				})
			}
			f.run("ip", "netns", "exec", namespaces[0], "python3", "-c", `import socket,sys,time
family,dev,token=sys.argv[1:]
if family=='6':
 s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM);index=socket.if_nametoindex(dev);s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_MULTICAST_IF,index);s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_MULTICAST_LOOP,0);target=('ff3e::7',20000,0,index)
else:
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.setsockopt(socket.IPPROTO_IP,socket.IP_MULTICAST_IF,socket.inet_aton('198.19.202.1'));s.setsockopt(socket.IPPROTO_IP,socket.IP_MULTICAST_LOOP,0);target=('239.192.0.7',20000)
for _ in range(3):s.sendto(token.encode(),target);time.sleep(.05)
`, family, peers[0], token)
			listenerSeen, outsiderSeen := listener(), outsider()
			if !listenerSeen || outsiderSeen == snooping {
				t.Log(f.vs("--columns=name,ofport,admin_state,link_state,error", "list", "Interface"))
				t.Fatalf("native forwarding disagrees: snooping=%v family=%s listener=%v nonmember=%v", snooping, family, listenerSeen, outsiderSeen)
			}
		}
	}
	probe(false)
	original := f.vs("get", "Bridge", bridge, "mcast_snooping_enable", "other_config")
	var offset atomic.Int64
	f.configureSafety(&offset)
	request := func() []map[string]any {
		return []map[string]any{{"intent_id": repository.NewID(), "operation": candidate.BridgeMulticastSet, "object": binding, "multicast": map[string]any{"enabled": true, "parameters": map[string]any{"mcast-snooping-aging-time": "300", "mcast-snooping-table-size": "64", "mcast-snooping-disable-flood-unregistered": "true"}}}}
	}
	in := f.prepareIntents(request())
	id := f.safeApply(in)
	f.waitSafety(id, "awaiting-confirmation")
	probe(true)
	f.decide(id, "rollback")
	f.waitSafety(id, "rolled-back")
	if f.vs("get", "Bridge", bridge, "mcast_snooping_enable", "other_config") != original {
		t.Fatal("native original or foreign key was not restored")
	}
	probe(false)
	settled := f.vs("get", "Open_vSwitch", ".", "next_cfg")
	must(t, f.engine.Recover(f.ctx))
	must(t, f.engine.SafetyTick(f.ctx))
	if settled != f.vs("get", "Open_vSwitch", ".", "next_cfg") {
		t.Fatal("terminal recovery replayed native mutation")
	}
	// Reopen the real manager state with entirely new auth/inventory/executor
	// instances while provisional. Recovery reads the durable checkpoint; the
	// original in-memory plan is not supplied to the new manager components.
	f.discardPortDraft()
	id = f.safeApply(f.prepareIntents(request()))
	f.waitSafety(id, "awaiting-confirmation")
	sentBeforeRestart := f.proxy.sent.Load()
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
	must(t, f.inventory.SetLocalMulticastBridges([]string{binding.ManagementID}))
	f.workspace, err = web.NewWorkspace(f.webStore, f.auth)
	must(t, err)
	offset.Store(int64(121 * time.Second))
	f.configureSafety(&offset)
	f.waitSafety(id, "rolled-back")
	if f.proxy.sent.Load() != sentBeforeRestart+1 || f.vs("get", "Bridge", bridge, "mcast_snooping_enable", "other_config") != original {
		t.Fatal("manager reopen did not compensate exactly once from its durable checkpoint")
	}
	settled = f.vs("get", "Open_vSwitch", ".", "next_cfg")
	must(t, f.engine.Recover(f.ctx))
	must(t, f.engine.SafetyTick(f.ctx))
	if settled != f.vs("get", "Open_vSwitch", ".", "next_cfg") {
		t.Fatal("reopened terminal recovery replayed a mutation")
	}
	offset.Store(0)
	f.discardPortDraft()
	in = f.prepareIntents(request())
	p, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("d", 64), in.Envelope)
	must(t, err)
	f.proxy.dropReply.Store(true)
	sent := f.proxy.sent.Load()
	out := f.executor.Commit(f.ctx, p, func() error { return nil })
	if out.Commit != "unknown" || out.Target != nil || out.Applied == "applied" {
		t.Fatal("lost native reply invented success", out)
	}
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{binding})
		return err == nil && s.Multicasts[binding.ManagementID].Configuration.Enabled
	})
	for n := 0; n < 3; n++ {
		out = f.executor.Observe(f.ctx, p, out)
		if out.Target != nil || out.Applied == "applied" {
			t.Fatal("observation invented Applied target")
		}
	}
	f.vs("set", "Bridge", bridge, "mcast_snooping_enable=false")
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{binding})
		return err == nil && !s.Multicasts[binding.ManagementID].Configuration.Enabled
	})
	if _, err := f.executor.PrepareRollback(f.ctx, p, strings.Repeat("e", 64)); err == nil || !strings.Contains(err.Error(), "ROLLBACK_CONFLICT") {
		t.Fatal("external native change did not block compensation", err)
	}
	if f.vs("get", "Bridge", bridge, "mcast_snooping_enable") != "false" || f.proxy.sent.Load() != sent+1 {
		t.Fatal("conflicting compensation changed native state or replayed a mutation")
	}
}
