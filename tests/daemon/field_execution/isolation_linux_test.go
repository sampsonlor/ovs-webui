//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
)

func TestNativePortIsolation(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires explicit isolated native kernel OVS fixture")
	}
	f := newFixture(t)
	suffix := repository.NewID()[:6]
	bridge := "bi" + suffix
	f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", bridge) })
	namespaces := []string{}
	ports := []string{}
	for n := 1; n <= 3; n++ {
		ns, p, peer := "ni"+suffix+strconv.Itoa(n), "pi"+suffix+strconv.Itoa(n), "ci"+suffix+strconv.Itoa(n)
		f.run("ip", "netns", "add", ns)
		namespaces = append(namespaces, ns)
		t.Cleanup(func() { f.run("ip", "netns", "del", ns) })
		f.run("ip", "link", "add", p, "type", "veth", "peer", "name", peer)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", p).Run() })
		f.run("ip", "link", "set", peer, "netns", ns)
		f.run("ip", "netns", "exec", ns, "ip", "addr", "add", "198.19.201."+strconv.Itoa(n)+"/24", "dev", peer)
		f.run("ip", "netns", "exec", ns, "ip", "link", "set", peer, "up")
		f.run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
		f.vs("add-port", bridge, p)
		f.run("ip", "link", "set", p, "up")
		ports = append(ports, p)
	}
	f.vs("set", "Port", ports[1], "protected=true")
	var binding candidate.Binding
	uuid := f.vs("get", "Port", ports[0], "_uuid")
	waitFor(t, func() bool {
		return f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Port' AND state='active' AND ovs_uuid=?", uuid).Scan(&binding.ManagementID, &binding.OVSUUID, &binding.Table, &binding.Generation)
		}) == nil
	})
	must(t, f.inventory.SetLocalIsolationPorts([]string{binding.ManagementID}))
	var role string
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
	}))
	must(t, f.auth.SetIsolationExecutionRole(f.ctx, role, true))
	fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	f.login = fresh
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{binding})
		return err == nil && candidate.IsolationEditable(s.Isolations[binding.ManagementID])
	})
	ping := func(to int) bool {
		return exec.Command("ip", "netns", "exec", namespaces[0], "ping", "-n", "-c", "1", "-W", "1", "198.19.201."+strconv.Itoa(to)).Run() == nil
	}
	waitFor(t, func() bool { return ping(2) && ping(3) })
	var offset atomic.Int64
	f.configureSafety(&offset)
	in := f.prepareIntents([]map[string]any{{"intent_id": repository.NewID(), "operation": candidate.PortIsolationSet, "object": binding, "protected": true}})
	id := f.safeApply(in)
	f.waitSafety(id, "awaiting-confirmation")
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		var n int
		err := q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", id).Scan(&n)
		if err == nil && n != 10 {
			t.Fatal("did not reserve root, Bridge and every Port/Interface", n)
		}
		return err
	}))
	if f.vs("get", "Port", ports[0], "protected") != "true" || ping(2) || !ping(3) {
		t.Fatal("native protected peer separation / unprotected communication failed")
	}
	f.decide(id, "rollback")
	f.waitSafety(id, "rolled-back")
	if f.vs("get", "Port", ports[0], "protected") != "false" || !ping(2) || !ping(3) {
		t.Fatal("exact compensation did not restore native forwarding")
	}
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		for _, op := range []string{"listEvents", "listAudit"} {
			value, err := evidence.Read(ctx, q, f.login.Claims, op, nil, url.Values{"object_id": {binding.ManagementID}}, make([]byte, 32), time.Now())
			if err != nil {
				return err
			}
			found := false
			for _, r := range value.(map[string]any)["items"].([]map[string]any) {
				if r["transaction_id"] == id {
					found = true
				}
			}
			if !found {
				t.Fatal("native Port lifecycle missing object-scoped evidence", op)
			}
		}
		return nil
	}))
	settled := f.vs("get", "Open_vSwitch", ".", "next_cfg")
	must(t, f.engine.Recover(f.ctx))
	must(t, f.engine.SafetyTick(f.ctx))
	if f.vs("get", "Open_vSwitch", ".", "next_cfg") != settled {
		t.Fatal("terminal recovery replayed a mutation")
	}
	// Independently lose this domain's native commit reply. Observing its marker
	// may recover commit knowledge, but cannot invent the exact Applied counter.
	f.discardPortDraft()
	in = f.prepareIntents([]map[string]any{{"intent_id": repository.NewID(), "operation": candidate.PortIsolationSet, "object": binding, "protected": true}})
	p, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("d", 64), in.Envelope)
	must(t, err)
	f.proxy.dropReply.Store(true)
	sent := f.proxy.sent.Load()
	out := f.executor.Commit(f.ctx, p, func() error { return nil })
	if out.Commit != "unknown" || out.Target != nil || out.Applied == "applied" {
		t.Fatal("lost reply invented native success", out)
	}
	waitFor(t, func() bool { return f.vs("get", "Port", ports[0], "protected") == "true" })
	for n := 0; n < 3; n++ {
		out = f.executor.Observe(f.ctx, p, out)
		if out.Target != nil || out.Applied == "applied" {
			t.Fatal("observation invented exact Applied", out)
		}
	}
	if f.proxy.sent.Load() != sent+1 {
		t.Fatal("lost native reply replayed a mutation")
	}
	// External changes after this unknown commit cannot be overwritten by frozen
	// compensation, even though the previous marker is still present.
	f.vs("set", "Port", ports[0], "protected=false")
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{binding})
		return err == nil && !s.Isolations[binding.ManagementID].Protected
	})
	if _, err := f.executor.PrepareRollback(f.ctx, p, strings.Repeat("e", 64)); err == nil || !strings.Contains(err.Error(), "ROLLBACK_CONFLICT") {
		t.Fatal("external field change did not block compensation", err)
	}
	if f.vs("get", "Port", ports[0], "protected") != "false" || f.proxy.sent.Load() != sent+1 {
		t.Fatal("conflicting compensation changed native state")
	}
}
