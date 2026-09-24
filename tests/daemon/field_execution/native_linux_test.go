//go:build linux

// Native execution tests use private dummy switches under /run. This package is
// compiled only as a test binary; no fault injector or safety override is shipped.
package fieldexecution

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/web"
)

var key = bytes.Repeat([]byte{9}, 32)

type faultProxy struct {
	listener    net.Listener
	target      string
	sent        atomic.Int32
	dropReply   atomic.Bool
	dropRequest atomic.Bool
	mu          sync.Mutex
	before      func()
	connections map[net.Conn]bool
	wg          sync.WaitGroup
}

func newProxy(t *testing.T, path, target string) *faultProxy {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	p := &faultProxy{listener: l, target: target, connections: map[net.Conn]bool{}}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.connections[c] = true
			p.mu.Unlock()
			p.wg.Add(1)
			go p.serve(c)
		}
	}()
	return p
}
func (p *faultProxy) serve(c net.Conn) {
	defer p.wg.Done()
	defer c.Close()
	defer func() { p.mu.Lock(); delete(p.connections, c); p.mu.Unlock() }()
	u, err := net.Dial("unix", p.target)
	if err != nil {
		return
	}
	defer u.Close()
	var transaction atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer u.Close()
		defer c.Close()
		d := json.NewDecoder(bufio.NewReader(c))
		for {
			var b json.RawMessage
			if d.Decode(&b) != nil {
				return
			}
			var r struct {
				Method string
				Params []json.RawMessage
			}
			_ = json.Unmarshal(b, &r)
			mutation := false
			if r.Method == "transact" && len(r.Params) > 1 {
				for _, raw := range r.Params[1:] {
					var op struct{ Op string }
					_ = json.Unmarshal(raw, &op)
					mutation = mutation || op.Op == "update" || op.Op == "mutate" || op.Op == "insert" || op.Op == "delete"
				}
			}
			if mutation {
				transaction.Store(true)
				p.sent.Add(1)
				p.mu.Lock()
				hook := p.before
				p.before = nil
				p.mu.Unlock()
				if hook != nil {
					hook()
				}
				if p.dropRequest.CompareAndSwap(true, false) {
					return
				}
			}
			if _, err := u.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}()
	d := json.NewDecoder(bufio.NewReader(u))
	for {
		var b json.RawMessage
		if d.Decode(&b) != nil {
			break
		}
		var r struct{ ID json.RawMessage }
		_ = json.Unmarshal(b, &r)
		if transaction.Load() && string(r.ID) == "2" && p.dropReply.CompareAndSwap(true, false) {
			break
		}
		if _, err := c.Write(append(b, '\n')); err != nil {
			break
		}
	}
	_ = c.Close()
	_ = u.Close()
	<-done
}
func (p *faultProxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	for c := range p.connections {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}
func (p *faultProxy) hook(f func()) { p.mu.Lock(); p.before = f; p.mu.Unlock() }

type fixture struct {
	t                                 *testing.T
	root, conf, dbSocket, proxySocket string
	proxy                             *faultProxy
	file                              *os.File
	store, webStore                   *sqlite.Store
	options                           sqlite.Options
	auth                              *auth.Repository
	login                             authn.LoginResult
	workspace                         *web.Workspace
	provider                          *ovsdb.Provider
	inventory                         *inventory.Service
	executor                          *ovsdb.Executor
	engine                            *executions.Engine
	ctx                               context.Context
	cancel                            context.CancelFunc
	monitorDone                       chan struct{}
}

func (f *fixture) run(args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "OVS_RUNDIR="+f.root, "OVS_DBDIR="+f.root, "OVS_LOGDIR="+f.root)
	b, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("%s: %v: %s", args[0], err, b)
	}
	return strings.TrimSpace(string(b))
}
func (f *fixture) vs(args ...string) string {
	return f.run(append([]string{"ovs-vsctl", "--timeout=5", "--no-wait", "--db=unix:" + f.dbSocket}, args...)...)
}
func (f *fixture) pid(name string) int {
	b, err := os.ReadFile(filepath.Join(f.root, name+".pid"))
	if err != nil {
		f.t.Fatal(err)
	}
	id, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *fixture) startDB() {
	f.run("ovsdb-server", f.conf, "--remote=punix:"+f.dbSocket, "--pidfile="+filepath.Join(f.root, "db.pid"), "--unixctl="+filepath.Join(f.root, "db.ctl"), "--detach", "--no-chdir", "--overwrite-pidfile")
}
func (f *fixture) stop(name string) {
	b, err := os.ReadFile(filepath.Join(f.root, name+".pid"))
	if err != nil {
		return
	}
	id, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if id > 0 {
		_ = syscall.Kill(id, syscall.SIGCONT)
		_ = syscall.Kill(id, syscall.SIGTERM)
	}
	waitFor(f.t, func() bool {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", id))
		return os.IsNotExist(err) || strings.Contains(string(data), ") Z ")
	})
	_ = os.Remove(filepath.Join(f.root, name+".pid"))
}
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("native condition timed out")
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := os.MkdirTemp("/run", "ovs-field-test-")
	must(t, err)
	f := &fixture{t: t, root: root, conf: filepath.Join(root, "conf.db"), dbSocket: filepath.Join(root, "db.sock"), proxySocket: filepath.Join(root, "fault.sock")}
	t.Cleanup(func() {
		if f.cancel != nil {
			f.cancel()
			<-f.monitorDone
		}
		if f.store != nil {
			_ = f.store.Close()
		}
		if f.webStore != nil {
			_ = f.webStore.Close()
		}
		if f.proxy != nil {
			f.proxy.close()
		}
		if f.file != nil {
			_ = f.file.Close()
		}
		f.stop("switch")
		f.stop("db")
		_ = os.RemoveAll(root)
	})
	f.run("ovsdb-tool", "create", f.conf, os.Getenv("OVS_EXECUTION_SCHEMA"))
	f.startDB()
	f.vs("init")
	f.run("ovs-vswitchd", "--enable-dummy", "--pidfile="+filepath.Join(root, "switch.pid"), "--unixctl="+filepath.Join(root, "switch.ctl"), "--detach", "--no-chdir", "unix:"+f.dbSocket)
	f.vs("add-br", "br-field", "--", "set", "Bridge", "br-field", "datapath_type=dummy", "--", "add-port", "br-field", "field-p1", "--", "set", "Interface", "field-p1", "type=dummy", "--", "set", "Port", "field-p1", "vlan_mode=access", "tag=10", "--", "add-port", "br-field", "field-p2", "--", "set", "Interface", "field-p2", "type=dummy", "--", "set", "Port", "field-p2", "vlan_mode=access", "tag=10")
	// The trusted test proxy holds the actual DB file. Production still verifies
	// SO_PEERCRED, process-start and file binding without any test bypass.
	f.file, err = os.Open(f.conf)
	must(t, err)
	f.proxy = newProxy(t, f.proxySocket, f.dbSocket)
	managerDir, webDir := filepath.Join(root, "manager"), filepath.Join(root, "web")
	must(t, os.Mkdir(managerDir, 0700))
	must(t, os.Mkdir(webDir, 0700))
	f.options = sqlite.Options{Path: filepath.Join(managerDir, "manager.db"), Kind: repository.Manager, SoftwareVersion: "native-execution-test"}
	must(t, sqlite.Initialize(context.Background(), f.options))
	f.store, err = sqlite.Open(context.Background(), f.options)
	must(t, err)
	f.auth, err = auth.New(f.store, key)
	must(t, err)
	must(t, f.auth.Bootstrap(context.Background(), "admin", "synthetic-native-field-password"))
	f.login, err = f.auth.Authenticate(context.Background(), authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	wo := sqlite.Options{Path: filepath.Join(webDir, "web.db"), Kind: repository.Web, SoftwareVersion: "native-execution-test"}
	must(t, sqlite.Initialize(context.Background(), wo))
	f.webStore, err = sqlite.Open(context.Background(), wo)
	must(t, err)
	f.workspace, err = web.NewWorkspace(f.webStore, f.auth)
	must(t, err)
	f.startMonitor()
	return f
}
func (f *fixture) startMonitor() {
	r, err := registry.New(f.store)
	must(f.t, err)
	f.inventory = inventory.New(r)
	f.auth.WithInventory(f.inventory)
	f.provider, err = ovsdb.New(ovsdb.Options{Socket: f.proxySocket, DatabaseFile: f.conf, PeerUID: uint32(os.Geteuid())})
	must(f.t, err)
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.monitorDone = make(chan struct{})
	go func() { defer close(f.monitorDone); f.provider.Run(f.ctx, f.inventory) }()
	waitFor(f.t, func() bool { _, err := f.inventory.CandidateSnapshot(f.ctx, nil); return err == nil })
	var ids []string
	for _, name := range []string{"field-p1", "field-p2"} {
		ids = append(ids, f.binding(name).ManagementID)
	}
	must(f.t, f.inventory.SetLocalVLANPorts(ids))
	f.executor = f.provider.Executor(f.inventory)
	f.engine, err = f.auth.ConfigureExecution(f.executor)
	must(f.t, err)
}
func (f *fixture) binding(name string) candidate.Binding {
	f.t.Helper()
	var b candidate.Binding
	ovsUUID := f.vs("get", "Port", name, "_uuid")
	must(f.t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Port' AND state='active' AND ovs_uuid=?", ovsUUID).Scan(&b.ManagementID, &b.OVSUUID, &b.Table, &b.Generation)
	}))
	return b
}
func (f *fixture) envelope() candidate.Envelope {
	f.t.Helper()
	var e candidate.Envelope
	must(f.t, f.webStore.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		var b []byte
		if err := q.QueryRowContext(ctx, "SELECT envelope FROM candidate_workspaces WHERE owner_id=?", f.login.Claims.PrincipalID).Scan(&b); err != nil {
			return err
		}
		return json.Unmarshal(b, &e)
	}))
	return e
}
func (f *fixture) prepare(names []string, tag int) execution.Request {
	intents := []candidate.Intent{}
	mode := "access"
	for _, name := range names {
		intents = append(intents, candidate.Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: f.binding(name), Value: candidate.VLAN{Mode: &mode, Tag: &tag, Trunks: []int{}, CVLANs: []int{}}})
	}
	return f.prepareIntents(intents)
}
func (f *fixture) prepareIntents(intents any) execution.Request {
	f.t.Helper()
	contract, err := apicontract.New()
	must(f.t, err)
	s := publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}
	op, path, _ := contract.Match("GET", "/api/v1/candidate")
	_, err = f.workspace.Read(f.ctx, s, publicapi.Query{Operation: op, Path: path, Values: url.Values{}})
	must(f.t, err)
	e := f.envelope()
	id := apitypes.RequestID(time.Now())
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": intents})
	op, path, _ = contract.Match("PATCH", "/api/v1/candidate")
	_, err = f.workspace.Execute(f.ctx, s, publicapi.Query{Operation: op, Path: path, Values: url.Values{}}, requests.Command{Principal: s.ID, Epoch: e.Epoch, Domain: "workspace", ID: id, Operation: "changeCandidate", Method: "PATCH", URI: "/api/v1/candidate", Precondition: `"` + e.Candidate.Revision + `"`, Payload: body})
	must(f.t, err)
	e = f.envelope()
	id = apitypes.RequestID(time.Now())
	body, _ = json.Marshal(map[string]any{"request_id": id, "candidate_id": e.Candidate.ID, "candidate_revision": e.Candidate.Revision})
	op, path, _ = contract.Match("POST", "/api/v1/validations")
	ack, err := f.workspace.Execute(f.ctx, s, publicapi.Query{Operation: op, Path: path, Values: url.Values{}}, requests.Command{Principal: s.ID, Epoch: f.login.Claims.RequestEpoch, Domain: "management", ID: id, Operation: "createValidation", Method: "POST", URI: "/api/v1/validations", Payload: body})
	must(f.t, err)
	return execution.Request{ID: apitypes.RequestID(time.Now()), ValidationID: ack.Receipt.Resource.ID, Envelope: e}
}

