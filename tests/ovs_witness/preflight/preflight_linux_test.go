//go:build linux

// Package preflight qualifies the private composition against the disposable
// patched kernel fixture. This test binary is not shipped with either daemon.
package preflight

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeWitnessPreflight(t *testing.T) {
	root := os.Getenv("OVS_WITNESS_ROOT")
	if root == "" {
		t.Skip("requires disposable patched OVS 3.3.9 kernel fixture")
	}
	if os.Geteuid() != 0 || !filepath.IsAbs(root) {
		t.Fatal("isolated root fixture required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vs := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, filepath.Join(root, "bin/ovs-vsctl"), append([]string{"--timeout=5", "--db=unix:" + filepath.Join(root, "db.sock")}, args...)...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture configuration: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	dir := t.TempDir()
	must(t, os.Chmod(dir, 0700))
	options := sqlite.Options{Path: filepath.Join(dir, "manager.db"), Kind: repository.Manager, SoftwareVersion: "witness-preflight-test"}
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
	deadline := time.Now().Add(6 * time.Second)
	var binding candidate.Binding
	uuid := vs("get", "Bridge", "br-witness", "_uuid")
	for {
		err = store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Bridge' AND state='active' AND ovs_uuid=?", uuid).Scan(&binding.ManagementID, &binding.OVSUUID, &binding.Table, &binding.Generation)
		})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	grants := func() {
		must(t, inv.SetLocalSpanningTreeBridges([]string{binding.ManagementID}))
		must(t, inv.SetLocalSpanningTreeExecutionBridges([]string{binding.ManagementID}))
	}
	grants()
	a, err := auth.New(store, bytes.Repeat([]byte{17}, 32))
	must(t, err)
	must(t, a.Bootstrap(ctx, "admin", "synthetic-witness-password"))
	login, err := a.Authenticate(ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-witness-password"})
	must(t, err)
	caps := append(append([]string{}, login.Claims.Capabilities...), auth.SpanningTreeExecutionCapability)
	role := func(values []string) {
		body, e := json.Marshal(values)
		must(t, e)
		must(t, store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, login.Claims.PrincipalID)
			return e
		}))
	}
	role(caps)
	fresh, err := a.Authenticate(ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-witness-password"})
	must(t, err)
	authorize := a.SpanningTreeExecutionAuthorizer(fresh.Grant)
	control := ovsdb.SpanningTreeControl{Socket: filepath.Join(root, "vsd.ctl"), PeerUID: 0}
	initial, err := ovsdb.ReadSpanningTreeRuntime(ctx, control, "stp", "br-witness")
	must(t, err)
	control.ExpectedDaemon = initial.Daemon
	manifestPath := filepath.Join(root, "preflight-build.json")
	manifest, err := os.ReadFile(manifestPath)
	must(t, err)
	manifestHash := sha256.Sum256(manifest)
	build := ovsdb.SpanningTreeWitnessBuild{Executable: filepath.Join(root, "bin/ovs-vswitchd"), Manifest: manifestPath, ManifestDigest: hex.EncodeToString(manifestHash[:])}
	executor := p.Executor(inv)
	draft := func() candidate.StoredIntent {
		t.Helper()
		target := vs("get", "Open_vSwitch", ".", "next_cfg")
		end := time.Now().Add(6 * time.Second)
		var v inventory.ExecutionView
		for {
			v, err = inv.SpanningTreeExecutionView(ctx, binding)
			if err == nil {
				row := v.Observation.Rows["Open_vSwitch"][v.Observation.Evidence.Root]
				if row.Values["next_cfg"] == target && row.Values["cur_cfg"] == target {
					break
				}
			}
			if time.Now().After(end) {
				t.Fatal("monitor fixture barrier", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		tr := v.Candidate.SpanningTrees[binding.ManagementID]
		var after candidate.SpanningTreeConfig
		data, _ := json.Marshal(tr.Configuration)
		must(t, json.Unmarshal(data, &after))
		key := "stp-priority"
		if after.RSTP {
			key = "rstp-priority"
		}
		priority := "4096"
		after.Parameters[key] = &priority
		return candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.SpanningTreePrepare, Object: binding, Schema: v.Candidate.Schema, Dependency: tr.Dependency, SpanningTree: &candidate.SpanningTreeChange{Before: tr.Configuration, After: after}}
	}
	noEvidence := func(out ovsdb.SpanningTreePreflight, e error) {
		t.Helper()
		if e == nil || !reflect.DeepEqual(out, ovsdb.SpanningTreePreflight{}) {
			t.Fatal("unproven partial evidence", out, e)
		}
	}
	for _, protocol := range []string{"stp", "rstp"} {
		t.Run("two_"+protocol+"_witnesses_match_native_fences_and_preserve_configuration", func(t *testing.T) {
			vs("set", "Bridge", "br-witness", "stp_enable="+map[bool]string{true: "true", false: "false"}[protocol == "stp"], "rstp_enable="+map[bool]string{true: "true", false: "false"}[protocol == "rstp"])
			i := draft()
			original := vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config")
			out, e := executor.ReadSpanningTreeWitnessPreflight(ctx, i, control, build, authorize)
			must(t, e)
			if out.Witness == nil || out.Binding != binding || out.Transition.State != "compatible" || out.Sample.Runtime.Protocol != protocol || out.Witness.First.Witness.Nonce == out.Witness.Second.Witness.Nonce || vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config") != original {
				t.Fatal("composition or read-only check", out)
			}
			t.Logf("whole preflight age: %s", time.Since(out.Sample.ObservedAt))
		})
	}
	t.Run("old_credential_ceiling_remains_denied", func(t *testing.T) {
		noEvidence(executor.ReadSpanningTreeWitnessPreflight(ctx, draft(), control, build, a.SpanningTreeExecutionAuthorizer(login.Grant)))
	})
	for _, fault := range []string{"manifest-hash", "profile", "architecture", "variant", "executable-hash", "manifest-mode", "executable-mode", "wrong-path"} {
		t.Run("build_"+fault+"_withholds_all", func(t *testing.T) {
			i := draft()
			pin := build
			defer func() {
				must(t, os.WriteFile(manifestPath, manifest, 0600))
				must(t, os.Chmod(manifestPath, 0600))
				must(t, os.Chmod(build.Executable, 0755))
			}()
			var m map[string]any
			must(t, json.Unmarshal(manifest, &m))
			switch fault {
			case "manifest-hash":
				pin.ManifestDigest = strings.Repeat("0", 64)
			case "manifest-mode":
				must(t, os.Chmod(manifestPath, 0666))
			case "executable-mode":
				must(t, os.Chmod(build.Executable, 0777))
			case "wrong-path":
				pin.Executable = filepath.Join(root, "bin/ovsdb-server")
			default:
				key := map[string]string{"profile": "profile_sha256", "architecture": "architecture", "variant": "variant", "executable-hash": "executable_sha256"}[fault]
				m[key] = "unqualified"
				if fault == "executable-hash" || fault == "profile" {
					m[key] = strings.Repeat("0", 64)
				}
				data, _ := json.Marshal(m)
				must(t, os.WriteFile(manifestPath, data, 0600))
				h := sha256.Sum256(data)
				pin.ManifestDigest = hex.EncodeToString(h[:])
			}
			noEvidence(executor.ReadSpanningTreeWitnessPreflight(ctx, i, control, pin, authorize))
		})
	}
	for _, fault := range []string{"role", "prepare-root-aba", "execution-root-aba", "configuration-aba", "manifest-replaced", "cancel"} {
		t.Run("between_samples_"+fault+"_withholds_all", func(t *testing.T) {
			i := draft()
			calls := 0
			readCtx, stop := context.WithCancel(ctx)
			defer stop()
			defer grants()
			defer role(caps)
			defer func() { must(t, os.WriteFile(manifestPath, manifest, 0600)) }()
			check := func(ctx context.Context) error {
				calls++
				if calls == 9 { // after A, before the second request's source checks
					switch fault {
					case "role":
						role(login.Claims.Capabilities)
					case "prepare-root-aba":
						must(t, inv.SetLocalSpanningTreeBridges(nil))
						must(t, inv.SetLocalSpanningTreeBridges([]string{binding.ManagementID}))
					case "execution-root-aba":
						must(t, inv.SetLocalSpanningTreeExecutionBridges(nil))
						must(t, inv.SetLocalSpanningTreeExecutionBridges([]string{binding.ManagementID}))
					case "configuration-aba":
						vs("set", "Bridge", "br-witness", "other_config:witness-test=temporary")
						vs("remove", "Bridge", "br-witness", "other_config", "witness-test")
					case "manifest-replaced":
						replacement := manifestPath + ".replacement"
						must(t, os.WriteFile(replacement, manifest, 0600))
						must(t, os.Rename(replacement, manifestPath))
					case "cancel":
						stop()
					}
				}
				return authorize(ctx)
			}
			noEvidence(executor.ReadSpanningTreeWitnessPreflight(readCtx, i, control, build, check))
			if calls < 9 {
				t.Fatal("did not reach the between-samples boundary", calls)
			}
		})
	}
	t.Run("whole_preflight_deadline_never_refreshes_evidence", func(t *testing.T) {
		i := draft()
		c, stop := context.WithTimeout(ctx, time.Nanosecond)
		defer stop()
		noEvidence(executor.ReadSpanningTreeWitnessPreflight(c, i, control, build, authorize))
	})
}
