//go:build linux

package ovsdb

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func TestSpanningTreeRootProbeIsFixedReadOnlyAndKeepsDiscoveryClosed(t *testing.T) {
	const root = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"valid", `{"id":2,"error":null,"result":[{"rows":[{"_uuid":["uuid","` + root + `"]}]}]}`, true},
		{"missing", `{"id":2,"error":null,"result":[{"rows":[]}]}`, false},
		{"error", `{"id":2,"error":null,"result":[{"rows":[{"_uuid":["uuid","` + root + `"]}],"error":"rejected"}]}`, false},
		{"wrong-id", `{"id":3,"error":null,"result":[{"rows":[{"_uuid":["uuid","` + root + `"]}]}]}`, false},
		{"wrong-root", `{"id":2,"error":null,"result":[{"rows":[{"_uuid":["uuid","22222222-2222-4222-8222-222222222222"]}]}]}`, false},
		{"extra-root", `{"id":2,"error":null,"result":[{"rows":[{"_uuid":["uuid","` + root + `"]},{"_uuid":["uuid","` + root + `"]}]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			requests := make(chan map[string]any, 1)
			go func() {
				defer server.Close()
				_ = server.SetDeadline(time.Now().Add(time.Second))
				var q map[string]any
				if json.NewDecoder(server).Decode(&q) == nil {
					requests <- q
					_, _ = server.Write([]byte(tc.reply + "\n"))
				}
			}()
			err := spanningTreeRoot(client, bufio.NewReader(client), root)
			if (err == nil) != tc.valid {
				t.Fatal("root result misclassified", err)
			}
			q := <-requests
			want := map[string]any{"id": float64(2), "method": "transact", "params": []any{"Open_vSwitch", map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{}, "columns": []any{"_uuid"}}}}
			if !reflect.DeepEqual(q, want) {
				t.Fatal("root probe accepted a general transaction", q)
			}
			if denied := request(client, 4, "transact", nil); denied == nil || denied.Error() != "OVSDB_METHOD_DENIED" {
				t.Fatal("discovery method allowlist broadened")
			}
		})
	}
}

func diagPacket(kind uint16, body []byte) []byte {
	b := make([]byte, 16+len(body))
	binary.NativeEndian.PutUint32(b, uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:], kind)
	binary.NativeEndian.PutUint32(b[8:], 1)
	copy(b[16:], body)
	return b
}

func TestSpanningTreeSourceDiagRejectsMalformedAndInterruptedEvidence(t *testing.T) {
	body := make([]byte, 24)
	body[0], body[1], body[2] = unix.AF_UNIX, unix.SOCK_STREAM, 1
	binary.NativeEndian.PutUint32(body[4:], 11)
	binary.NativeEndian.PutUint16(body[16:], 8)
	binary.NativeEndian.PutUint16(body[18:], 2)
	binary.NativeEndian.PutUint32(body[20:], 12)
	valid := diagPacket(20, body)
	done := diagPacket(unix.NLMSG_DONE, make([]byte, 4))
	out := map[uint32]uint32{}
	if complete, err := parseUnixPeers(append(append([]byte{}, valid...), done...), out); err != nil || !complete || !reflect.DeepEqual(out, map[uint32]uint32{11: 12}) {
		t.Fatal(out, complete, err)
	}
	for _, scenario := range []string{"header", "length", "sequence", "interrupted", "kernel-error", "done-error", "truncated-body", "attribute", "duplicate-attribute", "duplicate-inode", "zero-peer", "after-done"} {
		t.Run(scenario, func(t *testing.T) {
			b := append([]byte{}, valid...)
			switch scenario {
			case "header":
				b = b[:7]
			case "length":
				binary.NativeEndian.PutUint32(b, 100)
			case "sequence":
				binary.NativeEndian.PutUint32(b[8:], 2)
			case "interrupted":
				binary.NativeEndian.PutUint16(b[6:], unix.NLM_F_DUMP_INTR)
			case "kernel-error":
				b = diagPacket(unix.NLMSG_ERROR, make([]byte, 4))
			case "done-error":
				b = append([]byte{}, done...)
				b[16] = 1
			case "truncated-body":
				b = diagPacket(20, body[:8])
			case "attribute":
				binary.NativeEndian.PutUint16(b[32:], 9)
			case "duplicate-attribute":
				b = diagPacket(20, append(append([]byte{}, body...), body[16:]...))
			case "duplicate-inode":
				b = append(b, valid...)
			case "zero-peer":
				binary.NativeEndian.PutUint32(b[36:], 0)
			case "after-done":
				b = append(append([]byte{}, done...), valid...)
			}
			if _, err := parseUnixPeers(b, map[uint32]uint32{}); err == nil {
				t.Fatal("malformed evidence accepted")
			}
		})
	}
}

func TestSpanningTreeSourceDiagObservesActualConnectedUnixPeers(t *testing.T) {
	dir, err := os.MkdirTemp("", "ovs-diag-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.Dial("unix", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	inode := func(c net.Conn) uint32 {
		raw, err := c.(*net.UnixConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var st unix.Stat_t
		if err = raw.Control(func(fd uintptr) { err = unix.Fstat(int(fd), &st) }); err != nil {
			t.Fatal(err)
		}
		return uint32(st.Ino)
	}
	a, b := inode(c), inode(s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	peers, err := unixSocketPeers(ctx)
	if err != nil || peers[a] != b || peers[b] != a {
		t.Fatal("kernel peer relation missing", a, b, peers[a], peers[b], err)
	}
	cancel()
	if _, err := unixSocketPeers(ctx); err == nil {
		t.Fatal("cancelled dump accepted")
	}
}

func TestSpanningTreeBoundSourceRequiresAdmissionBeforeAnyProviderAccess(t *testing.T) {
	t.Run("own_probe_connections_survive_nested_checks_and_close_with_attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx, release := treeSourceContext(ctx)
		defer release()
		nested, finish := treeSourceContext(ctx)
		client, server := net.Pipe()
		defer server.Close()
		if err := retainTreeSourceConnection(nested, client); err != nil {
			t.Fatal(err)
		}
		finish()
		_ = server.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
		if _, err := server.Read(make([]byte, 1)); err == nil || errors.Is(err, io.EOF) {
			t.Fatal("nested inspection closed a held probe", err)
		}
		cancel()
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := server.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatal("cancelled attempt retained probe", err)
		}
		release()
	})
	t.Run("own_probe_budget_is_fixed_and_failed_admission_closes_socket", func(t *testing.T) {
		ctx, release := treeSourceContext(context.Background())
		defer release()
		for n := 0; n < 13; n++ {
			client, server := net.Pipe()
			defer server.Close()
			err := retainTreeSourceConnection(ctx, client)
			if (err == nil) != (n < 12) {
				t.Fatal("probe budget changed", n, err)
			}
			if n == 12 {
				if _, err := server.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
					t.Fatal("over-budget probe retained", err)
				}
			}
		}
	})
	denied := errors.New("current permission denied")
	calls := 0
	check := func(context.Context) error { calls++; return denied }
	got, err := ReadBoundSpanningTreeRuntime(context.Background(), SpanningTreeControl{}, Options{}, inventory.Evidence{}, "", "stp", "br-tree", check)
	if !errors.Is(err, denied) || calls != 1 || !reflect.DeepEqual(got, SpanningTreeRuntimeSample{}) {
		t.Fatal("provider/configuration oracle preceded authorization", got, err, calls)
	}
	if _, err := ReadBoundSpanningTreeRuntime(context.Background(), SpanningTreeControl{}, Options{}, inventory.Evidence{}, "", "stp", "br-tree", nil); err == nil {
		t.Fatal("missing authorizer accepted")
	}
	if _, err := processSocketInodes(-1); err == nil {
		t.Fatal("unknown process accepted")
	}
	// Source verification after the native reply must never refresh its age.
	o := controlServer(t, func(c net.Conn) {
		var request map[string]any
		if json.NewDecoder(c).Decode(&request) == nil {
			_ = json.NewEncoder(c).Encode(map[string]any{"id": 1, "error": nil, "result": controlRuntimeText})
		}
	})
	var afterRead time.Time
	checks := 0
	sample, err := readSpanningTreeRuntime(context.Background(), o, "stp", "br-tree", func(context.Context, net.Conn) error {
		checks++
		if checks == 2 {
			afterRead = time.Now().UTC()
		}
		return nil
	})
	if err != nil || checks != 2 || sample.ObservedAt.IsZero() || sample.ObservedAt.After(afterRead) {
		t.Fatal("post-read attestation refreshed the runtime sample timestamp", checks, sample, err)
	}
}