type isolatedSafety struct{ f *fixture }

func (s isolatedSafety) Check(ctx context.Context, in execution.Request) error {
	if !strings.HasPrefix(s.f.root, "/run/ovs-field-test-") || os.Geteuid() != 0 {
		return fmt.Errorf("not an isolated native fixture")
	}
	view, err := s.f.inventory.ExecutionView(ctx, candidate.Bindings(in.Envelope.Candidate))
	if err != nil {
		return err
	}
	for _, row := range view.Observation.Rows["Interface"] {
		if row.Values["type"] != "dummy" && row.Values["type"] != "internal" {
			return fmt.Errorf("non-test interface")
		}
	}
	return nil
}
func (f *fixture) submit(in execution.Request) execution.Record {
	f.t.Helper()
	s := publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}
	lease, err := f.workspace.ReserveExecution(f.ctx, s, in, isolatedSafety{f})
	must(f.t, err)
	ack, err := f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), lease)
	must(f.t, err)
	r, err := f.engine.Read(f.ctx, ack.Receipt.Resource.ID)
	must(f.t, err)
	return r
}
func (f *fixture) observe(id string, check func(execution.Record) bool) execution.Record {
	f.t.Helper()
	var r execution.Record
	waitFor(f.t, func() bool {
		if f.engine.Reconcile(f.ctx, id) != nil {
			return false
		}
		r, _ = f.engine.Read(f.ctx, id)
		return check(r)
	})
	return r
}

