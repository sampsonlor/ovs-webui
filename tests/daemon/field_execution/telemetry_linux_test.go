//go:build linux

package fieldexecution

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

const telemetryBridge = "br-field"

type telemetryProtocol struct {
	name, table string
	original    []string
	columns     []string
	reviewed    map[string]any
}

var telemetryProtocols = []telemetryProtocol{
	{
		name: "sflow", table: "sFlow",
		original: []string{"sampling=1", "polling=1", "agent=127.0.0.1", "header=128", "external_ids:fixture=preserve"},
		columns:  []string{"targets", "sampling", "polling", "agent", "header", "external_ids"},
		reviewed: map[string]any{"sampling": 1, "polling": 2},
	},
	{
		name: "netflow", table: "NetFlow",
		original: []string{"active_timeout=1", "engine_type=2", "engine_id=7", "add_id_to_interface=false", "external_ids:fixture=preserve"},
		columns:  []string{"targets", "active_timeout", "engine_type", "engine_id", "add_id_to_interface", "external_ids"},
		reviewed: map[string]any{"active_timeout": 2},
	},
	{
		name: "ipfix", table: "IPFIX",
		original: []string{"sampling=1", "obs_domain_id=51", "obs_point_id=21", "cache_active_timeout=0", "cache_max_flows=0", "stats_interval=600", "template_interval=300", "other_config:fixture=preserve", "external_ids:fixture=preserve"},
		columns:  []string{"targets", "sampling", "obs_domain_id", "obs_point_id", "cache_active_timeout", "cache_max_flows", "stats_interval", "template_interval", "other_config", "external_ids"},
		reviewed: map[string]any{"sampling": 1, "obs_domain_id": 52, "obs_point_id": 22},
	},
}

type telemetryFixture struct {
	*fixture
	protocol telemetryProtocol
	bridge   candidate.Binding
	offset   atomic.Int64
}

func newTelemetryFixture(t *testing.T, protocol telemetryProtocol) *telemetryFixture {
	t.Helper()
	f := &telemetryFixture{fixture: newFixture(t), protocol: protocol}
	uuid := f.vs("get", "Bridge", telemetryBridge, "_uuid")
	var role string
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		if err := q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Bridge' AND state='active' AND ovs_uuid=?", uuid).Scan(&f.bridge.ManagementID, &f.bridge.OVSUUID, &f.bridge.Table, &f.bridge.Generation); err != nil {
			return err
		}
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
	}))
	must(t, f.auth.SetTelemetryExecutionRole(f.ctx, role, true))
	fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	f.login = fresh
	must(t, f.inventory.SetLocalTelemetryBindings([]string{f.bridge.ManagementID + ":" + protocol.name}))
	f.configureSafety(&f.offset)
	return f
}

func (f *telemetryFixture) reference() string {
	f.t.Helper()
	return f.vs("get", "Bridge", telemetryBridge, f.protocol.name)
}

func (f *telemetryFixture) seed(target string) string {
	f.t.Helper()
	args := []string{"--", "--id=@exporter", "create", f.protocol.table, "targets=" + strconv.Quote(target)}
	args = append(args, f.protocol.original...)
	f.vs(append(args, "--", "set", "Bridge", telemetryBridge, f.protocol.name+"=@exporter")...)
	uuid := f.reference()
	// The OVSDB CLI can see the new row before the manager's monitor does.
	waitFor(f.t, func() bool {
		view, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{f.bridge})
		return err == nil && view.Observation.Rows[f.protocol.table][uuid].UUID == uuid
	})
	return uuid
}

func (f *telemetryFixture) request(target string) execution.Request {
	f.t.Helper()
	intent := map[string]any{
		"intent_id": repository.NewID(), "operation": "telemetry.exporter.clear", "object": f.bridge, "protocol": f.protocol.name,
	}
	if target != "" {
		intent["operation"] = "telemetry.exporter.set"
		intent["targets"] = []string{target}
		intent["settings"] = f.protocol.reviewed
	}
	return f.prepareIntents([]any{intent})
}

