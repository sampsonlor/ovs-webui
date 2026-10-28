//go:build linux

package fieldexecution

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/web"
)

func (f *fixture) grantNativeMirror(bridge, name string) candidate.Binding {
	f.t.Helper()
	b := f.topologyNode("Bridge", bridge).Binding
	ids := []string{}
	for id := range f.topologySnapshot().Topology.Nodes {
		ids = append(ids, id)
	}
	must(f.t, f.inventory.SetLocalMirrorObjects(ids))
	must(f.t, f.inventory.SetLocalMirrorCreates([]string{b.ManagementID + ":" + name}))
	if s := f.topologySnapshot(); !s.Topology.MirrorSupported || !s.Topology.MirrorAuthority[b.ManagementID] {
		// Report only public schema descriptors and qualification booleans,
		// never private observed row values, credentials or recovery images.
		var native map[string]any
		must(f.t, json.Unmarshal([]byte(f.run("ovsdb-client", "get-schema", "unix:"+f.dbSocket, "Open_vSwitch")), &native))
		columns := native["tables"].(map[string]any)["Mirror"].(map[string]any)["columns"]
		f.t.Logf("Mirror qualification: core=%t mirror=%t defaults=%d bridge=%t native_columns=%v", s.Topology.Supported, s.Topology.MirrorSupported, len(s.Topology.MirrorDefaults), s.Topology.MirrorAuthority[b.ManagementID], columns)
	}
	waitFor(f.t, func() bool {
		s := f.topologySnapshot()
		return s.Topology.MirrorSupported && s.Topology.MirrorAuthority[b.ManagementID] && f.vs("get", "Open_vSwitch", ".", "next_cfg") == f.vs("get", "Open_vSwitch", ".", "cur_cfg")
	})
	return b
}

