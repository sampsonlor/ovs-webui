//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

// These cases prove native OVS compensation. Real network reachability is
// separately exercised by safe_apply.py over a kernel OVS VLAN and veth pair.
type nativeSafetyProbe struct{ root string }

func (p nativeSafetyProbe) Domain() string { return p.root }
func (p nativeSafetyProbe) Check(context.Context) error {
	if !strings.HasPrefix(p.root, "/run/ovs-field-test-") {
		return apitypes.Fail(503, "NOT_AN_ISOLATED_FIXTURE")
	}
	return nil
}
func (f *fixture) configureSafety(offset *atomic.Int64) {
	must(f.t, f.auth.ConfigureSafety(executions.SafetyOptions{Probe: nativeSafetyProbe{f.root}, Clock: func() tlscontrol.Clock {
		c := tlscontrol.Now()
		d := time.Duration(offset.Load())
		c.NS += int64(d)
		c.Wall = c.Wall.Add(d)
		return c
	}}))
}
func (f *fixture) safeApply(in execution.Request) string {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"request_id": in.ID, "candidate_id": in.Envelope.Candidate.ID, "candidate_revision": in.Envelope.Candidate.Revision, "validation_id": in.ValidationID, "mode": "safe-apply", "reason": "isolated native compensation test"})
	contract, err := apicontract.New()
	must(f.t, err)
	op, path, _ := contract.Match("POST", "/api/v1/transactions")
	ack, err := f.workspace.Execute(f.ctx, publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}, publicapi.Query{Operation: op, Path: path, Values: url.Values{}}, requests.Command{Principal: f.login.Claims.PrincipalID, Epoch: f.login.Claims.RequestEpoch, Domain: "management", ID: in.ID, Operation: "createTransaction", Method: "POST", URI: "/api/v1/transactions", Payload: body})
	must(f.t, err)
	return ack.Receipt.Resource.ID
}
func (f *fixture) safeState(id string) safety.Record {
	f.t.Helper()
	var s safety.Record
	must(f.t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		var b []byte
		if err := q.QueryRowContext(ctx, "SELECT document FROM safe_applies WHERE id=?", id).Scan(&b); err != nil {
			return err
		}
		return json.Unmarshal(b, &s)
	}))
	return s
}
func (f *fixture) waitSafety(id, state string) safety.Record {
	f.t.Helper()
	var s safety.Record
	waitFor(f.t, func() bool {
		must(f.t, f.engine.Recover(f.ctx))
		must(f.t, f.engine.SafetyTick(f.ctx))
		s = f.safeState(id)
		return s.State == state
	})
	return s
}
func (f *fixture) decide(id, decision string) {
	f.t.Helper()
	r, err := f.engine.Read(f.ctx, id)
	must(f.t, err)
	req := apitypes.RequestID(time.Now())
	b, _ := json.Marshal(map[string]any{"request_id": req, "expected_sequence": r.Sequence, "decision": decision})
	_, err = f.auth.ExecuteAuth(f.ctx, f.login.Grant, authn.Command{Method: "POST", URI: "/api/v1/transactions/" + id + "/decisions", Epoch: f.login.Claims.RequestEpoch, RequestID: req, Payload: b})
	must(f.t, err)
}
func TestNativeSafeApply(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit native Safe Apply matrix")
	}
	t.Run("confirmation_rechecks_OVS_when_monitor_is_behind", func(t *testing.T) {
		f := newFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1"}, 20))
		f.waitSafety(id, "awaiting-confirmation")
		// Keep the old cache intact inside its freshness budget while the real
		// database changes. Confirmation must not treat that cache as proof.
		f.cancel()
		<-f.monitorDone
		f.cancel = nil
		f.vs("set", "Port", "field-p1", "tag=31")
		r, err := f.engine.Read(f.ctx, id)
		must(t, err)
		req := apitypes.RequestID(time.Now())
		body, _ := json.Marshal(map[string]any{"request_id": req, "expected_sequence": r.Sequence, "decision": "confirm"})
		_, err = f.auth.ExecuteAuth(f.ctx, f.login.Grant, authn.Command{Method: "POST", URI: "/api/v1/transactions/" + id + "/decisions", Epoch: f.login.Claims.RequestEpoch, RequestID: req, Payload: body})
		var problem *apitypes.Problem
		if !errors.As(err, &problem) || problem.Code != "CONFIRMATION_EVIDENCE_UNAVAILABLE" || f.safeState(id).State == "confirmed" || f.proxy.sent.Load() != 1 {
			t.Fatal("stale cache confirmed or read proof mutated OVS", err)
		}
	})
	t.Run("lost_rollback_reply_retains_uncertainty_and_protection", func(t *testing.T) {
		f := newFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1"}, 20))
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		waitFor(t, func() bool { must(t, f.engine.SafetyTick(f.ctx)); return f.safeState(id).Outcome.Commit == "committed" })
		s := f.safeState(id)
		if s.Outcome.Target != nil || s.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 2 || f.vs("get", "Port", "field-p1", "tag") != "10" {
			t.Fatal("rollback reply loss invented success or replayed", s)
		}
		var n int
		must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE transaction_id=?", id).Scan(&n)
		}))
		if n != 1 {
			t.Fatal("unknown rollback released protection")
		}
	})
	t.Run("confirm_consumes_only_authoritative_workspace", func(t *testing.T) {
		f := newFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1"}, 20))
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "confirm")
		if f.safeState(id).State != "confirmed" {
			t.Fatal("not confirmed")
		}
		contract, _ := apicontract.New()
		op, path, _ := contract.Match("GET", "/api/v1/workspace")
		_, err := f.workspace.Read(f.ctx, publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}, publicapi.Query{Operation: op, Path: path, Values: url.Values{}})
		must(t, err)
		if len(f.envelope().Candidate.Intents) != 0 || f.vs("get", "Port", "field-p1", "tag") != "20" {
			t.Fatal("confirmed result lost")
		}
		var storedID string
		must(t, f.webStore.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT id FROM candidate_workspaces WHERE owner_id=?", f.login.Claims.PrincipalID).Scan(&storedID)
		}))
		if storedID != f.envelope().Candidate.ID {
			t.Fatal("workspace index and sealed Candidate diverged")
		}
	})
	t.Run("rollback_restores_touched_fields_and_preserves_unrelated", func(t *testing.T) {
		f := newFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1"}, 20))
		f.waitSafety(id, "awaiting-confirmation")
		f.vs("set", "Port", "field-p1", "other_config:synthetic=preserved", "--", "set", "Port", "field-p2", "tag=71")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if f.vs("get", "Port", "field-p1", "tag") != "10" || f.vs("get", "Port", "field-p2", "tag") != "71" || f.vs("get", "Port", "field-p1", "other_config:synthetic") != "preserved" {
			t.Fatal("rollback overwritten unrelated fields")
		}
		if f.proxy.sent.Load() != 2 {
			t.Fatal("unexpected native writes", f.proxy.sent.Load())
		}
	})
	t.Run("late_external_write_aborts_atomic_rollback", func(t *testing.T) {
		f := newFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1", "field-p2"}, 20))
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.hook(func() { f.vs("set", "Port", "field-p2", "tag=31") })
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if f.vs("get", "Port", "field-p1", "tag") != "20" || f.vs("get", "Port", "field-p2", "tag") != "31" {
			t.Fatal("partial or broad compensation")
		}
	})
	t.Run("replacement_generation_forbids_compensation", func(t *testing.T) {
		f := newFixture(t)
		var offset atomic.Int64
		f.configureSafety(&offset)
		id := f.safeApply(f.prepare([]string{"field-p1"}, 20))
		f.waitSafety(id, "awaiting-confirmation")
		f.stop("db")
		b, err := os.ReadFile(f.conf)
		must(t, err)
		must(t, os.WriteFile(f.conf+".copy", b, 0600))
		must(t, os.Rename(f.conf+".copy", f.conf))
		f.startDB()
		waitFor(t, func() bool { return f.inventory.PendingDigest() != "" })
		offset.Store(int64(121 * time.Second))
		f.waitSafety(id, "recovery-required")
		if f.proxy.sent.Load() != 1 || f.vs("get", "Port", "field-p1", "tag") != "20" {
			t.Fatal("old generation compensated")
		}
	})
	t.Run("SIGKILL_commit_reply_gap_recovers_and_compensates", func(t *testing.T) {
		f := newFixture(t)
		in := f.prepare([]string{"field-p1"}, 20)
		input := nativeCrashInput{Root: f.root, Grant: f.login.Grant, Request: in, AllowIDs: []string{f.binding("field-p1").ManagementID, f.binding("field-p2").ManagementID}, Safe: true}
		b, _ := json.Marshal(input)
		path := filepath.Join(f.root, "synthetic-safe-crash.json")
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
			t.Fatal("missed native Safe Apply crash", err, string(output))
		}
		f.store, err = sqlite.Open(context.Background(), f.options)
		must(t, err)
		f.auth, err = auth.New(f.store, key)
		must(t, err)
		f.startMonitor()
		var offset atomic.Int64
		offset.Store(int64(121 * time.Second))
		f.configureSafety(&offset)
		var id string
		must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT id FROM safe_applies").Scan(&id)
		}))
		f.waitSafety(id, "rolled-back")
		if f.proxy.sent.Load() != 2 || f.vs("get", "Port", "field-p1", "tag") != "10" {
			t.Fatal("unsafe replay or missing compensation", f.proxy.sent.Load())
		}
	})
}
