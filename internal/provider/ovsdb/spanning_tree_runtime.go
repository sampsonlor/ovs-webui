package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

// SpanningTreeControl is private root-supplied transport configuration. There
// is intentionally no public endpoint, CLI flag, command name or generic RPC.
// A sample alone does not bind this daemon/name to an immutable OVSDB Bridge.
type SpanningTreeControl struct {
	Socket         string
	PeerUID        uint32
	ExpectedDaemon string
}

type SpanningTreeRuntimeSample struct {
	Daemon     string               `json:"daemon"`
	ObservedAt time.Time            `json:"observed_at"`
	Runtime    spanningtree.Runtime `json:"runtime"`
}

func spanningTreeDaemon(conn net.Conn, uid uint32) (string, error) {
	identity, _, err := peer(conn, uid)
	if err != nil {
		return "", err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	id := strings.TrimSpace(string(boot))
	if err != nil || !apitypes.UUID(id) {
		return "", errors.New("SPANNING_TREE_CONTROL_PEER_UNPROVEN")
	}
	return id + ":" + identity, nil
}

// ReadSpanningTreeRuntime sends exactly one of two read-only native methods,
// with exactly one Bridge argument. No shell, appctl process, retries, write
// verbs, echo handling or socket discovery are available. Failure returns an
// empty sample, never a configured/default/previously cached installed value.
func ReadSpanningTreeRuntime(ctx context.Context, o SpanningTreeControl, protocol, bridge string) (SpanningTreeRuntimeSample, error) {
	bad := errors.New("SPANNING_TREE_CONTROL_INVALID")
	if protocol != "stp" && protocol != "rstp" || !spanningtree.RuntimeName(bridge) || !filepath.IsAbs(o.Socket) || len(o.Socket) > 100 || strings.ContainsAny(o.Socket, "\x00\r\n") || o.PeerUID == ^uint32(0) {
		return SpanningTreeRuntimeSample{}, bad
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(bounded, "unix", o.Socket)
	if err != nil {
		return SpanningTreeRuntimeSample{}, errors.New("SPANNING_TREE_CONTROL_UNAVAILABLE")
	}
	defer conn.Close()
	closeOnCancel := context.AfterFunc(bounded, func() { _ = conn.Close() })
	defer closeOnCancel()
	deadline, _ := bounded.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return SpanningTreeRuntimeSample{}, bad
	}
	daemon, err := spanningTreeDaemon(conn, o.PeerUID)
	if err != nil || o.ExpectedDaemon != "" && daemon != o.ExpectedDaemon {
		return SpanningTreeRuntimeSample{}, errors.New("SPANNING_TREE_CONTROL_PEER_UNPROVEN")
	}
	if err = json.NewEncoder(conn).Encode(map[string]any{"method": protocol + "/show", "params": []string{bridge}, "id": 1}); err != nil {
		return SpanningTreeRuntimeSample{}, errors.New("SPANNING_TREE_CONTROL_UNAVAILABLE")
	}
	// LimitReader bounds the complete wire reply, including whitespace; the
	// existing strict JSON validator rejects duplicate keys, invalid UTF-8 and
	// pathological nesting. The text has an independent, smaller byte budget.
	r := bufio.NewReader(io.LimitReader(conn, 2*spanningtree.MaxRuntimeBytes+1))
	data, err := frame(r)
	if err != nil || len(data) > 2*spanningtree.MaxRuntimeBytes || validateJSON(data) != nil {
		return SpanningTreeRuntimeSample{}, errors.New("SPANNING_TREE_CONTROL_REPLY_INVALID")
	}
	var reply struct {
		ID     int             `json:"id"`
		Result *string         `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reply) != nil || reply.ID != 1 || reply.Result == nil || string(reply.Error) != "null" {
		return SpanningTreeRuntimeSample{}, errors.New("SPANNING_TREE_CONTROL_REPLY_INVALID")
	}
	runtime, err := spanningtree.ParseRuntime(protocol, bridge, *reply.Result)
	if err != nil {
		return SpanningTreeRuntimeSample{}, err
	}
	currentDaemon, err := spanningTreeDaemon(conn, o.PeerUID)
	if bounded.Err() != nil || err != nil || currentDaemon != daemon {
		return SpanningTreeRuntimeSample{}, errors.New("SPANNING_TREE_CONTROL_PEER_UNPROVEN")
	}
	return SpanningTreeRuntimeSample{Daemon: daemon, ObservedAt: time.Now().UTC(), Runtime: runtime}, nil
}