func TestNativeFieldExecution(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit isolated native OVS fixture runs in the Linux CI stage after OVS installation")
	}
	if os.Geteuid() != 0 || os.Getenv("OVS_EXECUTION_SCHEMA") == "" {
		t.Fatal("root isolated fixture and explicit schema required")
	}
	t.Run("commit_applied_and_unrelated_field", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		f.proxy.hook(func() { f.vs("set", "Port", "field-p1", "other_config:synthetic=preserved") })
		r := f.submit(in)
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Applied == "applied" })
		if r.Outcome.Commit != "committed" || r.Outcome.Target == nil || r.Outcome.Observed == nil || f.vs("get", "Port", "field-p1", "other_config:synthetic") != "preserved" {
			t.Fatal(r)
		}
		before := f.proxy.sent.Load()
		ack, err := f.engine.Submit(f.ctx, in, f.auth.ExecutionAuthorizer(f.login.Grant), nil)
		must(t, err)
		if !ack.Replayed || f.proxy.sent.Load() != before {
			t.Fatal("replay resent")
		}
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != before {
			t.Fatal("recovery resent")
		}
	})
	t.Run("native_atomic_wait_aborts_all_fields", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1", "field-p2"}, 20)
		f.proxy.hook(func() { f.vs("set", "Port", "field-p2", "tag=77") })
		r := f.submit(in)
		if r.Outcome.Commit != "rejected" || f.vs("get", "Port", "field-p1", "tag") != "10" || f.vs("get", "Port", "field-p2", "tag") != "77" {
			t.Fatal(r)
		}
	})
	t.Run("independent_ports_do_not_share_global_CAS", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1", "field-p2"}, 20)
		plans := []execution.Plan{}
		for _, intent := range in.Envelope.Candidate.Intents {
			e := in.Envelope
			e.Candidate.Intents = []candidate.StoredIntent{intent}
			p, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat(string('a'+rune(len(plans))), 64), e)
			must(t, err)
			plans = append(plans, p)
		}
		out := make([]execution.Outcome, 2)
		var wg sync.WaitGroup
		for n := range plans {
			wg.Add(1)
			go func(n int) { defer wg.Done(); out[n] = f.executor.Commit(f.ctx, plans[n], func() error { return nil }) }(n)
		}
		wg.Wait()
		if out[0].Commit != "committed" || out[1].Commit != "committed" || *out[0].Target == *out[1].Target {
			t.Fatal(out)
		}
	})
	t.Run("lost_commit_reply_proves_commit_without_invented_applied", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		f.proxy.dropReply.Store(true)
		r := f.submit(in)
		if r.Outcome.Commit != "unknown" {
			t.Fatal(r)
		}
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.State != "recovery-required" || r.Outcome.Target != nil || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 1 {
			t.Fatal(r)
		}
		// A separate process reopens the real manager.db and performs recovery.
		// No live grant, automatic replay or in-memory marker is required.
		f.cancel()
		<-f.monitorDone
		f.cancel = nil
		must(t, f.store.Close())
		f.store = nil
		cmd := exec.Command(os.Args[0], "-test.run=^TestNativeExecutionRecoveryChild$", "-test.v")
		cmd.Env = append(os.Environ(), "OVS_EXECUTION_RECOVERY_ROOT="+f.root, "OVS_EXECUTION_RECOVERY_ID="+r.ID)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(b))
		}
		if !bytes.Contains(b, []byte("recovered-committed-applied-unknown")) || f.proxy.sent.Load() != 1 {
			t.Fatal(string(b), f.proxy.sent.Load())
		}
	})
	t.Run("SIGKILL_after_real_commit_before_durable_reply", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		input := nativeCrashInput{Root: f.root, Request: in, Grant: f.login.Grant, AllowIDs: []string{f.binding("field-p1").ManagementID, f.binding("field-p2").ManagementID}}
		b, _ := json.Marshal(input)
		path := filepath.Join(f.root, "synthetic-crash-input.json")
		must(t, os.WriteFile(path, b, 0600))
		f.cancel()
		<-f.monitorDone
		f.cancel = nil
		must(t, f.store.Close())
		f.store = nil
		must(t, f.webStore.Close())
		f.webStore = nil
		cmd := exec.Command(os.Args[0], "-test.run=^TestNativeExecutionCrashChild$", "-test.v")
		cmd.Env = append(os.Environ(), "OVS_NATIVE_CRASH_INPUT="+path)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatal("missed real commit crash", err, string(output))
		}
		if f.proxy.sent.Load() != 1 || f.vs("get", "Port", "field-p1", "tag") != "20" {
			t.Fatal("crash did not follow native commit")
		}
		store, err := sqlite.Open(context.Background(), f.options)
		must(t, err)
		var id, state string
		must(t, store.Read(context.Background(), func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT id,state FROM field_executions").Scan(&id, &state)
		}))
		must(t, store.Close())
		if state != "committing" {
			t.Fatal("reply was saved before crash", state)
		}
		cmd = exec.Command(os.Args[0], "-test.run=^TestNativeExecutionRecoveryChild$", "-test.v")
		cmd.Env = append(os.Environ(), "OVS_EXECUTION_RECOVERY_ROOT="+f.root, "OVS_EXECUTION_RECOVERY_ID="+id)
		output, err = cmd.CombinedOutput()
		if err != nil || !bytes.Contains(output, []byte("recovered-committed-applied-unknown")) || f.proxy.sent.Load() != 1 {
			t.Fatal(err, string(output), f.proxy.sent.Load())
		}
	})
	t.Run("dropped_request_and_equal_after_image_are_ambiguous", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		f.proxy.dropRequest.Store(true)
		r := f.submit(in)
		f.vs("set", "Port", "field-p1", "tag=20")
		time.Sleep(350 * time.Millisecond)
		must(t, f.engine.Reconcile(f.ctx, r.ID))
		r, err := f.engine.Read(f.ctx, r.ID)
		must(t, err)
		if r.Outcome.Commit != "unknown" || r.State != "recovery-required" || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 1 {
			t.Fatal(r)
		}
	})
	t.Run("ovs_vswitchd_pause_separates_commit_and_applied", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGSTOP))
		r := f.submit(in)
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Commit == "committed" && r.Outcome.Current != nil })
		if r.Outcome.Applied == "applied" || r.Outcome.Target == nil {
			t.Fatal(r)
		}
		must(t, syscall.Kill(f.pid("switch"), syscall.SIGCONT))
		r = f.observe(r.ID, func(r execution.Record) bool { return r.Outcome.Applied == "applied" })
		if r.Outcome.Commit != "committed" {
			t.Fatal(r)
		}
	})
	t.Run("same_UUID_copy_and_database_restart_refuse_old_plan", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		p, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("c", 64), in.Envelope)
		must(t, err)
		f.stop("db")
		data, err := os.ReadFile(f.conf)
		must(t, err)
		copyPath := f.conf + ".copy"
		must(t, os.WriteFile(copyPath, data, 0600))
		must(t, os.Rename(copyPath, f.conf))
		f.startDB()
		out := f.executor.Commit(f.ctx, p, func() error { t.Fatal("identity discontinuity reached dispatch"); return nil })
		if out.Commit != "rejected" || f.proxy.sent.Load() != 0 {
			t.Fatal(out)
		}
		waitFor(t, func() bool { return f.inventory.PendingDigest() != "" })
		if f.vs("get", "Port", "field-p1", "tag") != "10" {
			t.Fatal("old plan written to replacement")
		}
	})
}

