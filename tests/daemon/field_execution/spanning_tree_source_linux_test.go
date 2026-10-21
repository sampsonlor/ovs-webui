//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
)

type treeSourceSink struct {
	mu       sync.Mutex
	evidence inventory.Evidence
}

func (s *treeSourceSink) Previous(context.Context) (inventory.Evidence, error) {
	return inventory.Evidence{}, nil
}
func (s *treeSourceSink) Publish(_ context.Context, o inventory.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidence = o.Evidence
	return nil
}
func (s *treeSourceSink) Unavailable(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidence = inventory.Evidence{}
}
func (s *treeSourceSink) get() inventory.Evidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evidence
}

func TestNativeSpanningTreeSource(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires isolated native OVS")
	}
	f := newFixture(t)
	f.run("ovs-vsctl", "--timeout=5", "--db=unix:"+f.dbSocket, "add-br", "br-source", "--", "set", "Bridge", "br-source", "datapath_type=dummy", "stp_enable=true")
	control := ovsdb.SpanningTreeControl{Socket: filepath.Join(f.root, "switch.ctl"), PeerUID: uint32(os.Geteuid())}
	initial, err := ovsdb.ReadSpanningTreeRuntime(f.ctx, control, "stp", "br-source")
	must(t, err)
	control.ExpectedDaemon = initial.Daemon
	db := ovsdb.Options{Socket: f.dbSocket, DatabaseFile: f.conf, PeerUID: uint32(os.Geteuid())}
	provider, err := ovsdb.New(db)
	must(t, err)
	ctx, cancel := context.WithCancel(f.ctx)
	sink := &treeSourceSink{}
	done := make(chan struct{})
	go func() { defer close(done); provider.Run(ctx, sink) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { return !sink.get().ObservedAt.IsZero() })
	executable, err := exec.LookPath("ovs-vswitchd")
	must(t, err)
	allow := func(context.Context) error { return nil }
	read := func(ctl ovsdb.SpanningTreeControl, options ovsdb.Options, evidence inventory.Evidence, exe string, check func(context.Context) error) (ovsdb.SpanningTreeRuntimeSample, error) {
		return ovsdb.ReadBoundSpanningTreeRuntime(ctx, ctl, options, evidence, exe, "stp", "br-source", check)
	}
	noSample := func(sample ovsdb.SpanningTreeRuntimeSample, err error) {
		t.Helper()
		if err == nil || !reflect.DeepEqual(sample, ovsdb.SpanningTreeRuntimeSample{}) {
			t.Fatal("unproven source produced a sample", sample, err)
		}
	}
	t.Run("same_daemon_live_connection_schema_root_and_file", func(t *testing.T) {
		sample, err := read(control, db, sink.get(), executable, allow)
		must(t, err)
		if sample.Daemon != initial.Daemon || sample.Runtime.Local != initial.Runtime.Local {
			t.Fatal(sample, initial)
		}
	})
	t.Run("builtin_prepare_and_apply_permissions_do_not_authorize_source", func(t *testing.T) {
		_, err := read(control, db, sink.get(), executable, f.auth.SpanningTreeExecutionAuthorizer(f.login.Grant))
		if auth.ErrorCode(err) != "CAPABILITY_DENIED" {
			t.Fatal(err)
		}
	})
	t.Run("explicit_role_current_permission_and_old_credential_ceiling", func(t *testing.T) {
		caps := append(append([]string{}, f.login.Claims.Capabilities...), auth.SpanningTreeExecutionCapability)
		body, _ := json.Marshal(caps)
		must(t, f.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, f.login.Claims.PrincipalID)
			return err
		}))
		_, err := read(control, db, sink.get(), executable, f.auth.SpanningTreeExecutionAuthorizer(f.login.Grant))
		if auth.ErrorCode(err) != "CAPABILITY_DENIED" {
			t.Fatal("old credential ceiling expanded", err)
		}
		fresh, err := f.auth.Authenticate(ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
		must(t, err)
		_, err = read(control, db, sink.get(), executable, f.auth.SpanningTreeExecutionAuthorizer(fresh.Grant))
		must(t, err)
	})
	t.Run("same_uid_proxy_cannot_impersonate_database_connection", func(t *testing.T) {
		other := db
		other.Socket = f.proxySocket
		e := sink.get()
		e.Endpoint = f.proxySocket
		noSample(read(control, other, e, executable, allow))
	})
	t.Run("other_real_database_with_same_named_bridge_has_no_daemon_link", func(t *testing.T) {
		other := newFixture(t)
		other.run("ovs-vsctl", "--timeout=5", "--db=unix:"+other.dbSocket, "add-br", "br-source", "--", "set", "Bridge", "br-source", "datapath_type=dummy", "stp_enable=true")
		options := ovsdb.Options{Socket: other.dbSocket, DatabaseFile: other.conf, PeerUID: uint32(os.Geteuid())}
		p, err := ovsdb.New(options)
		must(t, err)
		otherCtx, stop := context.WithCancel(ctx)
		otherSink := &treeSourceSink{}
		finished := make(chan struct{})
		go func() { defer close(finished); p.Run(otherCtx, otherSink) }()
		defer func() { stop(); <-finished }()
		waitFor(t, func() bool { return !otherSink.get().ObservedAt.IsZero() })
		sample, err := read(control, options, otherSink.get(), executable, allow)
		noSample(sample, err)
		if err.Error() != "SPANNING_TREE_SOURCE_CONNECTION_UNPROVEN" {
			t.Fatal("unrelated instance was not rejected by the actual connection gate", err)
		}
	})
	for _, scenario := range []string{"root", "schema", "peer", "file", "stale", "future"} {
		t.Run("changed_"+scenario+"_rejects_source", func(t *testing.T) {
			e := sink.get()
			switch scenario {
			case "root":
				e.Root = repository.NewID()
			case "schema":
				e.Schema = "wrong-schema"
			case "peer":
				e.Peer = "wrong-peer"
			case "file":
				e.File.Inode++
			case "stale":
				e.ObservedAt = time.Now().Add(-2 * inventory.FreshFor)
			case "future":
				e.ObservedAt = time.Now().Add(inventory.FreshFor)
			}
			sample, err := read(control, db, e, executable, allow)
			noSample(sample, err)
			codes := map[string]string{"root": "ROOT", "schema": "SCHEMA", "peer": "DATABASE_PEER", "file": "FILE", "stale": "EVIDENCE", "future": "EVIDENCE"}
			if err.Error() != "SPANNING_TREE_SOURCE_"+codes[scenario]+"_UNPROVEN" {
				t.Fatal("source rejected at the wrong boundary", scenario, err)
			}
		})
	}
	t.Run("unrelated_executable_and_replaced_daemon_denied", func(t *testing.T) {
		wrong, err := exec.LookPath("true")
		must(t, err)
		noSample(read(control, db, sink.get(), wrong, allow))
		changed := control
		changed.ExpectedDaemon += ":replaced"
		noSample(read(changed, db, sink.get(), executable, allow))
	})
	t.Run("revocation_after_runtime_read_withholds_entire_sample", func(t *testing.T) {
		calls := 0
		denied := errors.New("revoked")
		check := func(context.Context) error {
			calls++
			if calls >= 3 {
				return denied
			}
			return nil
		}
		sample, err := read(control, db, sink.get(), executable, check)
		noSample(sample, err)
		if !errors.Is(err, denied) || calls != 3 {
			t.Fatal("post-read recheck missing", calls, err)
		}
	})
}