func (f *telemetryFixture) configuration(uuid string, columns ...string) string {
	f.t.Helper()
	return f.vs(append([]string{"get", f.protocol.table, uuid}, columns...)...)
}

func (f *telemetryFixture) hasTarget(uuid, target string) bool {
	f.t.Helper()
	return uuid != "[]" && f.configuration(uuid, "targets") == "["+strconv.Quote(target)+"]"
}

func (f *telemetryFixture) assertAbsent() {
	f.t.Helper()
	if ref := f.reference(); ref != "[]" {
		f.t.Fatalf("expected no Bridge exporter reference, got %s", ref)
	}
	if rows := f.vs("--data=bare", "--no-headings", "--columns=_uuid", "find", f.protocol.table); rows != "" {
		f.t.Fatalf("unreferenced exporter remained in OVSDB: %s", rows)
	}
}

func (f *telemetryFixture) assertReviewed(uuid, target string) {
	f.t.Helper()
	if !f.hasTarget(uuid, target) {
		f.t.Fatal("Bridge did not bind the reviewed collector")
	}
	for column, value := range f.protocol.reviewed {
		if f.configuration(uuid, column) != fmt.Sprint(value) {
			f.t.Fatalf("Bridge did not adopt the reviewed %s", column)
		}
	}
}

