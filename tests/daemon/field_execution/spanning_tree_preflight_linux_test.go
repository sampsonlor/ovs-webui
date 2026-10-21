//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

func TestNativeSpanningTreePreflight(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires isolated native OVS and kernel system datapath")
	}
	f := newFixture(t)
	const name = "br-tree-fence"
	f.run("ovs-vsctl", "--timeout=5", "--db=unix:"+f.dbSocket, "add-br", name, "--", "set", "Bridge", name, "datapath_type=system", "stp_enable=true", "other_config:m2c3-opaque=original")
	defer f.vs("--if-exists", "del-br", name)
	// A separate, initially empty registry observes the actual DB peer directly.
	// The existing fault proxy is deliberately not an accepted runtime source.
	dir := filepath.Join(f.root, "fenced-manager")
	must(t, os.Mkdir(dir, 0700))
	options := sqlite.Options{Path: filepath.Join(dir, "manager.db"), Kind: repository.Manager, SoftwareVersion: "spanning-tree-fence-test"}
	must(t, sqlite.Initialize(f.ctx, options))
	store, err := sqlite.Open(f.ctx, options)
	must(t, err)
	defer store.Close()
	r, err := registry.New(store)
	must(t, err)
	inv := inventory.New(r)
	p, err := ovsdb.New(ovsdb.Options{Socket: f.dbSocket, DatabaseFile: f.conf, PeerUID: uint32(os.Geteuid())})
	must(t, err)
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx, inv) }()
	defer func() { cancel(); <-done }()
	var binding candidate.Binding
	lookup := func() {
		uuid := f.vs("get", "Bridge", name, "_uuid")
		waitFor(t, func() bool {
			return store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
				return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Bridge' AND state='active' AND ovs_uuid=?", uuid).Scan(&binding.ManagementID, &binding.OVSUUID, &binding.Table, &binding.Generation)
			}) == nil
		})
		must(t, inv.SetLocalSpanningTreeBridges([]string{binding.ManagementID}))
		must(t, inv.SetLocalSpanningTreeExecutionBridges([]string{binding.ManagementID}))
	}
	lookup()
	executor := p.Executor(inv)
	control := ovsdb.SpanningTreeControl{Socket: filepath.Join(f.root, "switch.ctl"), PeerUID: uint32(os.Geteuid())}
	runtime, err := ovsdb.ReadSpanningTreeRuntime(ctx, control, "stp", name)
	must(t, err)
	control.ExpectedDaemon = runtime.Daemon
	executable, err := exec.LookPath("ovs-vswitchd")
	must(t, err)
	caps := append(append([]string{}, f.login.Claims.Capabilities...), auth.SpanningTreeExecutionCapability)
	role := func(values []string) {
		body, err := json.Marshal(values)
		must(t, err)
		must(t, f.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, f.login.Claims.PrincipalID)
			return err
		}))
	}
	role(caps)
	fresh, err := f.auth.Authenticate(ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	authorize := f.auth.SpanningTreeExecutionAuthorizer(fresh.Grant)
	draft := func() candidate.StoredIntent {
		var view inventory.ExecutionView
		// ovs-vsctl completion does not mean our independent monitor has
		// delivered the new configuration yet. Await that exact native counter,
		// never retry a failed preflight or accept an earlier settled snapshot.
		target := f.vs("get", "Open_vSwitch", ".", "next_cfg")
		waitFor(t, func() bool {
			v, err := inv.SpanningTreeExecutionView(ctx, binding)
			if err != nil {
				return false
			}
			root := v.Observation.Rows["Open_vSwitch"][v.Observation.Evidence.Root]
			view = v
			return root.Values["cur_cfg"] == target && root.Values["next_cfg"] == target
		})
		tr := view.Candidate.SpanningTrees[binding.ManagementID]
		var after candidate.SpanningTreeConfig
		blob, err := json.Marshal(tr.Configuration)
		must(t, err)
		must(t, json.Unmarshal(blob, &after))
		priority := "4096"
		protocol := "stp"
		if after.RSTP {
			protocol = "rstp"
		}
		after.Parameters[protocol+"-priority"] = &priority
		return candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.SpanningTreePrepare, Object: binding, Schema: view.Candidate.Schema, Dependency: tr.Dependency, SpanningTree: &candidate.SpanningTreeChange{Before: tr.Configuration, After: after}}
	}
	noEvidence := func(out ovsdb.SpanningTreePreflight, err error) {
		t.Helper()
		if err == nil || !reflect.DeepEqual(out, ovsdb.SpanningTreePreflight{}) {
			t.Fatal("unproven preflight returned partial evidence", out, err)
		}
	}
	for _, protocol := range []string{"stp", "rstp"} {
		t.Run("integrated_"+protocol+"_system_bridge_is_read_only", func(t *testing.T) {
			f.run("ovs-vsctl", "--timeout=5", "--db=unix:"+f.dbSocket, "set", "Bridge", name, "stp_enable="+map[bool]string{true: "true", false: "false"}[protocol == "stp"], "rstp_enable="+map[bool]string{true: "true", false: "false"}[protocol == "rstp"])
			i := draft()
			original := f.vs("get", "Bridge", name, "stp_enable", "rstp_enable", "other_config")
			out, err := executor.ReadSpanningTreePreflight(ctx, i, control, executable, authorize)
			must(t, err)
			if out.Binding != binding || out.Transition.State != "compatible" || out.Sample.Runtime.Protocol != protocol || out.Sample.Runtime.BridgeName != name || f.vs("get", "Bridge", name, "stp_enable", "rstp_enable", "other_config") != original {
				t.Fatal("fenced read changed configuration or lost identity", out)
			}
		})
	}
	t.Run("old_credential_ceiling_remains_denied", func(t *testing.T) {
		out, err := executor.ReadSpanningTreePreflight(ctx, draft(), control, executable, f.auth.SpanningTreeExecutionAuthorizer(f.login.Grant))
		noEvidence(out, err)
		if auth.ErrorCode(err) != "CAPABILITY_DENIED" {
			t.Fatal(err)
		}
	})
	t.Run("prepare_root_grant_does_not_authorize_preflight", func(t *testing.T) {
		i := draft()
		must(t, inv.SetLocalSpanningTreeExecutionBridges(nil))
		defer func() { must(t, inv.SetLocalSpanningTreeExecutionBridges([]string{binding.ManagementID})) }()
		noEvidence(executor.ReadSpanningTreePreflight(ctx, i, control, executable, authorize))
	})
	for _, scenario := range []string{"schema", "dependency", "original", "generation", "closed-image"} {
		t.Run("reviewed_"+scenario+"_mismatch_denied", func(t *testing.T) {
			i := draft()
			switch scenario {
			case "schema":
				i.Schema = "other-schema"
			case "dependency":
				i.Dependency = "other-graph"
			case "original":
				i.SpanningTree.Before.STP = !i.SpanningTree.Before.STP
			case "generation":
				i.Object.Generation = repository.NewID()
			case "closed-image":
				delete(i.SpanningTree.After.Parameters, "stp-priority")
			}
			noEvidence(executor.ReadSpanningTreePreflight(ctx, i, control, executable, authorize))
		})
	}
	for _, scenario := range []string{"role", "execution-root-aba", "prepare-root-aba", "configuration-aba", "cancel"} {
		t.Run("post_runtime_"+scenario+"_withholds_all_evidence", func(t *testing.T) {
			i := draft()
			calls := 0
			readCtx, stop := context.WithCancel(ctx)
			defer stop()
			defer role(caps)
			check := func(ctx context.Context) error {
				calls++
				if calls == 5 { // after the one runtime reply, before post-source checks
					switch scenario {
					case "role":
						role(f.login.Claims.Capabilities)
					case "execution-root-aba":
						must(t, inv.SetLocalSpanningTreeExecutionBridges(nil))
						must(t, inv.SetLocalSpanningTreeExecutionBridges([]string{binding.ManagementID}))
					case "prepare-root-aba":
						must(t, inv.SetLocalSpanningTreeBridges(nil))
						must(t, inv.SetLocalSpanningTreeBridges([]string{binding.ManagementID}))
					case "configuration-aba":
						// Two real DB transactions restore the exact configuration and
						// leave next_cfg untouched. _version must still invalidate it.
						for _, value := range []string{"transient", "original"} {
							payload, _ := json.Marshal([]any{"Open_vSwitch", map[string]any{"op": "update", "table": "Bridge", "where": []any{[]any{"_uuid", "==", []any{"uuid", binding.OVSUUID}}}, "row": map[string]any{"other_config": []any{"map", []any{[]any{"m2c3-opaque", value}}}}}})
							f.run("ovsdb-client", "transact", "unix:"+f.dbSocket, string(payload))
						}
						waitFor(t, func() bool {
							v, err := inv.SpanningTreeExecutionView(ctx, binding)
							return err == nil && v.Candidate.SpanningTrees[binding.ManagementID].Dependency == i.Dependency
						})
					case "cancel":
						stop()
					}
				}
				return authorize(ctx)
			}
			out, err := executor.ReadSpanningTreePreflight(readCtx, i, control, executable, check)
			noEvidence(out, err)
			if calls < 5 {
				t.Fatal("scenario did not reach the post-runtime boundary", scenario, calls, err)
			}
			if scenario == "configuration-aba" && err.Error() != "SPANNING_TREE_NATIVE_FENCE_CHANGED" {
				t.Fatal("restored values were not rejected by the native version fence", err)
			}
		})
	}
	t.Run("unavailable_control_does_not_fall_back_to_configuration", func(t *testing.T) {
		ctl := control
		ctl.Socket = filepath.Join(f.root, "missing.ctl")
		noEvidence(executor.ReadSpanningTreePreflight(ctx, draft(), ctl, executable, authorize))
	})
	t.Run("same_name_bridge_replacement_loses_immutable_binding", func(t *testing.T) {
		i := draft()
		f.run("ovs-vsctl", "--timeout=5", "--db=unix:"+f.dbSocket, "del-br", name, "--", "add-br", name, "--", "set", "Bridge", name, "datapath_type=system", "rstp_enable=true")
		if f.vs("get", "Bridge", name, "_uuid") == i.Object.OVSUUID {
			t.Fatal("native replacement did not replace UUID")
		}
		noEvidence(executor.ReadSpanningTreePreflight(ctx, i, control, executable, authorize))
	})
}