type nativeCrashInput struct {
	Safe        bool
	Root, Grant string
	Request     execution.Request
	AllowIDs    []string
}
type crashAfterCommit struct{ *ovsdb.Executor }

func (p crashAfterCommit) Commit(ctx context.Context, plan execution.Plan, before func() error) execution.Outcome {
	o := p.Executor.Commit(ctx, plan, before)
	if o.Commit == "committed" {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	}
	return o
}
func TestNativeExecutionCrashChild(t *testing.T) {
	path := os.Getenv("OVS_NATIVE_CRASH_INPUT")
	if path == "" {
		t.Skip("separate process real commit SIGKILL helper")
	}
	b, err := os.ReadFile(path)
	must(t, err)
	var in nativeCrashInput
	must(t, json.Unmarshal(b, &in))
	if !strings.HasPrefix(in.Root, "/run/ovs-field-test-") {
		t.Fatal("invalid isolated fixture")
	}
	f := &fixture{t: t, root: in.Root, conf: filepath.Join(in.Root, "conf.db"), proxySocket: filepath.Join(in.Root, "fault.sock")}
	o := sqlite.Options{Path: filepath.Join(in.Root, "manager", "manager.db"), Kind: repository.Manager, SoftwareVersion: "native-execution-test"}
	f.store, err = sqlite.Open(context.Background(), o)
	must(t, err)
	defer f.store.Close()
	f.auth, err = auth.New(f.store, key)
	must(t, err)
	r, err := registry.New(f.store)
	must(t, err)
	f.inventory = inventory.New(r)
	must(t, f.inventory.SetLocalVLANPorts(in.AllowIDs))
	f.auth.WithInventory(f.inventory)
	p, err := ovsdb.New(ovsdb.Options{Socket: f.proxySocket, DatabaseFile: f.conf, PeerUID: 0})
	must(t, err)
	f.ctx, f.cancel = context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(f.ctx, f.inventory) }()
	defer func() { f.cancel(); <-done }()
	waitFor(t, func() bool { _, err := f.inventory.CandidateSnapshot(f.ctx, nil); return err == nil })
	wo := sqlite.Options{Path: filepath.Join(in.Root, "web", "web.db"), Kind: repository.Web, SoftwareVersion: "native-execution-test"}
	f.webStore, err = sqlite.Open(f.ctx, wo)
	must(t, err)
	defer f.webStore.Close()
	w, err := web.NewWorkspace(f.webStore, f.auth)
	must(t, err)
	subject := publicapi.Subject{ID: in.Request.Envelope.Owner, Credential: in.Grant}
	if in.Safe {
		f.workspace = w
		claims, err := f.auth.InspectAuth(f.ctx, in.Grant)
		must(t, err)
		f.login = authn.LoginResult{Grant: in.Grant, Claims: claims}
		f.engine, err = f.auth.ConfigureExecution(crashAfterCommit{p.Executor(f.inventory)})
		must(t, err)
		var offset atomic.Int64
		f.configureSafety(&offset)
		f.safeApply(in.Request)
		time.Sleep(10 * time.Second)
		t.Fatal("Safe Apply did not reach native crash boundary")
	}
	lease, err := w.ReserveExecution(f.ctx, subject, in.Request, isolatedSafety{f})
	must(t, err)
	e, err := f.auth.ConfigureExecution(crashAfterCommit{p.Executor(f.inventory)})
	must(t, err)
	_, err = e.Submit(f.ctx, in.Request, f.auth.ExecutionAuthorizer(in.Grant), lease)
	must(t, err)
	t.Fatal("native commit did not reach crash boundary")
}

