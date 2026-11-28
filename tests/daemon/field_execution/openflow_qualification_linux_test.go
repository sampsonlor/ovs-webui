//go:build linux

package fieldexecution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

// TestNativeQualifiedOpenFlow uses the witness build provenance produced by
// tests/ovs_witness/build.sh and the direct local management socket of its
// disposable switch. CLI configuration is test setup, outside the provider.
func TestNativeQualifiedOpenFlow(t *testing.T) {
	if os.Getenv("OVS_OPENFLOW_NATIVE_TEST") != "1" {
		t.Skip("requires explicitly enabled isolated OpenFlow witness fixture")
	}
	selfNS, err := os.Stat("/proc/self/ns/net")
	must(t, err)
	hostNS, err := os.Stat("/proc/1/ns/net")
	must(t, err)
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" || os.Geteuid() != 0 || os.SameFile(selfNS, hostNS) {
		t.Fatal("root and a separate Linux network namespace are required")
	}

	buildDir := os.Getenv("OVS_OPENFLOW_WITNESS_BUILD")
	f, build := newNativeOpenFlowFixture(t, buildDir)
	ovsVsctl := filepath.Join(buildDir, "utilities", "ovs-vsctl")
	ovsOfctl := filepath.Join(buildDir, "utilities", "ovs-ofctl")
	const name = "br-of-qualified"
	const cookie uint64 = 0xf123456789abcdef
	const dpID uint64 = 0x0277685511223344
	db := "--db=unix:" + f.dbSocket
	f.run(ovsVsctl, "--timeout=5", db, "add-br", name, "--", "set", "Bridge", name,
		"datapath_type=system", "protocols=OpenFlow13", fmt.Sprintf("other_config:datapath-id=%016x", dpID))
	socket := filepath.Join(f.root, name+".mgmt")
	waitFor(t, func() bool {
		info, err := os.Lstat(socket)
		return err == nil && info.Mode()&os.ModeSocket != 0
	})
	ofctl := func(action string, args ...string) string {
		t.Helper()
		return f.run(append([]string{ovsOfctl, "-O", "OpenFlow13", action, "unix:" + socket}, args...)...)
	}
	ofctl("add-flow", "table=7,priority=405,cookie=0xf123456789abcdef,actions=drop")
	ofctl("add-flow", "table=8,priority=406,cookie=0xf123456789abcdef,actions=drop")
	ofctl("add-flow", "table=9,priority=407,ip,actions=drop")
	for i := 0; i < 12; i++ {
		ofctl("add-flow", fmt.Sprintf("table=7,priority=%d,cookie=0x%x,actions=drop", 500+i, 100+i))
	}
	ofctl("add-group", "group_id=17,type=all,bucket=output:LOCAL")
	features := ofctl("meter-features")
	fields := regexp.MustCompile(`(?m)\bmax_meter:\s*([0-9]+)\s+max_bands:\s*([0-9]+)`).FindStringSubmatch(features)
	if len(fields) != 3 {
		t.Fatal("native switch did not return a recognizable meter-feature witness")
	}
	maxMeter, err := strconv.ParseUint(fields[1], 10, 32)
	must(t, err)
	maxBands, err := strconv.ParseUint(fields[2], 10, 8)
	must(t, err)
	meterSupported := maxMeter > 0 && maxBands > 0 &&
		openFlowMeterFeature(features, "band_types", "drop") && openFlowMeterFeature(features, "capabilities", "kbps")
	if meterSupported {
		ofctl("add-meter", "meter=31,kbps,band=type=drop,rate=1024")
	}

	ctx := t.Context()
	options := sqlite.Options{Path: filepath.Join(f.root, "manager.db"), Kind: repository.Manager, SoftwareVersion: "native-openflow-qualified"}
	must(t, sqlite.Initialize(ctx, options))
	store, err := sqlite.Open(ctx, options)
	must(t, err)
	defer store.Close()
	identities, err := registry.New(store)
	must(t, err)
	inv := inventory.New(identities)
	provider, err := ovsdb.New(ovsdb.Options{Socket: f.dbSocket, DatabaseFile: f.conf, PeerUID: 0})
	must(t, err)
	monitorCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); provider.Run(monitorCtx, inv) }()
	defer func() { stop(); <-done }()
	nativeUUID := f.run(ovsVsctl, "--timeout=5", "--no-wait", db, "get", "Bridge", name, "_uuid")
	var bridgeID string
	waitFor(t, func() bool {
		if err := store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id FROM identities WHERE table_name='Bridge' AND state='active' AND ovs_uuid=?", nativeUUID).Scan(&bridgeID)
		}); err != nil {
			return false
		}
		got, err := inv.OpenFlowSource(ctx, bridgeID)
		return err == nil && got.Bridge.UUID == nativeUUID && got.DatapathID == dpID
	})
	qualified := ovsdb.QualifiedOpenFlowOptions{
		BridgeSockets: map[string]string{bridgeID: socket},
		ControlSocket: filepath.Join(f.root, "switch.ctl"), PeerUID: 0, Build: build,
	}
	observer, err := provider.QualifiedOpenFlowObserver(inv, qualified)
	must(t, err)
	table := uint8(7)
	selectedCookie := cookie
	query := ovsdb.OpenFlowQuery{BridgeID: bridgeID, Table: &table, Cookie: &selectedCookie, Limit: 5, IncludeGroupsMeters: true}

	t.Run("actual_filtered_flows_groups_and_meter_config", func(t *testing.T) {
		got, err := observer.ObserveFlows(ctx, query)
		must(t, err)
		if got.BridgeID != bridgeID || !apitypes.ManagementID(got.InstanceGeneration) || got.ObservedAt.IsZero() ||
			got.Ownership != "unknown" || got.Truncated || got.Source != "openflow-1.3-unix" ||
			got.Query.Table == nil || *got.Query.Table != table || got.Query.Cookie != "0xf123456789abcdef" ||
			!got.Query.IncludeGroupsMeters || len(got.Rows) != 1 || got.GroupMeterStatistics != "unknown" {
			t.Fatalf("observed query, identity or coverage changed: %+v", got)
		}
		row := got.Rows[0]
		if row.Table != table || row.Priority != 405 || row.Cookie != "0xf123456789abcdef" ||
			row.Packets == nil || *row.Packets != "0" || row.Bytes == nil || *row.Bytes != "0" {
			t.Fatalf("native Flow from the wrong table or inaccurate numeric record: %+v", row)
		}
		if got.Groups != "observed" || len(got.GroupRows) != 1 || got.GroupRows[0].ID != 17 || len(got.GroupRows[0].Buckets) != 1 {
			t.Fatalf("configured group was not obtained from the same proven socket: %+v", got)
		}
		if meterSupported {
			if got.Meters != "observed" || len(got.MeterRows) != 1 || got.MeterRows[0].ID != 31 ||
				len(got.MeterRows[0].Bands) != 1 || got.MeterRows[0].Bands[0].Rate != 1024 {
				t.Fatalf("native supported meter lost: %+v", got)
			}
			t.Log("native meter config verified")
		} else {
			if len(got.MeterRows) != 0 || (got.Meters != "observed" && got.MeterReason != "OPENFLOW_METER_CONFIGURATION_UNSUPPORTED") {
				t.Fatalf("missing native meter support misrepresented: %+v", got)
			}
			t.Log("native datapath did not advertise the requested drop/kbps meter capacity")
		}
	})

	t.Run("actual_requested_raw_OXM_bytes", func(t *testing.T) {
		table := uint8(9)
		got, err := observer.ObserveFlows(ctx, ovsdb.OpenFlowQuery{BridgeID: bridgeID, Table: &table, Limit: 5, RawFields: true})
		must(t, err)
		if !got.Query.RawFields || got.FieldCoverage != "bounded-raw-fields" || len(got.Rows) != 1 || got.Rows[0].Table != 9 {
			t.Fatalf("qualified raw capture did not preserve current scope: %+v", got)
		}
		if !slices.ContainsFunc(got.Rows[0].Match, func(field ovsdb.OpenFlowMatchField) bool {
			return field.Class == 0x8000 && field.Field == 5 && field.RawPayload == "0800"
		}) {
			t.Fatalf("native IPv4 EtherType bytes were not preserved: %+v", got.Rows[0].Match)
		}
	})

	t.Run("capture_cap_stops_auxiliary_reads", func(t *testing.T) {
		bounded := query
		bounded.Cookie, bounded.Limit = nil, 2
		got, err := observer.ObserveFlows(ctx, bounded)
		must(t, err)
		body, err := json.Marshal(got)
		must(t, err)
		if !got.Truncated || len(got.Rows) != 2 || len(body) > ovsdb.MaxOpenFlowBytes ||
			got.GroupReason != "OPENFLOW_CAPTURE_LIMIT" || got.MeterReason != "OPENFLOW_CAPTURE_LIMIT" {
			t.Fatalf("actual native capture escaped client limits: %+v", got)
		}
	})

	t.Run("reviewed_wrong_socket_and_build_ring_are_denied", func(t *testing.T) {
		bad := qualified
		bad.BridgeSockets = map[string]string{bridgeID: filepath.Join(f.root, "wrong.mgmt")}
		wrong, err := provider.QualifiedOpenFlowObserver(inv, bad)
		must(t, err)
		requireNativeOpenFlowDenied(t, wrong, query, "OPENFLOW_IDENTITY_UNPROVEN")
		bad = qualified
		bad.Build.ManifestDigest = strings.Repeat("0", 64)
		wrong, err = provider.QualifiedOpenFlowObserver(inv, bad)
		must(t, err)
		requireNativeOpenFlowDenied(t, wrong, query, "OPENFLOW_IDENTITY_UNPROVEN")
		// Matching the witnessed basename and the socket's filesystem record
		// still cannot authorize a listener from another local process.
		dir := filepath.Join(f.root, "other-ring")
		must(t, os.Mkdir(dir, 0700))
		impostor := filepath.Join(dir, name+".mgmt")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: impostor, Net: "unix"})
		must(t, err)
		defer listener.Close()
		must(t, listener.SetDeadline(time.Now().Add(4*time.Second)))
		reads := make(chan error, 1)
		go func() {
			pipe, err := listener.AcceptUnix()
			if err != nil {
				reads <- err
				return
			}
			defer pipe.Close()
			_ = pipe.SetReadDeadline(time.Now().Add(time.Second))
			var b [1]byte
			n, err := pipe.Read(b[:])
			if n != 0 {
				err = errors.New("provider wrote bytes to an unqualified peer")
			}
			reads <- err
		}()
		bad = qualified
		bad.BridgeSockets = map[string]string{bridgeID: impostor}
		wrong, err = provider.QualifiedOpenFlowObserver(inv, bad)
		must(t, err)
		requireNativeOpenFlowDenied(t, wrong, query, "OPENFLOW_IDENTITY_UNPROVEN")
		if err := <-reads; !errors.Is(err, io.EOF) {
			t.Fatalf("wrong peer was not closed before protocol writes: %v", err)
		}
	})

	t.Run("controller_reference_is_outside_this_witness", func(t *testing.T) {
		controller := filepath.Join(f.root, "fixture-controller.mgmt")
		target := "punix:" + controller
		f.run(ovsVsctl, "--timeout=5", db, "set-controller", name, target)
		defer f.run(ovsVsctl, "--timeout=5", db, "del-controller", name)
		if got := f.run(ovsVsctl, "--timeout=5", "--no-wait", db, "get-controller", name); got != target {
			t.Fatal("isolated controller target was not configured")
		}
		outside := qualified
		outside.BridgeSockets = map[string]string{bridgeID: controller}
		controllerObserver, err := provider.QualifiedOpenFlowObserver(inv, outside)
		must(t, err)
		requireNativeOpenFlowDenied(t, controllerObserver, query, "OPENFLOW_IDENTITY_UNPROVEN")

		got, err := observer.ObserveFlows(ctx, query)
		must(t, err)
		if got.BridgeID != bridgeID || got.Source != "openflow-1.3-unix" || got.Ownership != "unknown" {
			t.Fatalf("the witnessed local observer misstated scope or controller ownership: %+v", got)
		}
	})

	t.Run("expired_inventory_does_not_reuse_a_live_management_socket", func(t *testing.T) {
		source, err := inv.OpenFlowSource(ctx, bridgeID)
		must(t, err)
		stop()
		<-done
		waitFor(t, func() bool { return time.Since(source.Evidence.ObservedAt) > inventory.FreshFor })
		requireNativeOpenFlowDenied(t, observer, query, "OPENFLOW_SOURCE_UNAVAILABLE")
	})
}

