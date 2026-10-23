//go:build linux

package preflight

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

// Runs only in the existing disposable namespace, with the exact actual OVS
// build and its root-pinned manifest. No host OVS instance is discovered.
func TestNativeMulticastGroups(t *testing.T) {
	root := os.Getenv("OVS_WITNESS_ROOT")
	if root == "" {
		t.Skip("requires disposable patched OVS kernel fixture")
	}
	if os.Geteuid() != 0 || !filepath.IsAbs(root) {
		t.Fatal("isolated root fixture required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("isolated fixture: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	vs := func(args ...string) string {
		return run(filepath.Join(root, "bin/ovs-vsctl"), append([]string{"--timeout=5", "--db=unix:" + filepath.Join(root, "db.sock")}, args...)...)
	}
	name := "br-mcast-groups"
	vs("add-br", name, "--", "set", "Bridge", name, "datapath_type=system")
	t.Cleanup(func() { vs("--if-exists", "del-br", name) })
	uuid := vs("get", "Bridge", name, "_uuid")
	dir := t.TempDir()
	options := sqlite.Options{Path: filepath.Join(dir, "manager.db"), Kind: repository.Manager, SoftwareVersion: "native-multicast-groups"}
	must(t, sqlite.Initialize(ctx, options))
	store, err := sqlite.Open(ctx, options)
	must(t, err)
	defer store.Close()
	r, err := registry.New(store)
	must(t, err)
	inv := inventory.New(r)
	p, err := ovsdb.New(ovsdb.Options{Socket: filepath.Join(root, "db.sock"), DatabaseFile: filepath.Join(root, "conf.db"), PeerUID: 0})
	must(t, err)
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx, inv) }()
	defer func() { cancel(); <-done }()
	setupWait := func(check func() bool) {
		t.Helper()
		until := time.Now().Add(6 * time.Second)
		for !check() {
			if time.Now().After(until) {
				t.Fatal("native fixture barrier did not settle")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	var binding inventory.Binding
	setupWait(func() bool {
		return store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,state FROM identities WHERE table_name='Bridge' AND state='active' AND ovs_uuid=?", uuid).Scan(&binding.ManagementID, &binding.UUID, &binding.Table, &binding.State)
		}) == nil
	})
	manifest := filepath.Join(root, "preflight-build.json")
	data, err := os.ReadFile(manifest)
	must(t, err)
	hash := sha256.Sum256(data)
	opt := ovsdb.MulticastGroupOptions{ControlSocket: filepath.Join(root, "vsd.ctl"), Executable: filepath.Join(root, "bin/ovs-vswitchd"), Manifest: manifest, ManifestDigest: hex.EncodeToString(hash[:]), PeerUID: 0}
	observer, err := p.MulticastGroupObserver(opt)
	must(t, err)
	inv.SetMulticastGroupObserver(observer)
	claims := authn.Claims{PrincipalID: repository.NewID(), Revision: repository.NewID(), Capabilities: []string{"inventory.read", "configuration.read"}}
	read := func(c authn.Claims) map[string]any {
		t.Helper()
		out, err := inv.Read(ctx, "readBridge", map[string]string{"id": binding.ManagementID}, url.Values{}, c)
		must(t, err)
		return out.(map[string]any)["multicast_groups"].(map[string]any)
	}
	settled := func(enabled bool) {
		t.Helper()
		target := vs("get", "Open_vSwitch", ".", "next_cfg")
		setupWait(func() bool {
			out, err := inv.Read(ctx, "readInventory", nil, url.Values{}, claims)
			if err != nil {
				return false
			}
			progress := out.(map[string]any)["ovs_progress"].(map[string]any)
			if progress["next_cfg"] != target || progress["cur_cfg"] != target {
				return false
			}
			row, err := inv.Read(ctx, "readBridge", map[string]string{"id": binding.ManagementID}, url.Values{}, authn.Claims{Capabilities: []string{"inventory.read"}})
			if err != nil {
				return false
			}
			_ = row
			return vs("get", "Bridge", name, "mcast_snooping_enable") == map[bool]string{true: "true", false: "false"}[enabled]
		})
	}
	t.Run("disabled_is_unavailable_not_empty", func(t *testing.T) {
		settled(false)
		out := read(claims)
		if out["reason"] != "MULTICAST_SNOOPING_DISABLED" || out["items"] != nil {
			t.Fatal(out)
		}
	})
	vs("set", "Bridge", name, "mcast_snooping_enable=true")
	settled(true)
	t.Run("enabled_empty_table_is_observed_known", func(t *testing.T) {
		out := read(claims)
		if out["availability"] != "known" || len(out["items"].([]any)) != 0 {
			t.Fatal(out)
		}
	})
	run("ip", "link", "add", "mc-groups-p", "type", "veth", "peer", "name", "mc-groups-peer")
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "mc-groups-p").Run() })
	vs("add-port", name, "mc-groups-p")
	run("ip", "link", "set", "mc-groups-p", "up")
	run("ip", "addr", "add", "198.19.203.1/24", "dev", "mc-groups-peer")
	run("ip", "link", "set", "mc-groups-peer", "up")
	run("sysctl", "-q", "-w", "net.ipv4.conf.mc-groups-peer.force_igmp_version=2")
	listener := exec.CommandContext(ctx, "python3", "-u", "-c", `import socket,time
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('',20001));s.setsockopt(socket.IPPROTO_IP,socket.IP_ADD_MEMBERSHIP,socket.inet_aton('239.192.0.9')+socket.inet_aton('198.19.203.1'))
print('ready',flush=True);time.sleep(60)
`)
	stdout, err := listener.StdoutPipe()
	must(t, err)
	must(t, listener.Start())
	defer func() { _ = listener.Process.Kill(); _ = listener.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	must(t, err)
	if strings.TrimSpace(line) != "ready" {
		t.Fatal("listener fixture did not start")
	}
	setupWait(func() bool {
		raw := run(filepath.Join(root, "bin/ovs-appctl"), "-t", filepath.Join(root, "vsd.ctl"), "mdb/show", name)
		groups, err := multicast.DecodeGroups([]byte(raw))
		if err != nil {
			return false
		}
		for _, g := range groups {
			if g.Address == "239.192.0.9" {
				return true
			}
		}
		return false
	})
	settled(true)
	t.Run("igmp_member_maps_to_current_immutable_port", func(t *testing.T) {
		out := read(claims)
		if out["availability"] != "known" {
			t.Fatal(out)
		}
		found := false
		for _, value := range out["items"].([]any) {
			g := value.(map[string]any)
			if g["address"] == "239.192.0.9" {
				if g["port_name"] != "mc-groups-p" || g["protocol"] != "IGMPv2" {
					t.Fatal(g)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("native listener not observed", out)
		}
	})
	t.Run("mld_member_maps_to_current_immutable_port", func(t *testing.T) {
		run("sysctl", "-q", "-w", "net.ipv6.conf.mc-groups-peer.disable_ipv6=0")
		run("ip", "-6", "addr", "add", "fe80::203/64", "nodad", "dev", "mc-groups-peer")
		run("sysctl", "-q", "-w", "net.ipv6.conf.mc-groups-peer.force_mld_version=2")
		mld := exec.CommandContext(ctx, "python3", "-u", "-c", `import socket,struct,time
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM);s.bind(('::',20003));s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_JOIN_GROUP,socket.inet_pton(socket.AF_INET6,'ff3e::1234')+struct.pack('=I',socket.if_nametoindex('mc-groups-peer')))
print('ready',flush=True);time.sleep(60)
`)
		stdout, err := mld.StdoutPipe()
		must(t, err)
		must(t, mld.Start())
		defer func() { _ = mld.Process.Kill(); _ = mld.Wait() }()
		line, err := bufio.NewReader(stdout).ReadString('\n')
		must(t, err)
		if strings.TrimSpace(line) != "ready" {
			t.Fatal("MLD fixture did not start")
		}
		setupWait(func() bool {
			raw := run(filepath.Join(root, "bin/ovs-appctl"), "-t", filepath.Join(root, "vsd.ctl"), "mdb/show", name)
			groups, err := multicast.DecodeGroups([]byte(raw))
			if err != nil {
				return false
			}
			for _, g := range groups {
				if g.Address == "ff3e::1234" && g.Protocol == "MLDv2" {
					return true
				}
			}
			return false
		})
		out := read(claims)
		if out["availability"] != "known" {
			t.Fatal(out)
		}
		found := false
		for _, value := range out["items"].([]any) {
			g := value.(map[string]any)
			if g["address"] == "ff3e::1234" {
				if g["port_name"] != "mc-groups-p" || g["protocol"] != "MLDv2" {
					t.Fatal(g)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("MLD listener not observed", out)
		}
	})
	t.Run("configuration_withheld_returns_no_membership", func(t *testing.T) {
		c := claims
		c.Capabilities = []string{"inventory.read"}
		out := read(c)
		if out["availability"] != "withheld" || out["items"] != nil {
			t.Fatal(out)
		}
	})
	t.Run("unqualified_build_never_becomes_empty", func(t *testing.T) {
		bad := opt
		bad.ManifestDigest = strings.Repeat("0", 64)
		other, err := p.MulticastGroupObserver(bad)
		must(t, err)
		inv.SetMulticastGroupObserver(other)
		out := read(claims)
		if out["availability"] != "unavailable" || out["items"] != nil {
			t.Fatal(out)
		}
		inv.SetMulticastGroupObserver(observer)
	})
	t.Run("same_name_recreation_cannot_inherit_uuid_witness", func(t *testing.T) {
		evidence, err := inv.Previous(ctx)
		must(t, err)
		vs("del-br", name, "--", "add-br", name, "--", "set", "Bridge", name, "mcast_snooping_enable=true")
		out, err := observer.ObserveMulticast(ctx, inventory.MulticastGroupRequest{Bridge: binding, Name: name, Evidence: evidence})
		if err == nil || out.Groups != nil {
			t.Fatal("old UUID inherited same-name groups", out, err)
		}
	})
}
