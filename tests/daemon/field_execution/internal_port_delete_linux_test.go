//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
)

func portDeletionFixture(t *testing.T, kernel bool) (*fixture, string, candidate.StoredIntent) {
	f, name, parent := internalPortFixture(t, kernel)
	var offset atomic.Int64
	f.configureSafety(&offset)
	in := f.prepareInternalPort(name, parent)
	id := f.safeApply(in)
	f.waitSafety(id, "awaiting-confirmation")
	f.decide(id, "confirm")
	f.waitSafety(id, "confirmed")
	must(t, f.inventory.SetLocalInternalPortDeleteTargets([]string{parent.ManagementID + ":" + name}))
	return f, name, in.Envelope.Candidate.Intents[0]
}
func (f *fixture) preparePortDeletion(b candidate.Binding) execution.Request {
	return f.prepareIntents([]any{map[string]any{"intent_id": repository.NewID(), "operation": candidate.InternalPortDelete, "object": b}})
}
func portDeletionGraph(g candidate.InternalPortGraph) candidate.StoredIntent {
	p := g.Configuration
	return candidate.StoredIntent{Operation: candidate.InternalPortCreate, Object: g.Port, PortCreation: &p}
}

func TestNativeInternalPortDeletion(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("explicit manager-owned internal Port deletion matrix")
	}
	t.Run("delete_confirm_retires_source_and_unused_compensation", func(t *testing.T) {
		f, name, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		d := in.Envelope.Candidate.Intents[0].PortDeletion
		if f.proxy.sent.Load() != 1 || f.vs("get", "Port", name, "_uuid") != source.Object.OVSUUID {
			t.Fatal("stage deleted live graph")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.graphAbsent(source)
		f.parentPreserved(source)
		f.graphStates(portDeletionGraph(d.Replacement), "pending")
		f.deletionProtection(id, 6)
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		f.graphStates(portDeletionGraph(d.Replacement), "tombstone")
		f.deletionProtection(id, 0)
		if f.proxy.sent.Load() != 2 {
			t.Fatal("extra deletion dispatch")
		}
	})
	t.Run("rollback_recreates_fresh_bindings_and_preserves_tombstones", func(t *testing.T) {
		f, name, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		d := in.Envelope.Candidate.Intents[0].PortDeletion
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(source)
		f.parentPreserved(source)
		f.graphStates(portDeletionGraph(d.Replacement), "active")
		for _, b := range d.Replacement.Bindings() {
			if f.vs("get", b.Table, name, "_uuid") != b.OVSUUID {
				t.Fatal("wrong replacement", b)
			}
		}
		f.deletionProtection(id, 0)
		view, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{d.Replacement.Configuration.Bridge})
		must(t, err)
		if candidate.Digest(view.PortDeletions.Graphs[d.Replacement.Port.ManagementID].Graph) != candidate.Digest(d.Replacement) {
			t.Fatal("compensated graph lost private ownership")
		}
		if f.proxy.sent.Load() != 3 {
			t.Fatal("compensation replayed")
		}
	})
	for _, change := range []string{"member", "parent_policy", "weak_reference", "unmonitored_configuration", "access_vlan"} {
		t.Run("delete_atomic_guards_refuse_late_"+change, func(t *testing.T) {
			f, name, source := portDeletionFixture(t, false)
			in := f.preparePortDeletion(source.Object)
			f.proxy.hook(func() {
				switch change {
				case "member":
					f.vs("add-port", source.PortCreation.BridgeName, name+"p", "--", "set", "Interface", name+"p", "type=dummy")
				case "parent_policy":
					f.vs("set", "Bridge", source.PortCreation.BridgeName, "fail_mode=secure")
				case "access_vlan":
					f.vs("set", "Port", name, "tag=21")
				case "weak_reference":
					f.vs("--id=@m", "create", "Mirror", "name=external-ref", "select_src_port="+source.Object.OVSUUID, "output_port="+f.binding("field-p1").OVSUUID, "--", "add", "Bridge", "br-field", "mirrors", "@m")
				case "unmonitored_configuration":
					f.vs("set", "Interface", name, "ingress_policing_rate=64")
				}
			})
			id := f.safeApply(in)
			f.waitSafety(id, "not-committed")
			f.graphStates(source, "active")
			if f.vs("get", "Port", name, "_uuid") != source.Object.OVSUUID || f.proxy.sent.Load() != 2 {
				t.Fatal("changed graph deleted")
			}
		})
	}
	t.Run("preflight_rejects_unmonitored_config_before_admission", func(t *testing.T) {
		f, name, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		f.vs("set", "Interface", name, "ingress_policing_rate=64")
		_, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("d", 64), in.Envelope)
		deletionError(t, err, "INTERNAL_PORT_GRAPH_CHANGED")
		if f.proxy.sent.Load() != 1 {
			t.Fatal("read-only preflight mutated OVS")
		}
	})
	t.Run("deletion_requires_independent_root_authority", func(t *testing.T) {
		f, _, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		must(t, f.inventory.SetLocalInternalPortDeleteTargets(nil))
		_, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("d", 64), in.Envelope)
		deletionError(t, err, "EXECUTION_PREFLIGHT_FAILED")
		if f.proxy.sent.Load() != 1 {
			t.Fatal("unauthorized delete sent")
		}
	})
	t.Run("rollback_same_name_takeover_is_conflict", func(t *testing.T) {
		f, name, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		// Deliberately race compensation after preflight, at native dispatch.
		var takeover string
		f.proxy.hook(func() {
			f.vs("add-port", source.PortCreation.BridgeName, name, "--", "set", "Interface", name, "type=dummy")
			takeover = f.vs("get", "Port", name, "_uuid")
		})
		f.decide(id, "rollback")
		f.waitSafety(id, "rollback-conflict")
		if takeover == "" || f.vs("get", "Port", name, "_uuid") != takeover {
			t.Fatal("same-name object replaced")
		}
		f.graphStates(portDeletionGraph(in.Envelope.Candidate.Intents[0].PortDeletion.Replacement), "pending")
		f.deletionProtection(id, 6)
	})

	for _, change := range []string{"parent_members", "local_configuration"} {
		t.Run("restoration_guards_refuse_late_"+change, func(t *testing.T) {
			f, name, source := portDeletionFixture(t, false)
			in := f.preparePortDeletion(source.Object)
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			f.proxy.hook(func() {
				if change == "parent_members" {
					f.vs("add-port", source.PortCreation.BridgeName, name+"x", "--", "set", "Interface", name+"x", "type=dummy")
				} else {
					f.vs("set", "Port", source.PortCreation.BridgeName, "tag=21")
				}
			})
			f.decide(id, "rollback")
			f.waitSafety(id, "rollback-conflict")
			f.graphAbsent(source)
			f.graphStates(portDeletionGraph(in.Envelope.Candidate.Intents[0].PortDeletion.Replacement), "pending")
			f.deletionProtection(id, 6)
			if f.vs("--if-exists", "get", "Port", name, "_uuid") != "" || f.proxy.sent.Load() != 3 {
				t.Fatal("unsafe restoration or repeated dispatch")
			}
		})
	}
	t.Run("lost_deletion_reply_survives_restart_without_replay_or_target", func(t *testing.T) {
		f, _, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		f.proxy.dropReply.Store(true)
		id := f.safeApply(in)
		f.waitSafety(id, "recovery-required")
		restarted, err := executions.New(f.store, key, f.executor)
		must(t, err)
		f.engine = restarted
		must(t, f.engine.Recover(f.ctx))
		r := f.observe(id, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Target != nil || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 2 {
			t.Fatal("lost reply invented target or replayed deletion", r)
		}
		f.graphAbsent(source)
		f.parentPreserved(source)
		f.deletionProtection(id, 6)
	})
	t.Run("lost_restoration_reply_keeps_new_identities_unverified", func(t *testing.T) {
		f, _, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.proxy.dropReply.Store(true)
		f.decide(id, "rollback")
		f.waitSafety(id, "recovery-required")
		waitFor(t, func() bool { must(t, f.engine.SafetyTick(f.ctx)); return f.safeState(id).Outcome.Commit == "committed" })
		s := f.safeState(id)
		if s.Outcome.Target != nil || s.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 3 {
			t.Fatal("restoration reply loss invented success or replayed", s)
		}
		f.graphAbsent(source)
		f.parentPreserved(source)
		f.graphStates(portDeletionGraph(in.Envelope.Candidate.Intents[0].PortDeletion.Replacement), "active")
		f.deletionProtection(id, 6)
	})
	t.Run("revoked_delete_capability_recreates_with_fresh_identity", func(t *testing.T) {
		f, _, source := portDeletionFixture(t, false)
		in := f.preparePortDeletion(source.Object)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		caps := slices.DeleteFunc(append([]string{}, f.login.Claims.Capabilities...), func(s string) bool { return s == "ovs.port.internal.delete" })
		b, _ := json.Marshal(caps)
		must(t, f.store.Write(f.ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", b, f.login.Claims.PrincipalID)
			return err
		}))
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(source)
		f.parentPreserved(source)
		f.graphStates(portDeletionGraph(in.Envelope.Candidate.Intents[0].PortDeletion.Replacement), "active")
	})
	t.Run("kernel_interface_removed_then_recreated_without_old_UUID", func(t *testing.T) {
		f, name, source := portDeletionFixture(t, true)
		in := f.preparePortDeletion(source.Object)
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		if _, err := net.InterfaceByName(name); err == nil {
			t.Fatal("Applied before kernel interface removal")
		}
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		if _, err := net.InterfaceByName(name); err != nil {
			t.Fatal("recreated internal interface absent", err)
		}
		f.graphAbsent(source)
		f.parentPreserved(source)
		f.graphStates(portDeletionGraph(in.Envelope.Candidate.Intents[0].PortDeletion.Replacement), "active")
	})
	t.Run("kernel_host_address_blocks_delete", func(t *testing.T) {
		f, name, source := portDeletionFixture(t, true)
		in := f.preparePortDeletion(source.Object)
		f.run("ip", "address", "add", "192.0.2.77/32", "dev", name)
		_, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("d", 64), in.Envelope)
		deletionError(t, err, "BRIDGE_HOST_DEPENDENCY_CHANGED")
		if f.proxy.sent.Load() != 1 {
			t.Fatal("host-dependent graph mutated")
		}
	})
}