func TestNativeMirrorSessions(t *testing.T) {
	if os.Getenv("OVS_MIRROR_NATIVE_TEST") != "1" {
		t.Skip("requires explicit isolated native Mirror kernel fixture")
	}
	for _, scenario := range []string{"source_direction", "destination_direction", "all_ports_vlan_filter", "snaplen", "rspan_vlan"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			f.run("modprobe", "openvswitch")
			// This fixture's initial field Bridge uses a dummy datapath. Keep
			// both disposable Bridges on the real kernel datapath so the native
			// revalidator/wait command has exactly one udpif (its upstream
			// precondition). No production or other test fixture is changed.
			f.vs("set", "Bridge", "br-field", "datapath_type=system", "--", "set", "Interface", "field-p1", "type=internal", "--", "set", "Interface", "field-p2", "type=internal")
			waitFor(t, func() bool {
				show := f.run("ovs-appctl", "--timeout=3", "-t", filepath.Join(f.root, "switch.ctl"), "dpif/show")
				return strings.Contains(show, "system@") && !strings.Contains(show, "dummy@")
			})
			suffix := repository.NewID()[:6]
			bridge, name := "brm"+suffix, "span-"+suffix
			f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system", "other_config:mirror-foreign=preserve")
			t.Cleanup(func() { f.vs("--if-exists", "del-br", bridge) })
			namespaces, ports, peers := []string{}, []string{}, []string{}
			for n := 0; n < 3; n++ {
				ns, p, peer := "nm"+suffix+strconv.Itoa(n), "pm"+suffix+strconv.Itoa(n), "cm"+suffix+strconv.Itoa(n)
				f.run("ip", "netns", "add", ns)
				t.Cleanup(func() { f.run("ip", "netns", "del", ns) })
				f.run("ip", "link", "add", p, "type", "veth", "peer", "name", peer)
				t.Cleanup(func() { _ = exec.Command("ip", "link", "del", p).Run() })
				f.run("ip", "link", "set", peer, "netns", ns)
				f.run("ip", "link", "set", p, "up")
				f.run("ip", "netns", "exec", ns, "ip", "link", "set", peer, "address", "02:00:00:66:00:0"+strconv.Itoa(n+1))
				f.run("ip", "netns", "exec", ns, "ip", "link", "set", peer, "up")
				f.run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
				f.vs("add-port", bridge, p)
				if n < 2 {
					f.vs("set", "Port", p, "vlan_mode=access", "tag=100")
					f.run("ip", "netns", "exec", ns, "ip", "addr", "add", "198.19.166."+strconv.Itoa(n+1)+"/24", "dev", peer)
				} else {
					// Exclude ordinary VLAN 100 forwarding, including unknown-
					// unicast flooding after native configuration flushes the FDB.
					// An output-Port Mirror bypasses normal VLAN membership;
					// RSPAN copies leave only on the explicitly allowed VLAN 200.
					f.vs("set", "Port", p, "vlan_mode=trunk", "trunks=200")
				}
				namespaces, ports, peers = append(namespaces, ns), append(ports, p), append(peers, peer)
			}
			warm := func() bool {
				return exec.Command("ip", "netns", "exec", namespaces[0], "ping", "-n", "-c", "1", "-W", "1", "198.19.166.2").Run() == nil
			}
			waitFor(t, warm)
			// Packet sockets run only in these three disposable namespaces. A
			// readiness barrier precedes each send; no synthetic flow emulates a
			// native Mirror. VLAN auxiliary metadata handles veth tag offload.
			capture := func(index int, token string) func() (int, int) {
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				script := `import socket,sys,time,struct
dev,token=sys.argv[1:]
s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(3));s.bind((dev,0));s.setsockopt(263,8,1);s.settimeout(.1)
print('ready',flush=True);end=time.monotonic()+1.5
while time.monotonic()<end:
 try:
  data,anc,flags,addr=s.recvmsg(4096,128)
  if addr[2]==4:continue
  vlan=-1;offset=14;typ=int.from_bytes(data[12:14],'big')
  if typ==0x8100 and len(data)>=18:vlan=int.from_bytes(data[14:16],'big')&4095;typ=int.from_bytes(data[16:18],'big');offset=18
  for level,kind,raw in anc:
   if level==263 and kind==8 and len(raw)>=20:
    status,length,snap,mac,net,tci,tpid=struct.unpack('=IIIHHHH',raw[:20])
    if status&16:vlan=tci&4095
  if typ==0x88b5 and data[offset:offset+8]==token[:8].encode():print(str(len(data))+','+str(vlan),flush=True);break
 except socket.timeout:pass
else:print('0,-1',flush=True)
`
				cmd := exec.CommandContext(ctx, "ip", "netns", "exec", namespaces[index], "python3", "-u", "-c", script, peers[index], token)
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
					t.Fatal("native Mirror capture unavailable", err, stderr.String())
				}
				return func() (int, int) {
					line, err := reader.ReadString('\n')
					waitErr := cmd.Wait()
					done = true
					cancel()
					if err != nil || waitErr != nil {
						t.Fatal("native Mirror capture failed", err, waitErr, stderr.String())
					}
					parts := strings.Split(strings.TrimSpace(line), ",")
					if len(parts) != 2 {
						t.Fatal("invalid packet evidence")
					}
					length, e1 := strconv.Atoi(parts[0])
					vlan, e2 := strconv.Atoi(parts[1])
					must(t, e1)
					must(t, e2)
					return length, vlan
				}
			}
			phase := "before-create"
			probe := func(from, to int, want bool, wantSnap int, wantVLAN int) {
				// Test-fixture barrier only: the first cycle may already be running
				// with an older revalidation sequence. Two successive barriers cover
				// a complete cycle started after the settled native configuration.
				// This is not a production Applied or delivery guarantee.
				// Upstream: ofproto/ofproto-dpif-upcall.c (v3.3.9), revalidator/wait.
				f.run("ovs-appctl", "--timeout=3", "-t", filepath.Join(f.root, "switch.ctl"), "revalidator/wait")
				f.run("ovs-appctl", "--timeout=3", "-t", filepath.Join(f.root, "switch.ctl"), "revalidator/wait")
				token := repository.NewID()
				receive := capture(2, token)
				f.run("ip", "netns", "exec", namespaces[from], "python3", "-c", `import socket,sys
dev,src,dst,token=sys.argv[1:]
s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW);s.bind((dev,0));frame=bytes.fromhex('02000066000'+dst+'02000066000'+src+'88b5')+token.encode()+b'x'*64;s.send(frame)
`, peers[from], strconv.Itoa(from+1), strconv.Itoa(to+1), token)
				length, vlan := receive()
				if (length > 0) != want || wantSnap > 0 && length != wantSnap || wantVLAN >= 0 && vlan != wantVLAN {
					t.Fatalf("native copy mismatch scenario=%s phase=%s from=%d to=%d length=%d vlan=%d want=%v snap=%d vlan=%d native_mirrors=%s counters=%s", scenario, phase, from, to, length, vlan, want, wantSnap, wantVLAN, f.vs("get", "Bridge", bridge, "mirrors"), f.vs("get", "Open_vSwitch", ".", "next_cfg", "cur_cfg"))
				}
			}
			probe(0, 1, false, 0, -1)
			var role string
			must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
				return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
			}))
			must(t, f.auth.SetMirrorExecutionRole(f.ctx, role, true))
			fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
			must(t, err)
			f.login = fresh
			binding := f.grantNativeMirror(bridge, name)
			source, output := f.topologyNode("Port", ports[0]).Binding, f.topologyNode("Port", ports[2]).Binding
			config := mirror.Configuration{SourcePorts: []string{source.ManagementID}, DestinationPorts: []string{}, VLANs: []string{"100"}, OutputPort: &output.ManagementID}
			if scenario == "destination_direction" {
				config.SourcePorts, config.DestinationPorts = []string{}, []string{source.ManagementID}
			}
			if scenario == "all_ports_vlan_filter" {
				config.SelectAll, config.SourcePorts = true, []string{}
			}
			if scenario == "snaplen" {
				value := "32"
				config.Snaplen = &value
			}
			if scenario == "rspan_vlan" {
				value := "200"
				config.OutputPort, config.OutputVLAN = nil, &value
			}
			var offset atomic.Int64
			f.configureSafety(&offset)
			id := f.safeApply(f.prepareTopology(binding, candidate.MirrorCreate, candidate.TopologyRequest{Name: name, Mirror: &config}))
			f.waitTopology(id, "awaiting-confirmation")
			phase = "created"
			probeCurrent := func() {
				snap, vlan := 0, -1
				if scenario == "snaplen" {
					snap = 32
				}
				if scenario == "rspan_vlan" {
					vlan = 200
				}
				probe(0, 1, scenario != "destination_direction", snap, vlan)
				if scenario == "destination_direction" || scenario == "source_direction" || scenario == "all_ports_vlan_filter" {
					probe(1, 0, scenario != "source_direction", 0, -1)
				}
			}
			probeCurrent()
			f.decide(id, "confirm")
			f.waitTopology(id, "confirmed")
			object := f.topologyNode("Mirror", name).Binding
			f.vs("set", "Mirror", name, "external_ids:mirror-foreign=preserve")
			waitFor(t, func() bool {
				return f.topologySnapshot().Topology.Configurations[object.ManagementID]["external_ids"].(map[string]any)["mirror-foreign"] == "preserve"
			})
			original := f.vs("get", "Mirror", name, "select_all", "select_src_port", "select_dst_port", "select_vlan", "output_port", "output_vlan", "snaplen", "external_ids")
			originalFields := f.vs("get", "Mirror", name, "select_all", "select_src_port", "select_dst_port", "select_vlan", "output_port", "output_vlan", "snaplen")
			f.grantNativeMirror(bridge, name)
			f.discardPortDraft()
			changed := mirror.Clone(config)
			changed.VLANs = []string{"101"}
			id = f.safeApply(f.prepareTopology(object, candidate.MirrorConfigure, candidate.TopologyRequest{Mirror: &changed}))
			f.waitTopology(id, "awaiting-confirmation")
			phase = "vlan-filter-changed"
			probe(0, 1, false, 0, -1)
			probe(1, 0, false, 0, -1)
			f.decide(id, "rollback")
			f.waitTopology(id, "rolled-back")
			phase = "configuration-restored"
			if f.topologyNode("Mirror", name).Binding != object || f.vs("get", "Mirror", name, "select_all", "select_src_port", "select_dst_port", "select_vlan", "output_port", "output_vlan", "snaplen", "external_ids") != original {
				t.Fatal("Mirror edit changed identity or raw originals")
			}
			probeCurrent()
			f.discardPortDraft()
			id = f.safeApply(f.prepareTopology(object, candidate.MirrorDelete, candidate.TopologyRequest{}))
			f.waitTopology(id, "awaiting-confirmation")
			phase = "deleted"
			if f.vs("get", "Bridge", bridge, "mirrors") != "[]" {
				t.Fatal("deleted Mirror retained strong Bridge reference")
			}
			if scenario == "destination_direction" {
				probe(1, 0, false, 0, -1)
			} else {
				probe(0, 1, false, 0, -1)
			}
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
			f.grantNativeMirror(bridge, name)
			f.workspace, err = web.NewWorkspace(f.webStore, f.auth)
			must(t, err)
			offset.Store(int64(121 * time.Second))
			f.configureSafety(&offset)
			f.waitTopology(id, "rolled-back")
			phase = "deletion-restored"
			restored := f.topologyNode("Mirror", name).Binding
			if restored.ManagementID == object.ManagementID || restored.OVSUUID == object.OVSUUID || f.proxy.sent.Load() != before+1 {
				t.Fatal("Mirror recovery reused identity or mutation")
			}
			if f.vs("get", "Mirror", name, "select_all", "select_src_port", "select_dst_port", "select_vlan", "output_port", "output_vlan", "snaplen") != originalFields || f.vs("get", "Mirror", name, "external_ids:mirror-foreign") != "preserve" {
				t.Fatal("fresh Mirror recovery lost native fields")
			}
			probeCurrent()
			settled := f.vs("get", "Open_vSwitch", ".", "next_cfg")
			must(t, f.engine.Recover(f.ctx))
			must(t, f.engine.SafetyTick(f.ctx))
			if settled != f.vs("get", "Open_vSwitch", ".", "next_cfg") {
				t.Fatal("terminal Mirror recovery replayed")
			}
			offset.Store(0)
			f.grantNativeMirror(bridge, name)
			f.discardPortDraft()
			in := f.prepareTopology(restored, candidate.MirrorConfigure, candidate.TopologyRequest{Mirror: &changed})
			plan, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("f", 64), in.Envelope)
			must(t, err)
			f.proxy.hook(func() { f.vs("set", "Interface", "field-p1", "external_ids:late-mirror=preserve") })
			outcome := f.executor.Commit(f.ctx, plan, func() error { return nil })
			if outcome.Commit != "rejected" || f.vs("get", "Mirror", name, "select_vlan") != "[100]" {
				t.Fatal("late unrelated native dependency bypassed Mirror CAS", outcome)
			}
			waitFor(t, func() bool {
				node := f.topologyNode("Interface", "field-p1")
				return f.topologySnapshot().Topology.Configurations[node.Binding.ManagementID]["external_ids"].(map[string]any)["late-mirror"] == "preserve"
			})
			f.discardPortDraft()
			in = f.prepareTopology(restored, candidate.MirrorConfigure, candidate.TopologyRequest{Mirror: &changed})
			plan, err = f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("e", 64), in.Envelope)
			must(t, err)
			f.proxy.dropReply.Store(true)
			sent := f.proxy.sent.Load()
			outcome = f.executor.Commit(f.ctx, plan, func() error { return nil })
			if outcome.Commit != "unknown" || outcome.Target != nil || outcome.Applied == "applied" {
				t.Fatal("lost Mirror reply invented installation", outcome)
			}
			waitFor(t, func() bool { return f.vs("get", "Mirror", name, "select_vlan") == "[101]" })
			for n := 0; n < 3; n++ {
				outcome = f.executor.Observe(f.ctx, plan, outcome)
				if outcome.Target != nil || outcome.Applied == "applied" {
					t.Fatal("Mirror observation invented lost target")
				}
			}
			f.vs("set", "Mirror", name, "select_vlan=102")
			waitFor(t, func() bool {
				return candidate.Digest(f.topologySnapshot().Topology.Configurations[restored.ManagementID]["select_vlan"]) == candidate.Digest([]any{"102"})
			})
			if _, err := f.executor.PrepareRollback(f.ctx, plan, strings.Repeat("d", 64)); err == nil {
				t.Fatal("conflicting Mirror original was overwritten")
			}
			if f.proxy.sent.Load() != sent+1 {
				t.Fatal("lost Mirror reply was replayed")
			}
		})
	}
}
