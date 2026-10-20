//go:build linux

package ovsdb

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const controlRuntimeText = "---- br-tree ----\nRoot ID:\n  stp-priority 4096\n  stp-system-id 02:00:00:00:00:01\n  stp-hello-time 1s\n  stp-max-age 6s\n  stp-fwd-delay 4s\n  This bridge is the root\n\nBridge ID:\n  stp-priority 32768\n  stp-system-id 02:00:00:00:00:02\n  stp-hello-time 2s\n  stp-max-age 20s\n  stp-fwd-delay 15s\n\n  Interface Role State Cost Pri.Nbr\n  ---------- ---------- ---------- ----- -------\n\n"

func controlServer(t *testing.T, handle func(net.Conn)) SpanningTreeControl {
	t.Helper()
	// Unix socket paths have a small kernel limit independent of t.TempDir.
	dir, err := os.MkdirTemp("", "ovs-tree-")
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(dir, "ctl"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			handle(c)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close(); <-done; _ = os.RemoveAll(dir) })
	return SpanningTreeControl{Socket: filepath.Join(dir, "ctl"), PeerUID: uint32(os.Geteuid())}
}

func TestSpanningTreeControlOnlySendsFixedReadMethodsAndBindsDaemon(t *testing.T) {
	requests := make(chan map[string]any, 4)
	o := controlServer(t, func(c net.Conn) {
		var request map[string]any
		if json.NewDecoder(c).Decode(&request) != nil {
			return
		}
		requests <- request
		_ = json.NewEncoder(c).Encode(map[string]any{"id": 1, "error": nil, "result": controlRuntimeText})
	})
	for _, protocol := range []string{"stp", "rstp"} {
		sample, err := ReadSpanningTreeRuntime(context.Background(), o, protocol, "br-tree")
		if err != nil || sample.Daemon == "" || sample.ObservedAt.IsZero() || sample.Runtime.Local.Priority != 32768 {
			t.Fatal(sample, err)
		}
		request := <-requests
		if !reflect.DeepEqual(request, map[string]any{"id": float64(1), "method": protocol + "/show", "params": []any{"br-tree"}}) {
			t.Fatal("transport widened its fixed purpose", request)
		}
		o.ExpectedDaemon = sample.Daemon
	}
	o.ExpectedDaemon = "different-boot:pid:start"
	if sample, err := ReadSpanningTreeRuntime(context.Background(), o, "rstp", "br-tree"); err == nil || !reflect.DeepEqual(sample, SpanningTreeRuntimeSample{}) {
		t.Fatal("daemon replacement inherited original sample", sample, err)
	}
	if len(requests) != 0 {
		t.Fatal("mismatched daemon received a request")
	}
}

func TestSpanningTreeControlRejectsMalformedRepliesAndArguments(t *testing.T) {
	valid, _ := json.Marshal(map[string]any{"id": 1, "error": nil, "result": controlRuntimeText})
	for name, reply := range map[string]string{
		"wrong id":          strings.Replace(string(valid), `"id":1`, `"id":2`, 1),
		"duplicate id":      strings.Replace(string(valid), `"id":1`, `"id":1,"id":1`, 1),
		"error":             `{"id":1,"error":"No such RSTP object","result":null}`,
		"missing error":     `{"id":1,"result":""}`,
		"wrong result":      `{"id":1,"error":null,"result":{}}`,
		"notification":      `{"method":"echo","params":[],"id":null}`,
		"oversize":          `{"id":1,"error":null,"result":"` + strings.Repeat("x", 300000) + `"}`,
		"whitespace budget": strings.Repeat(" ", 300000),
		"unknown field":     strings.Replace(string(valid), `"id":1`, `"id":1,"method":"rstp/show"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			o := controlServer(t, func(c net.Conn) {
				var request json.RawMessage
				if json.NewDecoder(c).Decode(&request) == nil {
					_, _ = io.WriteString(c, reply)
				}
			})
			if sample, err := ReadSpanningTreeRuntime(context.Background(), o, "rstp", "br-tree"); err == nil || !reflect.DeepEqual(sample, SpanningTreeRuntimeSample{}) {
				t.Fatal("bad control reply became installed state", sample, err)
			}
		})
	}
	for _, protocol := range []string{"", "disabled", "stp/tcn", "rstp/show", "exit", "rstp\nexit"} {
		if _, err := ReadSpanningTreeRuntime(context.Background(), SpanningTreeControl{}, protocol, "br-tree"); err == nil {
			t.Fatal("unapproved method admitted", protocol)
		}
	}
}

func TestSpanningTreeControlPeerCancellationAndDeadlineFailClosed(t *testing.T) {
	o := controlServer(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	o.PeerUID++
	if _, err := ReadSpanningTreeRuntime(context.Background(), o, "rstp", "br-tree"); err == nil || err.Error() != "SPANNING_TREE_CONTROL_PEER_UNPROVEN" {
		t.Fatal("unexpected UID accepted", err)
	}
	o.PeerUID--
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if sample, err := ReadSpanningTreeRuntime(ctx, o, "rstp", "br-tree"); err == nil || !reflect.DeepEqual(sample, SpanningTreeRuntimeSample{}) || time.Since(start) > time.Second {
		t.Fatal("cancelled probe continued", sample, err)
	}
	start = time.Now()
	if sample, err := ReadSpanningTreeRuntime(context.Background(), o, "rstp", "br-tree"); err == nil || !reflect.DeepEqual(sample, SpanningTreeRuntimeSample{}) || time.Since(start) > 2800*time.Millisecond {
		t.Fatal("probe exceeded fixed two-second budget", sample, err)
	}
}
