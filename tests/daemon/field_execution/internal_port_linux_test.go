//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func internalPortFixture(t *testing.T, kernel bool) (*fixture, string, candidate.Binding) {
	f, parentName := newBridgeFixture(t, kernel)
	f.vs("add-br", parentName, "--", "set", "Bridge", parentName, "datapath_type=system")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", parentName) })
	var parent candidate.Binding
	waitFor(t, func() bool {
		s, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{{Table: "Bridge"}})
		if err != nil {
			return false
		}
		for _, p := range s.InternalPorts.Parents {
			if p.Name == parentName && p.Eligible {
				parent = p.Binding
				return true
			}
		}
		return false
	})
	name := "pi-" + repository.NewID()[:8]
	must(t, f.inventory.SetLocalInternalPortTargets([]string{parent.ManagementID + ":" + name}))
	return f, name, parent
}
func (f *fixture) prepareInternalPort(name string, parent candidate.Binding) execution.Request {
	return f.prepareIntents([]any{map[string]any{"intent_id": repository.NewID(), "operation": candidate.InternalPortCreate, "object": parent, "name": name, "vlan_id": 20}})
}
func (f *fixture) parentPreserved(i candidate.StoredIntent) {
	f.t.Helper()
	p := i.PortCreation
	for _, b := range []candidate.Binding{p.Bridge, p.LocalPort, p.LocalInterface} {
		if f.vs("get", b.Table, p.BridgeName, "_uuid") != b.OVSUUID {
			f.t.Fatal("parent/local identity changed", b)
		}
	}
	for _, b := range p.Members {
		if f.vs("get", "Port", b.OVSUUID, "_uuid") != b.OVSUUID {
			f.t.Fatal("existing member removed")
		}
	}
}

