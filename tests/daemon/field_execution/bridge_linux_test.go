//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
)

func newBridgeFixture(t *testing.T, kernel bool) (*fixture, string) {
	f := newFixture(t)
	name := "brc-" + repository.NewID()[:8]
	if !kernel {
		f.stop("switch")
		f.run("ovs-vswitchd", "--enable-dummy=system", "--pidfile="+filepath.Join(f.root, "switch.pid"), "--unixctl="+filepath.Join(f.root, "switch.ctl"), "--detach", "--no-chdir", "unix:"+f.dbSocket)
	} else {
		f.run("modprobe", "openvswitch")
	}
	must(t, f.inventory.SetLocalBridgeNames([]string{name}))
	return f, name
}
func (f *fixture) prepareBridge(name string) execution.Request {
	return f.prepareIntents([]any{map[string]any{"intent_id": repository.NewID(), "operation": candidate.BridgeCreate, "name": name}})
}
func (f *fixture) graphStates(i candidate.StoredIntent, state string) {
	f.t.Helper()
	waitFor(f.t, func() bool {
		for _, b := range candidate.CreationBindings(i) {
			var got string
			err := f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
				return q.QueryRowContext(ctx, "SELECT state FROM identities WHERE management_id=? AND generation=? AND table_name=? AND ovs_uuid=?", b.ManagementID, b.Generation, b.Table, b.OVSUUID).Scan(&got)
			})
			if err != nil || got != state {
				return false
			}
		}
		return true
	})
}
func (f *fixture) graphAbsent(i candidate.StoredIntent) {
	f.t.Helper()
	for _, b := range candidate.CreationBindings(i) {
		if f.vs("--if-exists", "get", b.Table, b.OVSUUID, "_uuid") != "" {
			f.t.Fatal("uncollected created row", b)
		}
	}
	f.graphStates(i, "tombstone")
}

func TestNativeIsolatedBridge(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated Bridge lifecycle matrix")
	}
	t.Run("create_confirm_preserves_graph_identity_and_native_local_port", func(t *testing.T) {
		f, name := newBridgeFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareBridge(name)
		i := in.Envelope.Candidate.Intents[0]
		if f.proxy.sent.Load() != 0 || f.vs("--if-exists", "get", "Bridge", name, "_uuid") != "" {
			t.Fatal("draft wrote live OVS")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.graphStates(i, "active")
		for _, b := range candidate.CreationBindings(i) {
			if f.vs("get", b.Table, name, "_uuid") != b.OVSUUID {
				t.Fatal("assigned identity replaced", b)
			}
		}
		if f.vs("get", "Interface", name, "type") != "internal" || f.vs("get", "Interface", name, "ofport") != "65534" || f.vs("get", "Bridge", name, "fail_mode") != "secure" {
			t.Fatal("incorrect local graph")
		}
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("duplicate mutation")
		}
	})
	t.Run("rollback_GC_tombstones_and_same_name_is_new_identity", func(t *testing.T) {
		f, name := newBridgeFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareBridge(name)
		i := in.Envelope.Candidate.Intents[0]
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(i)
		if _, err := f.executor.Prepare(f.ctx, repository.NewID(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", in.Envelope); err == nil {
			t.Fatal("old graph UUIDs can be reused")
		}
		f.vs("add-br", name, "--", "set", "Bridge", name, "datapath_type=system")
		newUUID := f.vs("get", "Bridge", name, "_uuid")
		waitFor(t, func() bool {
			var mid string
			err := f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
				return q.QueryRowContext(ctx, "SELECT management_id FROM identities WHERE ovs_uuid=? AND state='active'", newUUID).Scan(&mid)
			})
			return err == nil && mid != i.Object.ManagementID
		})
		f.graphStates(i, "tombstone")
	})
	t.Run("late_name_collision_aborts_without_adopting_or_leaking_rows", func(t *testing.T) {
		f, name := newBridgeFixture(t, false)
		in := f.prepareBridge(name)
		var external string
		f.proxy.hook(func() {
			f.vs("add-br", name, "--", "set", "Bridge", name, "datapath_type=system")
			external = f.vs("get", "Bridge", name, "_uuid")
		})
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || external == "" || f.vs("get", "Bridge", name, "_uuid") != external {
			t.Fatal("name was adopted/replaced", r)
		}
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
	})
	for _, change := range []string{"member", "weak_reference", "unmonitored_configuration"} {
		t.Run("rollback_refuses_external_"+change, func(t *testing.T) {
			f, name := newBridgeFixture(t, false)
			var offset atomic.Int64
			f.configureSafety(&offset)
			in := f.prepareBridge(name)
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			// Modify after rollback preflight, immediately before the native transaction.
			// Neither a fresh monitor nor a preflight-only check can catch this race.
			f.proxy.hook(func() {
				switch change {
				case "member":
					f.vs("add-port", name, name+"p", "--", "set", "Interface", name+"p", "type=dummy")
				case "weak_reference":
					f.vs("--id=@m", "create", "Mirror", "name=external-ref", "select_src_port="+in.Envelope.Candidate.Intents[0].Creation.Port.OVSUUID, "output_port="+f.binding("field-p1").OVSUUID, "--", "add", "Bridge", "br-field", "mirrors", "@m")
				case "unmonitored_configuration":
					f.vs("set", "Interface", name, "ingress_policing_rate=64")
				}
			})
			f.decide(id, "rollback")
			f.waitSafety(id, "rollback-conflict")
			f.graphStates(in.Envelope.Candidate.Intents[0], "active")
			if f.vs("get", "Bridge", name, "_uuid") != in.Envelope.Candidate.Intents[0].Object.OVSUUID || f.proxy.sent.Load() != 2 {
				t.Fatal("external graph was removed or replayed")
			}
		})
	}
	t.Run("lost_creation_reply_recovers_commit_only_without_replay", func(t *testing.T) {
		f, name := newBridgeFixture(t, false)
		in := f.prepareBridge(name)
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Commit != "unknown" {
			t.Fatal(r)
		}
		restarted, err := executions.New(f.store, key, f.executor)
		must(t, err)
		f.engine = restarted
		must(t, f.engine.Recover(f.ctx))
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Target != nil || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 1 {
			t.Fatal("lost target invented or transaction replayed", r)
		}
		f.graphStates(in.Envelope.Candidate.Intents[0], "active")
	})
	t.Run("revoked_creation_capability_compensates", func(t *testing.T) {
		f, name := newBridgeFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareBridge(name)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		caps := slices.DeleteFunc(append([]string{}, f.login.Claims.Capabilities...), func(s string) bool { return s == "ovs.bridge.create" })
		body, _ := json.Marshal(caps)
		must(t, f.store.Write(f.ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, f.login.Claims.PrincipalID)
			return err
		}))
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
	})
	t.Run("lost_cleanup_reply_retains_recovery_and_never_replays_GC", func(t *testing.T) {
		f, name := newBridgeFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareBridge(name)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		waitFor(t, func() bool { must(t, f.engine.SafetyTick(f.ctx)); return f.safeState(id).Outcome.Commit == "committed" })
		s := f.safeState(id)
		if s.Outcome.Target != nil || s.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 2 {
			t.Fatal("cleanup invented Applied or replayed", s)
		}
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
	})
	t.Run("kernel_local_interface_create_and_cleanup", func(t *testing.T) {
		f, name := newBridgeFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareBridge(name)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		if _, err := net.InterfaceByName(name); err != nil {
			t.Fatal("Applied without kernel internal interface", err)
		}
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
		waitFor(t, func() bool { _, err := net.InterfaceByName(name); return err != nil })
	})
}