func openFlowMeterFeature(text, key, value string) bool {
	for _, line := range strings.Split(text, "\n") {
		if values, ok := strings.CutPrefix(strings.TrimSpace(line), key+":"); ok {
			return slices.Contains(strings.Fields(values), value)
		}
	}
	return false
}

func requireNativeOpenFlowDenied(t *testing.T, observer *ovsdb.OpenFlowObserver, query ovsdb.OpenFlowQuery, code string) {
	t.Helper()
	out, err := observer.ObserveFlows(t.Context(), query)
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Code != code || out.BridgeID != "" ||
		len(out.Rows) != 0 || len(out.GroupRows) != 0 || len(out.MeterRows) != 0 {
		t.Fatalf("want %s with no captured data, got %+v, %v", code, out, err)
	}
}

func newNativeOpenFlowFixture(t *testing.T, buildDir string) (*fixture, ovsdb.SpanningTreeWitnessBuild) {
	t.Helper()
	provenancePath, schemaPath := os.Getenv("OVS_OPENFLOW_BUILD_MANIFEST"), os.Getenv("OVS_EXECUTION_SCHEMA")
	for _, path := range []string{buildDir, provenancePath, schemaPath} {
		if !filepath.IsAbs(path) {
			t.Fatal("expected explicit absolute build, provenance and schema paths")
		}
	}
	var proof struct {
		Status     string            `json:"status"`
		Arch       string            `json:"architecture"`
		WitnessABI string            `json:"witness_abi"`
		Profile    string            `json:"build_profile_sha256"`
		Switch     map[string]string `json:"executables"`
		Database   map[string]string `json:"database_executables"`
	}
	data, err := os.ReadFile(provenancePath)
	must(t, err)
	must(t, json.Unmarshal(data, &proof))
	profile, known := ovswitness.LookupBuildProfile(proof.Profile)
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if !known || profile.Version != "3.3.9" ||
		proof.Status != "isolated-prototype-not-a-production-release" || proof.WitnessABI != "ovs-webui.stp-witness.v1" ||
		arch == "" || proof.Arch != arch {
		t.Fatal("expected approved 3.3.9 witness build for this architecture")
	}
	schemaData, err := os.ReadFile(schemaPath)
	must(t, err)
	// The source file pin covers the reviewed schema before ovsdb-tool
	// normalizes it. The build's database pin covers the live RPC result.
	const sourceSchemaSHA256 = "c18c754664bdb880c73d7ba49553fc898c43b1b8fbc80b6c0e53162b3cd36fba"
	sourceDigest := sha256.Sum256(schemaData)
	if hex.EncodeToString(sourceDigest[:]) != sourceSchemaSHA256 {
		t.Fatal("fixture source schema differs from the reviewed OVS 3.3.9 source")
	}

	root, err := os.MkdirTemp("/var/lib", "ovs-openflow-test-")
	must(t, err)
	f := &fixture{t: t, root: root, conf: filepath.Join(root, "conf.db"), dbSocket: filepath.Join(root, "db.sock")}
	t.Cleanup(func() {
		f.stop("switch")
		f.stop("db")
		_ = os.RemoveAll(root)
	})
	vswitchd, dbserver := filepath.Join(root, "ovs-vswitchd"), filepath.Join(root, "ovsdb-server")
	stageNativeOpenFlowExecutable(t, filepath.Join(buildDir, "vswitchd/ovs-vswitchd"), vswitchd, proof.Switch["enabled"])
	stageNativeOpenFlowExecutable(t, filepath.Join(buildDir, "ovsdb/ovsdb-server"), dbserver, proof.Database["enabled"])
	pin, err := json.Marshal(map[string]string{
		"format": "ovs-webui.stp-build.v1", "profile_sha256": profile.Digest, "architecture": runtime.GOARCH,
		"variant": "enabled", "executable_sha256": proof.Switch["enabled"],
	})
	must(t, err)
	manifest := filepath.Join(root, "preflight-build.json")
	must(t, os.WriteFile(manifest, pin, 0600))
	digest := sha256.Sum256(pin)
	f.run(filepath.Join(buildDir, "ovsdb", "ovsdb-tool"), "create", f.conf, schemaPath)
	f.run(dbserver, f.conf, "--remote=punix:"+f.dbSocket, "--pidfile="+filepath.Join(root, "db.pid"),
		"--unixctl="+filepath.Join(root, "db.ctl"), "--detach", "--no-chdir")
	liveSchema := f.run(filepath.Join(buildDir, "ovsdb", "ovsdb-client"), "--timeout=5", "get-schema", "unix:"+f.dbSocket, "Open_vSwitch")
	var schema map[string]any
	decoder := json.NewDecoder(strings.NewReader(liveSchema))
	decoder.UseNumber()
	must(t, decoder.Decode(&schema))
	if schema["name"] != "Open_vSwitch" || inventory.Digest(schema) != profile.Schema {
		t.Fatal("live database schema differs from the reviewed build profile")
	}
	f.run(filepath.Join(buildDir, "utilities", "ovs-vsctl"), "--timeout=5", "--no-wait", "--db=unix:"+f.dbSocket, "init")
	f.run(vswitchd, "--pidfile="+filepath.Join(root, "switch.pid"), "--unixctl="+filepath.Join(root, "switch.ctl"),
		"--detach", "--no-chdir", "unix:"+f.dbSocket)
	return f, ovsdb.SpanningTreeWitnessBuild{Executable: vswitchd, Manifest: manifest, ManifestDigest: hex.EncodeToString(digest[:])}
}

func stageNativeOpenFlowExecutable(t *testing.T, src, dst, expected string) {
	t.Helper()
	file, err := os.Open(src)
	must(t, err)
	defer file.Close()
	info, err := file.Stat()
	must(t, err)
	const limit = 128 << 20
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		t.Fatal("reviewed build artifact outside staging limits")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	must(t, err)
	h := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(file, limit+1))
	closeErr := out.Close()
	must(t, copyErr)
	must(t, closeErr)
	if size != info.Size() || hex.EncodeToString(h.Sum(nil)) != expected {
		t.Fatal("enabled native executable does not match reviewed build provenance")
	}
}
