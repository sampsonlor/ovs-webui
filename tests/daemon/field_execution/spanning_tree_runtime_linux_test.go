//go:build linux

package fieldexecution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

// Only the disposable fixture configures OVS through vsctl. The production
// reader is the fixed native transport, with no process or shell invocation.
func TestNativeSpanningTreeRuntime(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires the isolated native OVS fixture")
	}
	f := newFixture(t)
	control := ovsdb.SpanningTreeControl{Socket: filepath.Join(f.root, "switch.ctl"), PeerUID: uint32(os.Geteuid())}
	vs := func(args ...string) string {
		return f.run(append([]string{"ovs-vsctl", "--timeout=5", "--db=unix:" + f.dbSocket}, args...)...)
	}
	read := func(protocol string) ovsdb.SpanningTreeRuntimeSample {
		t.Helper()
		sample, err := ovsdb.ReadSpanningTreeRuntime(context.Background(), control, protocol, "br-tree")
		must(t, err)
		if sample.ObservedAt.IsZero() || sample.Daemon == "" {
			t.Fatal("missing daemon/time provenance", sample)
		}
		return sample
	}
	configuration := func(protocol string, values map[string]any) spanningtree.Configuration {
		stp, rstp := protocol == "stp", protocol == "rstp"
		return spanningtree.Configuration{STP: &stp, RSTP: &rstp, Other: values}
	}
	set := func(protocol string, values map[string]any) {
		args := []string{"set", "Bridge", "br-tree", protocol + "_enable=true", "other_config={}"}
		for k, v := range values {
			args = append(args, "other_config:"+k+"="+v.(string))
		}
		vs(args...)
	}
	for _, tc := range []struct {
		name, protocol string
		values         map[string]any
		priority       int
		timers         spanningtree.Timers
		compatible     bool
	}{
		{"stp_unset_defaults", "stp", map[string]any{}, 32768, spanningtree.Timers{HelloTime: 2, MaxAge: 20, ForwardDelay: 15}, true},
		{"stp_minimum", "stp", map[string]any{"stp-priority": "0", "stp-hello-time": "1", "stp-max-age": "6", "stp-forward-delay": "4"}, 0, spanningtree.Timers{HelloTime: 1, MaxAge: 6, ForwardDelay: 4}, true},
		{"stp_clamped_age", "stp", map[string]any{"stp-max-age": "5"}, 32768, spanningtree.Timers{HelloTime: 2, MaxAge: 6, ForwardDelay: 15}, false},
		{"stp_clamped_delay", "stp", map[string]any{"stp-forward-delay": "4"}, 32768, spanningtree.Timers{HelloTime: 2, MaxAge: 20, ForwardDelay: 11}, false},
		{"stp_explicit_hello_unit_caveat", "stp", map[string]any{"stp-hello-time": "10", "stp-max-age": "22", "stp-forward-delay": "12"}, 32768, spanningtree.Timers{HelloTime: 1, MaxAge: 22, ForwardDelay: 12}, false},
		{"rstp_unset_defaults", "rstp", map[string]any{}, 32768, spanningtree.Timers{HelloTime: 2, MaxAge: 20, ForwardDelay: 15}, true},
		{"rstp_minimum", "rstp", map[string]any{"rstp-priority": "0", "rstp-max-age": "6", "rstp-forward-delay": "4"}, 0, spanningtree.Timers{HelloTime: 2, MaxAge: 6, ForwardDelay: 4}, true},
		{"rstp_rounded_priority", "rstp", map[string]any{"rstp-priority": "4097"}, 4096, spanningtree.Timers{HelloTime: 2, MaxAge: 20, ForwardDelay: 15}, false},
		{"rstp_retained_delay", "rstp", map[string]any{"rstp-forward-delay": "4"}, 32768, spanningtree.Timers{HelloTime: 2, MaxAge: 20, ForwardDelay: 15}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vs("--if-exists", "del-br", "br-tree")
			vs("add-br", "br-tree", "--", "set", "Bridge", "br-tree", "datapath_type=dummy")
			set(tc.protocol, tc.values)
			sample := read(tc.protocol)
			if sample.Runtime.Local.Priority != tc.priority || sample.Runtime.Local.Timers != tc.timers {
				t.Fatal("configured defaults/rounded/clamped values confused with installed local parameters", sample)
			}
			c := configuration(tc.protocol, tc.values)
			assessment := spanningtree.AssessTransition(c, c, sample.Runtime)
			if (assessment.State == "compatible") != tc.compatible {
				t.Fatal("native mismatch passed transition analysis", assessment)
			}
		})
	}
	t.Run("rstp_forward_and_reverse_order_are_independent", func(t *testing.T) {
		vs("--if-exists", "del-br", "br-tree")
		vs("add-br", "br-tree", "--", "set", "Bridge", "br-tree", "datapath_type=dummy")
		low := map[string]any{"rstp-max-age": "6", "rstp-forward-delay": "4"}
		high := map[string]any{"rstp-max-age": "40", "rstp-forward-delay": "30"}
		set("rstp", low)
		before := read("rstp")
		assessment := spanningtree.AssessTransition(configuration("rstp", low), configuration("rstp", high), before.Runtime)
		if len(assessment.Checks) != 1 || assessment.Checks[0] != "RSTP_FORWARD_TIMER_ORDER_UNPROVEN" {
			t.Fatal("unsafe forward setter order accepted", assessment)
		}
		// Observe the real failure rather than assuming a valid endpoint installs.
		set("rstp", high)
		after := read("rstp")
		if after.Runtime.Local.MaxAge != 6 || after.Runtime.Local.ForwardDelay != 30 {
			t.Fatal("native max-age-first behavior changed; review the transition model", after)
		}
		if got := spanningtree.AssessTransition(configuration("rstp", high), configuration("rstp", low), after.Runtime); len(got.Checks) != 1 || got.Checks[0] != "SPANNING_TREE_INSTALLED_ORIGINAL_MISMATCH" {
			t.Fatal("configured high pair invented installed original", got)
		}
		// Fixture-only extra reconfiguration installs the high pair; this is NOT
		// a production retry policy or an automatically generated intermediate.
		vs("set", "Bridge", "br-tree", "other_config:rstp-priority=4096")
		high["rstp-priority"] = "4096"
		settled := read("rstp")
		if settled.Runtime.Local.MaxAge != 40 || settled.Runtime.Local.ForwardDelay != 30 {
			t.Fatal(settled)
		}
		assessment = spanningtree.AssessTransition(configuration("rstp", high), configuration("rstp", low), settled.Runtime)
		if len(assessment.Checks) != 1 || assessment.Checks[0] != "RSTP_ROLLBACK_TIMER_ORDER_UNPROVEN" {
			t.Fatal("safe forward pair silently granted unsafe rollback", assessment)
		}
		set("rstp", low)
		if got := read("rstp").Runtime.Local.Timers; got != (spanningtree.Timers{HelloTime: 2, MaxAge: 6, ForwardDelay: 4}) {
			t.Fatal("decreasing native transition failed", got)
		}
	})
	t.Run("rstp_compatible_overlap_installs_both_directions", func(t *testing.T) {
		vs("--if-exists", "del-br", "br-tree")
		vs("add-br", "br-tree", "--", "set", "Bridge", "br-tree", "datapath_type=dummy")
		before := map[string]any{}
		after := map[string]any{"rstp-priority": "04096", "rstp-max-age": "22", "rstp-forward-delay": "16"}
		for _, pair := range [][2]map[string]any{{before, after}, {after, before}} {
			set("rstp", pair[0])
			sample := read("rstp")
			if got := spanningtree.AssessTransition(configuration("rstp", pair[0]), configuration("rstp", pair[1]), sample.Runtime); got.State != "compatible" || len(got.Checks) != 0 {
				t.Fatal("compatible pair rejected", got)
			}
			set("rstp", pair[1])
			installed := read("rstp")
			if got := spanningtree.AssessTransition(configuration("rstp", pair[1]), configuration("rstp", pair[0]), installed.Runtime); got.State != "compatible" {
				t.Fatal("desired values not installed after actual transition", got, installed)
			}
		}
	})
	t.Run("stp_nonroot_local_timers_are_not_bridge_section_operational_timers", func(t *testing.T) {
		vs("--if-exists", "del-br", "br-tree")
		vs("add-br", "br-tree", "--", "set", "Bridge", "br-tree", "datapath_type=dummy", "stp_enable=true", "other_config:stp-max-age=6", "other_config:stp-forward-delay=4", "--", "add-br", "br-root", "--", "set", "Bridge", "br-root", "datapath_type=dummy", "stp_enable=true", "other_config:stp-priority=4096", "--", "add-port", "br-tree", "tree-patch", "--", "set", "Interface", "tree-patch", "type=patch", "options:peer=root-patch", "--", "add-port", "br-root", "root-patch", "--", "set", "Interface", "root-patch", "type=patch", "options:peer=tree-patch")
		waitFor(t, func() bool {
			sample, err := ovsdb.ReadSpanningTreeRuntime(context.Background(), control, "stp", "br-tree")
			return err == nil && sample.Runtime.OperationalTimers != nil && *sample.Runtime.OperationalTimers == (spanningtree.Timers{HelloTime: 2, MaxAge: 20, ForwardDelay: 15}) && sample.Runtime.Local.MaxAge == 6 && sample.Runtime.Local.ForwardDelay == 4
		})
		sample := read("stp")
		if sample.Runtime.Local.Priority != 32768 || sample.Runtime.Local.HelloTime != 2 {
			t.Fatal("root identity became local identity", sample)
		}
		vs("del-br", "br-root")
	})
	t.Run("peer_restart_missing_protocol_and_paused_daemon_do_not_reuse_sample", func(t *testing.T) {
		vs("set", "Bridge", "br-tree", "stp_enable=true", "rstp_enable=false")
		before := read("stp")
		if _, err := ovsdb.ReadSpanningTreeRuntime(context.Background(), control, "rstp", "br-tree"); err == nil {
			t.Fatal("missing native protocol inferred from configuration")
		}
		pid := f.pid("switch")
		must(t, syscall.Kill(pid, syscall.SIGSTOP))
		start := time.Now()
		_, err := ovsdb.ReadSpanningTreeRuntime(context.Background(), control, "stp", "br-tree")
		must(t, syscall.Kill(pid, syscall.SIGCONT))
		if err == nil || time.Since(start) > 2800*time.Millisecond {
			t.Fatal("paused daemon reused cached/configured values or exceeded budget", err)
		}
		f.stop("switch")
		f.run("ovs-vswitchd", "--enable-dummy", "--pidfile="+filepath.Join(f.root, "switch.pid"), "--unixctl="+control.Socket, "--detach", "--no-chdir", "unix:"+f.dbSocket)
		control.ExpectedDaemon = before.Daemon
		if _, err := ovsdb.ReadSpanningTreeRuntime(context.Background(), control, "stp", "br-tree"); err == nil || !strings.Contains(err.Error(), "PEER_UNPROVEN") {
			t.Fatal("restarted daemon inherited old incarnation", err)
		}
		control.ExpectedDaemon = ""
		fresh := read("stp")
		if fresh.Daemon == before.Daemon {
			t.Fatal("restart identity did not change")
		}
	})
}
