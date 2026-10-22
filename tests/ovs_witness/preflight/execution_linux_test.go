//go:build linux

package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func runExecutionCases(t *testing.T, ctx context.Context, e *ovsdb.Executor, inv *inventory.Service, b candidate.Binding, control ovsdb.SpanningTreeControl, build ovsdb.SpanningTreeWitnessBuild, authorize func(context.Context) error, vs func(...string) string, draft func() candidate.StoredIntent, grants func(), role func([]string), caps, oldCaps []string) {
	must(t, e.ConfigureSpanningTreeExecution(ovsdb.SpanningTreeExecutionOptions{ControlSocket: control.Socket, PeerUID: control.PeerUID, Executable: build.Executable, Manifest: build.Manifest, ManifestDigest: build.ManifestDigest}))
	writeCtx := execution.WithAdmission(ctx, authorize)
	ip := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("isolated data Port fixture: %v %s", err, out)
		}
	}
	ip("link", "add", "wexec-data", "type", "veth", "peer", "name", "wexec-peer")
	defer func() { vs("--if-exists", "del-port", "br-witness", "wexec-data"); ip("link", "del", "wexec-data") }()
	ip("link", "set", "wexec-data", "up")
	ip("link", "set", "wexec-peer", "up")
	vs("add-port", "br-witness", "wexec-data")
	defer func() {
		grants()
		role(caps)
		vs("set", "Bridge", "br-witness", "stp_enable=true", "rstp_enable=false")
		for _, key := range candidate.SpanningTreeBasicKeys {
			vs("remove", "Bridge", "br-witness", "other_config", key)
		}
	}()
	setup := func(protocol string) {
		vs("set", "Bridge", "br-witness", "stp_enable="+map[bool]string{true: "true", false: "false"}[protocol == "stp"], "rstp_enable="+map[bool]string{true: "true", false: "false"}[protocol == "rstp"])
		for _, key := range candidate.SpanningTreeBasicKeys {
			vs("remove", "Bridge", "br-witness", "other_config", key)
		}
		vs("set", "Bridge", "br-witness", "other_config:execution-opaque=preserve")
	}
	envelope := func(protocol string, params map[string]*string) candidate.Envelope {
		i := draft()
		i.Operation = candidate.SpanningTreeConfigure
		data, _ := json.Marshal(i.SpanningTree.Before)
		must(t, json.Unmarshal(data, &i.SpanningTree.After))
		i.SpanningTree.After.STP, i.SpanningTree.After.RSTP = protocol == "stp", protocol == "rstp"
		for key, value := range params {
			i.SpanningTree.After.Parameters[key] = value
		}
		return candidate.Envelope{Owner: repository.NewID(), Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Generation: &b.Generation, Intents: []candidate.StoredIntent{i}}}
	}
	prepare := func(in candidate.Envelope) execution.Plan {
		id := repository.NewID()
		p, err := e.Prepare(writeCtx, id, candidate.Digest([]any{id, in}), in)
		must(t, err)
		if len(p.GraphBindings) != 5 {
			t.Fatal("graph reservation omitted local or real system data Port members", p.GraphBindings)
		}
		return p
	}
	commit := func(p execution.Plan) execution.Outcome {
		calls := 0
		o := e.Commit(writeCtx, p, func() error { calls++; return nil })
		if calls != 1 || o.Commit != "committed" || o.Target == nil {
			t.Fatal("native commit", calls, o)
		}
		return o
	}
	observe := func(p execution.Plan, o execution.Outcome) execution.Outcome {
		end := time.Now().Add(execution.AppliedFor)
		for {
			o = e.Observe(ctx, p, o)
			if o.Applied == "applied" {
				return o
			}
			if time.Now().After(end) {
				t.Fatal("native installed proof never arrived", o)
				return o
			}
			time.Sleep(20 * time.Millisecond) // observe only; never repeat a write
		}
	}
	for _, change := range []struct{ before, after string }{{"disabled", "stp"}, {"disabled", "rstp"}, {"stp", "disabled"}, {"rstp", "disabled"}, {"stp", "rstp"}, {"rstp", "stp"}, {"stp", "stp"}, {"rstp", "rstp"}} {
		t.Run(change.before+"_to_"+change.after+"_installs_and_compensates_exactly", func(t *testing.T) {
			setup(change.before)
			if change.before == "stp" && change.after == "stp" {
				vs("set", "Bridge", "br-witness", "other_config:stp-priority=0032768", "other_config:rstp-priority=0008192")
			}
			original := vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config")
			params := map[string]*string{}
			if change.after != "disabled" {
				priority := "4096"
				params[change.after+"-priority"] = &priority
			}
			p := prepare(envelope(change.after, params))
			o := observe(p, commit(p))
			if lost := e.Observe(ctx, p, execution.Outcome{Commit: "unknown", Applied: "unknown"}); lost.Commit != "committed" || lost.Applied == "applied" || lost.Target != nil {
				t.Fatal("lost reply invented a target", lost)
			}
			rollback, err := e.PrepareRollback(writeCtx, p, strings.Repeat("b", 64))
			must(t, err)
			observe(rollback, commit(rollback))
			if now := vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config"); now != original {
				t.Fatal("raw/unset compensation or unrelated map changed", original, now, o)
			}
		})
	}
	for _, fault := range []string{"role-revoked", "root-revoked", "field-conflict", "before-send-denied"} {
		t.Run(fault+"_rejects_without_dispatch", func(t *testing.T) {
			setup("stp")
			p := prepare(envelope("rstp", nil))
			before := vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config")
			defer grants()
			defer role(caps)
			switch fault {
			case "role-revoked":
				role(oldCaps)
			case "root-revoked":
				must(t, inv.SetLocalSpanningTreeExecutionBridges(nil))
			case "field-conflict":
				vs("set", "Bridge", "br-witness", "other_config:rstp-priority=8192")
				before = vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config")
			}
			calls := 0
			o := e.Commit(writeCtx, p, func() error {
				calls++
				if fault == "before-send-denied" {
					return errors.New("denied")
				}
				return nil
			})
			want := 0
			if fault == "before-send-denied" {
				want = 1
			}
			if calls != want || o.Commit != "rejected" || vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config") != before {
				t.Fatal("unsafe dispatch", fault, calls, o)
			}
		})
	}
	t.Run("persistent_ABA_during_witness_rejects_without_dispatch", func(t *testing.T) {
		setup("stp")
		in := envelope("rstp", nil).Candidate.Intents[0]
		before := vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config")
		calls := 0
		_, err := e.ReadSpanningTreeWitnessPreflight(ctx, in, control, build, func(c context.Context) error {
			if err := authorize(c); err != nil {
				return err
			}
			calls++
			// The third callback follows installation of the connection-scoped
			// monitor, before its first atomic fence. The image is restored, but
			// the monitor must still reject the intervening transactions.
			if calls == 3 {
				vs("set", "Bridge", "br-witness", "other_config:execution-opaque=changed")
				vs("set", "Bridge", "br-witness", "other_config:execution-opaque=preserve")
			}
			return nil
		})
		if err == nil || calls < 3 || vs("get", "Bridge", "br-witness", "stp_enable", "rstp_enable", "other_config") != before {
			t.Fatal("persistent ABA accepted or native original changed", err, calls)
		}
	})
	t.Run("native_rstp_creation_timer_order_rejects_before_write", func(t *testing.T) {
		setup("disabled")
		age, delay := "40", "21"
		_, err := e.Prepare(writeCtx, repository.NewID(), strings.Repeat("a", 64), envelope("rstp", map[string]*string{"rstp-max-age": &age, "rstp-forward-delay": &delay}))
		if err == nil {
			t.Fatal("native setter clamp admitted")
		}
	})
	t.Run("changed_after_image_refuses_compensation", func(t *testing.T) {
		setup("stp")
		p := prepare(envelope("rstp", nil))
		observe(p, commit(p))
		vs("set", "Bridge", "br-witness", "other_config:rstp-priority=8192")
		draft() // wait for an explicitly induced external-change observation
		before := vs("get", "Bridge", "br-witness", "other_config")
		if _, err := e.PrepareRollback(writeCtx, p, strings.Repeat("b", 64)); err == nil || vs("get", "Bridge", "br-witness", "other_config") != before {
			t.Fatal("overwrote external change", err)
		}
	})
}