func TestNativeExecutionRecoveryChild(t *testing.T) {
	root := os.Getenv("OVS_EXECUTION_RECOVERY_ROOT")
	if root == "" {
		t.Skip("separate process recovery child")
	}
	if !strings.HasPrefix(root, "/run/ovs-field-test-") {
		t.Fatal("invalid isolated root")
	}
	o := sqlite.Options{Path: filepath.Join(root, "manager", "manager.db"), Kind: repository.Manager, SoftwareVersion: "native-execution-test"}
	store, err := sqlite.Open(context.Background(), o)
	must(t, err)
	defer store.Close()
	r, err := registry.New(store)
	must(t, err)
	inv := inventory.New(r)
	// Management bindings persist; reapply the exact reviewed local allowlist.
	var ids []string
	must(t, store.Read(context.Background(), func(ctx context.Context, q *sql.Conn) error {
		rows, err := q.QueryContext(ctx, "SELECT management_id FROM identities WHERE table_name='Port' AND state='active'")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}))
	must(t, inv.SetLocalVLANPorts(ids))
	p, err := ovsdb.New(ovsdb.Options{Socket: filepath.Join(root, "fault.sock"), DatabaseFile: filepath.Join(root, "conf.db"), PeerUID: 0})
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx, inv) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { _, err := inv.CandidateSnapshot(ctx, nil); return err == nil })
	e, err := executions.New(store, key, p.Executor(inv))
	must(t, err)
	must(t, e.Recover(ctx))
	record, err := e.Read(ctx, os.Getenv("OVS_EXECUTION_RECOVERY_ID"))
	must(t, err)
	if record.Outcome.Commit != "committed" || record.Outcome.Applied != "unknown" || record.Outcome.Target != nil || record.State != "recovery-required" {
		t.Fatal(record)
	}
	t.Log("recovered-committed-applied-unknown")
}