func telemetryCollector(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	must(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (f *telemetryFixture) assertDelivery(conn *net.UDPConn, sender byte, observationDomain uint32) {
	f.t.Helper()
	// Each stage uses a different source IP in the synthetic packet. A queued
	// datagram from the original configuration cannot satisfy the rollback check.
	source := []byte{198, 18, 109, sender}
	packet := fmt.Sprintf("eth(src=50:54:00:00:00:09,dst=50:54:00:00:00:0a),eth_type(0x0800),ipv4(src=%s,dst=198.18.109.12,proto=6,tos=0,ttl=64,frag=no),tcp(src=1234,dst=80)", net.IP(source))
	socket := filepath.Join(f.root, "switch.ctl")
	buffer := make([]byte, 65536)
	waitFor(f.t, func() bool {
		f.run("ovs-appctl", "-t", socket, "netdev-dummy/receive", "field-p1", packet, packet, packet)
		if f.protocol.name == "netflow" {
			// Dummy receive queues packets. Purge after processing so the
			// flow's final counters are exported without a long idle timeout.
			time.Sleep(100 * time.Millisecond)
			f.run("ovs-appctl", "-t", socket, "revalidator/purge")
		}
		must(f.t, conn.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
		for {
			n, remote, err := conn.ReadFromUDP(buffer)
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				return false
			}
			must(f.t, err)
			if remote.IP.IsLoopback() && f.protocol.sample(buffer[:n], source, observationDomain) {
				return true
			}
		}
	})
}

func (p telemetryProtocol) sample(datagram, source []byte, observationDomain uint32) bool {
	if !bytes.Contains(datagram, source) {
		return false
	}
	switch p.name {
	case "sflow":
		return len(datagram) > 28 && binary.BigEndian.Uint32(datagram) == 5
	case "netflow":
		return len(datagram) >= 72 && binary.BigEndian.Uint16(datagram) == 5 && binary.BigEndian.Uint16(datagram[2:]) > 0
	case "ipfix":
		return len(datagram) > 20 && binary.BigEndian.Uint16(datagram) == 10 && int(binary.BigEndian.Uint16(datagram[2:])) == len(datagram) &&
			binary.BigEndian.Uint32(datagram[12:]) == observationDomain && binary.BigEndian.Uint16(datagram[16:]) >= 256
	}
	return false
}

func TestNativeTelemetryExecution(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires an explicit disposable Linux telemetry fixture")
	}
	for _, protocol := range telemetryProtocols {
		t.Run(protocol.name+"_update_and_rollback_deliver_to_exact_collectors", func(t *testing.T) {
			f := newTelemetryFixture(t, protocol)
			original, reviewed := telemetryCollector(t), telemetryCollector(t)
			name := original.LocalAddr().String()
			f.vs("set", "Bridge", telemetryBridge, "external_ids:fixture-peer=preserve")
			uuid := f.seed(name)
			in := f.request(reviewed.LocalAddr().String())
			allBefore := f.configuration(uuid, protocol.columns...)
			opaque := slices.DeleteFunc(slices.Clone(protocol.columns), func(column string) bool {
				_, edited := protocol.reviewed[column]
				return edited || column == "targets"
			})
			opaqueBefore := f.configuration(uuid, opaque...)
			if f.proxy.sent.Load() != 0 || !f.hasTarget(uuid, name) {
				t.Fatal("staging or validation changed the exporter")
			}
			f.assertDelivery(original, 241, 51)
			id := f.safeApply(in)
			f.waitSafety(id, "awaiting-confirmation")
			f.assertReviewed(f.reference(), reviewed.LocalAddr().String())
			if f.reference() != uuid || f.configuration(uuid, opaque...) != opaqueBefore {
				t.Fatal("applying changed the native identity or an untouched field")
			}
			f.assertDelivery(reviewed, 242, 52)
			f.vs("set", "Port", "field-p2", "external_ids:fixture-peer=late")
			f.decide(id, "rollback")
			f.waitSafety(id, "rolled-back")
			if f.reference() != uuid || f.configuration(uuid, protocol.columns...) != allBefore ||
				f.vs("get", "Bridge", telemetryBridge, "external_ids:fixture-peer") != "preserve" ||
				f.vs("get", "Port", "field-p2", "external_ids:fixture-peer") != "late" ||
				f.proxy.sent.Load() != 2 {
				t.Fatal("rollback did not restore the exporter and preserve unrelated configuration")
			}
			f.assertDelivery(original, 243, 51)
		})
		t.Run(protocol.name+"_create_and_rollback_restore_absence", func(t *testing.T) {
			f := newTelemetryFixture(t, protocol)
			collector := telemetryCollector(t).LocalAddr().String()
			f.assertAbsent()
			id := f.safeApply(f.request(collector))
			f.waitSafety(id, "awaiting-confirmation")
			f.assertReviewed(f.reference(), collector)
			f.decide(id, "rollback")
			f.waitSafety(id, "rolled-back")
			f.assertAbsent()
			if f.proxy.sent.Load() != 2 {
				t.Fatal("creation and rollback must each dispatch once")
			}
		})
		t.Run(protocol.name+"_clear_and_rollback_restore_all_columns", func(t *testing.T) {
			f := newTelemetryFixture(t, protocol)
			original := telemetryCollector(t)
			uuid := f.seed(original.LocalAddr().String())
			before := f.configuration(uuid, protocol.columns...)
			id := f.safeApply(f.request(""))
			f.waitSafety(id, "awaiting-confirmation")
			f.assertAbsent()
			f.decide(id, "rollback")
			f.waitSafety(id, "rolled-back")
			restored := f.reference()
			if restored == "[]" || restored == uuid || f.configuration(restored, protocol.columns...) != before || f.proxy.sent.Load() != 2 {
				t.Fatal("rollback failed to restore the exact configuration of the collected native row")
			}
			f.assertDelivery(original, 244, 51)
		})
	}
	t.Run("database_only_commit_cannot_be_confirmed_and_deadline_restores_absence", func(t *testing.T) {
		f := newTelemetryFixture(t, telemetryProtocols[1])
		in := f.request(telemetryCollector(t).LocalAddr().String())
		pid := f.pid("switch")
		must(t, syscall.Kill(pid, syscall.SIGSTOP))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool {
			return r.Outcome.Commit == "committed" && r.Outcome.Reason == "awaiting-ovs-vswitchd"
		})
		if state := f.safeState(id); state.Confirmation != nil || state.State == "awaiting-confirmation" {
			t.Fatal("database-only exporter commit enabled confirmation")
		}
		_, err := f.executor.PrepareRollback(f.ctx, r.Plan, candidate.Digest("telemetry-deadline-preflight"))
		must(t, err)
		f.offset.Store(int64(safety.PreparationBudget + time.Second))
		waitFor(t, func() bool {
			must(t, f.engine.SafetyTick(f.ctx))
			return f.proxy.sent.Load() == 2
		})
		must(t, syscall.Kill(pid, syscall.SIGCONT))
		f.waitSafety(id, "rolled-back")
		f.assertAbsent()
	})
	t.Run("late_second_bridge_reference_rejects_update", func(t *testing.T) {
		f := newTelemetryFixture(t, telemetryProtocols[0])
		original, reviewed := telemetryCollector(t).LocalAddr().String(), telemetryCollector(t).LocalAddr().String()
		uuid := f.seed(original)
		f.vs("add-br", "br-shared", "--", "set", "Bridge", "br-shared", "datapath_type=dummy")
		in := f.request(reviewed)
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("telemetry-bridge-reference-race"), in.Envelope)
		must(t, err)
		f.proxy.hook(func() { f.vs("set", "Bridge", "br-shared", "sflow="+uuid) })
		outcome := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if outcome.Commit != "rejected" || f.reference() != uuid || f.vs("get", "Bridge", "br-shared", "sflow") != uuid ||
			!f.hasTarget(uuid, original) || f.proxy.sent.Load() != 1 {
			t.Fatal("an exporter that became shared was changed", outcome)
		}
	})
	t.Run("late_flow_sample_collector_set_reference_rejects_clear", func(t *testing.T) {
		f := newTelemetryFixture(t, telemetryProtocols[2])
		uuid := f.seed(telemetryCollector(t).LocalAddr().String())
		in := f.request("")
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("telemetry-ipfix-reference-race"), in.Envelope)
		must(t, err)
		f.proxy.hook(func() { f.vs("create", "Flow_Sample_Collector_Set", "id=7", "bridge="+f.bridge.OVSUUID, "ipfix="+uuid) })
		outcome := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if outcome.Commit != "rejected" || f.reference() != uuid ||
			f.vs("--data=bare", "--no-headings", "--columns=ipfix", "find", "Flow_Sample_Collector_Set", "id=7") != uuid ||
			f.proxy.sent.Load() != 1 {
			t.Fatal("an IPFIX exporter in use by a Flow Sample Collector Set was cleared", outcome)
		}
	})
	t.Run("external_exporter_edit_blocks_rollback", func(t *testing.T) {
		f := newTelemetryFixture(t, telemetryProtocols[2])
		original, reviewed := telemetryCollector(t).LocalAddr().String(), telemetryCollector(t).LocalAddr().String()
		uuid := f.seed(original)
		id := f.safeApply(f.request(reviewed))
		f.waitSafety(id, "awaiting-confirmation")
		f.vs("set", "IPFIX", uuid, "external_ids:fixture=late")
		// Exercise rollback preflight with the edit already observed. If a
		// change races that snapshot, the native transaction guards reject it.
		waitFor(t, func() bool {
			view, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{f.bridge})
			if err != nil {
				return false
			}
			labels, _ := view.Observation.Rows["IPFIX"][uuid].Configuration["external_ids"].(map[string]any)
			return labels["fixture"] == "late"
		})
		f.decide(id, "rollback")
		state := f.waitSafety(id, "rollback-conflict")
		if ref := f.reference(); ref != uuid {
			t.Fatalf("rollback changed the IPFIX reference to %s; expected %s", ref, uuid)
		}
		if label := f.configuration(uuid, "external_ids:fixture"); label != "late" {
			t.Fatalf("rollback changed the externally edited label to %q; expected late", label)
		}
		if !f.hasTarget(uuid, reviewed) {
			t.Fatalf("rollback changed the IPFIX targets to %s; expected %s", f.configuration(uuid, "targets"), reviewed)
		}
		if sends := f.proxy.sent.Load(); sends != 1 {
			t.Fatalf("rollback dispatched despite the observed conflict; %d writes, state reason %s", sends, state.Reason)
		}
	})
}