func TestNativeInternalPort(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit internal Port lifecycle matrix")
	}
	t.Run("create_confirm_access_vlan_and_preserve_parent", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareInternalPort(name, parent)
		i := in.Envelope.Candidate.Intents[0]
		if f.proxy.sent.Load() != 0 || f.vs("--if-exists", "get", "Port", name, "_uuid") != "" {
			t.Fatal("draft wrote live configuration")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.graphStates(i, "active")
		f.parentPreserved(i)
		f.deletionProtection(id, 4)
		if f.vs("get", "Port", name, "tag") != "20" || f.vs("get", "Port", name, "vlan_mode") != "access" || f.vs("get", "Interface", name, "type") != "internal" || f.vs("get", "Interface", name, "ofport") == "65534" {
			t.Fatal("invalid independent access interface")
		}
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		f.deletionProtection(id, 0)
		must(t, f.engine.Recover(f.ctx))
		if f.proxy.sent.Load() != 1 {
			t.Fatal("replayed creation")
		}
	})
	t.Run("rollback_GC_recreate_fresh_identity", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareInternalPort(name, parent)
		i := in.Envelope.Candidate.Intents[0]
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(i)
		f.parentPreserved(i)
		f.deletionProtection(id, 0)
		if _, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("a", 64), in.Envelope); err == nil {
			t.Fatal("retired identities reused")
		}
		f.discardPortDraft()
		again := f.prepareInternalPort(name, parent)
		j := again.Envelope.Candidate.Intents[0]
		for k, b := range candidate.CreationBindings(j) {
			if b == candidate.CreationBindings(i)[k] {
				t.Fatal("same-name identity reuse")
			}
		}
		id = f.safeApply(again)
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		f.graphStates(i, "tombstone")
		f.graphStates(j, "active")
	})
	for _, change := range []string{"name", "parent-policy", "parent-members"} {
		t.Run("atomic_create_rejects_late_"+change, func(t *testing.T) {
			f, name, parent := internalPortFixture(t, false)
			in := f.prepareInternalPort(name, parent)
			i := in.Envelope.Candidate.Intents[0]
			f.proxy.hook(func() {
				switch change {
				case "name":
					f.vs("add-port", i.PortCreation.BridgeName, name, "--", "set", "Interface", name, "type=internal")
				case "parent-policy":
					f.vs("set", "Bridge", parent.OVSUUID, "fail_mode=secure")
				case "parent-members":
					f.vs("add-port", i.PortCreation.BridgeName, "late-member", "--", "set", "Interface", "late-member", "type=dummy")
				}
			})
			r := f.submit(in)
			if r.Outcome.Commit != "rejected" {
				t.Fatal("late change committed", r)
			}
			f.graphAbsent(i)
			f.parentPreserved(i)
		})
	}
	for _, change := range []string{"member", "weak_reference", "unmonitored_configuration"} {
		t.Run("rollback_refuses_late_"+change, func(t *testing.T) {
			f, name, parent := internalPortFixture(t, false)
			var offset atomic.Int64
			f.configureSafety(&offset)
			in := f.prepareInternalPort(name, parent)
			i := in.Envelope.Candidate.Intents[0]
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			f.proxy.hook(func() {
				switch change {
				case "member":
					f.vs("add-port", i.PortCreation.BridgeName, "late-member", "--", "set", "Interface", "late-member", "type=dummy")
				case "weak_reference":
					f.vs("--id=@m", "create", "Mirror", "name=external-ref", "select_src_port="+i.Object.OVSUUID, "output_port="+f.binding("field-p1").OVSUUID, "--", "add", "Bridge", "br-field", "mirrors", "@m")
				case "unmonitored_configuration":
					f.vs("set", "Interface", name, "ingress_policing_rate=64")
				}
			})
			f.decide(id, "rollback")
			f.waitSafety(id, "rollback-conflict")
			f.graphStates(i, "active")
			f.parentPreserved(i)
			f.deletionProtection(id, 4)
			if f.proxy.sent.Load() != 2 {
				t.Fatal("replayed compensation")
			}
		})
	}
	t.Run("lost_create_reply_restart_does_not_guess_target_or_replay", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, false)
		in := f.prepareInternalPort(name, parent)
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
			t.Fatal("invented Applied or replayed", r)
		}
		f.graphStates(in.Envelope.Candidate.Intents[0], "active")
	})
	t.Run("lost_cleanup_reply_retains_protection_without_replay", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareInternalPort(name, parent)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		waitFor(t, func() bool { must(t, f.engine.SafetyTick(f.ctx)); return f.safeState(id).Outcome.Commit == "committed" })
		s := f.safeState(id)
		if s.Outcome.Target != nil || s.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 2 {
			t.Fatal(s)
		}
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
		f.parentPreserved(in.Envelope.Candidate.Intents[0])
		f.deletionProtection(id, 4)
	})
	t.Run("revoked_capability_compensates", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, false)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareInternalPort(name, parent)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		caps := slices.DeleteFunc(append([]string{}, f.login.Claims.Capabilities...), func(c string) bool { return c == "ovs.port.internal.create" })
		body, _ := json.Marshal(caps)
		must(t, f.store.Write(f.ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, f.login.Claims.PrincipalID)
			return err
		}))
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
		f.parentPreserved(in.Envelope.Candidate.Intents[0])
	})
	t.Run("independent_root_grant_required", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, false)
		in := f.prepareInternalPort(name, parent)
		must(t, f.inventory.SetLocalInternalPortTargets(nil))
		_, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("a", 64), in.Envelope)
		deletionError(t, err, "EXECUTION_PREFLIGHT_FAILED")
		if f.proxy.sent.Load() != 0 {
			t.Fatal("unauthorized dispatch")
		}
	})
	t.Run("kernel_internal_interface_create_cleanup", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareInternalPort(name, parent)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		if _, err := net.InterfaceByName(name); err != nil {
			t.Fatal("Applied without kernel interface", err)
		}
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
		f.parentPreserved(in.Envelope.Candidate.Intents[0])
		waitFor(t, func() bool { _, err := net.InterfaceByName(name); return err != nil })
	})
	t.Run("kernel_host_address_blocks_cleanup", func(t *testing.T) {
		f, name, parent := internalPortFixture(t, true)
		var offset atomic.Int64
		f.configureSafety(&offset)
		in := f.prepareInternalPort(name, parent)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.run("ip", "addr", "add", "192.0.2.19/32", "dev", name)
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		f.graphStates(in.Envelope.Candidate.Intents[0], "active")
		f.parentPreserved(in.Envelope.Candidate.Intents[0])
		f.deletionProtection(id, 4)
		if f.proxy.sent.Load() != 1 {
			t.Fatal("host dependency ignored")
		}
	})
}

func (f *fixture) discardPortDraft() {
	f.t.Helper()
	contract, err := apicontract.New()
	must(f.t, err)
	subject := publicapi.Subject{ID: f.login.Claims.PrincipalID, Credential: f.login.Grant}
	op, path, _ := contract.Match("GET", "/api/v1/candidate")
	_, err = f.workspace.Read(f.ctx, subject, publicapi.Query{Operation: op, Path: path, Values: url.Values{}})
	must(f.t, err)
	e := f.envelope()
	id := apitypes.RequestID(time.Now())
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "discard"})
	op, path, _ = contract.Match("PATCH", "/api/v1/candidate")
	_, err = f.workspace.Execute(f.ctx, subject, publicapi.Query{Operation: op, Path: path, Values: url.Values{}}, requests.Command{Principal: subject.ID, Epoch: e.Epoch, Domain: "workspace", ID: id, Operation: "changeCandidate", Method: "PATCH", URI: "/api/v1/candidate", Precondition: string(rune(34)) + e.Candidate.Revision + string(rune(34)), Payload: body})
	must(f.t, err)
}
