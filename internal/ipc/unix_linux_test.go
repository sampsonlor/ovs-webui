//go:build linux

package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type authFunc func(context.Context, string, Operation) error

func (f authFunc) Authorize(ctx context.Context, grant string, operation Operation) error {
	return f(ctx, grant, operation)
}

func testServer(t *testing.T, handler *Handler, peer uint32) (*Client, *http.Server) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mgrd.sock")
	listener, err := ListenUnix(SocketOptions{Path: path, OwnerUID: uint32(os.Geteuid()), GroupGID: uint32(os.Getegid()), PeerUID: peer})
	if err != nil {
		t.Fatal(err)
	}
	server := HTTPServer(handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); listener.Release() })
	client := NewClient(path, handler.protocol)
	client.expectedUID = uint32(os.Geteuid())
	return client, server
}

func TestUnixHandshakeAndPerOperationAuthorization(t *testing.T) {
	var deny atomic.Bool
	var calls atomic.Int32
	handler := NewHandler(CurrentProtocol("test-build"), authFunc(func(_ context.Context, grant string, op Operation) error {
		calls.Add(1)
		if grant != "synthetic-grant" || op.Name != "runtime.inspect" || op.Capability != "state.read" || op.Class != Read || deny.Load() {
			return ErrDenied
		}
		return nil
	}), nil)
	client, _ := testServer(t, handler, uint32(os.Geteuid()))
	if health, err := client.Probe(context.Background()); err != nil || health.ConfigurationReady || health.AuthenticationReady {
		t.Fatalf("bad transport health: %+v %v", health, err)
	}
	if calls.Load() != 0 {
		t.Fatal("transport health consulted business authority")
	}
	if _, err := client.Inspect(context.Background(), "synthetic-grant"); err != nil {
		t.Fatal(err)
	}
	deny.Store(true)
	_, err := client.Inspect(context.Background(), "synthetic-grant")
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Status != 403 || calls.Load() != 2 {
		t.Fatalf("did not reauthorize: %v calls=%d", err, calls.Load())
	}
	for _, modify := range []func(*Protocol){func(p *Protocol) { p.Major++ }, func(p *Protocol) { p.Minor++ }, func(p *Protocol) { p.Software = "older" }, func(p *Protocol) { p.Digest = "different" }} {
		other := *client
		modify(&other.protocol)
		_, err := other.Probe(context.Background())
		if !errors.As(err, &remote) || remote.Code != "IPC_VERSION_MISMATCH" {
			t.Fatalf("version gate bypassed: %v", err)
		}
	}
}

func TestPeerCredentialsCannotBeReplacedByHeaders(t *testing.T) {
	handler := NewHandler(CurrentProtocol("test-build"), nil, nil)
	client, _ := testServer(t, handler, uint32(os.Geteuid())+1)
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("wrong client UID accepted")
	}
	client, _ = testServer(t, handler, uint32(os.Geteuid()))
	client.expectedUID++
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("wrong manager UID accepted")
	}
}

func TestDefaultAuthorizerFailsClosed(t *testing.T) {
	client, _ := testServer(t, NewHandler(CurrentProtocol("test-build"), nil, nil), uint32(os.Geteuid()))
	_, err := client.Inspect(context.Background(), "synthetic-grant")
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "IPC_AUTH_UNAVAILABLE" {
		t.Fatal(err)
	}
}

func TestTypedFramingAndRedactedFailures(t *testing.T) {
	var logs bytes.Buffer
	handler := NewHandler(CurrentProtocol("test-build"), nil, slog.New(slog.NewJSONHandler(&logs, nil)))
	client, _ := testServer(t, handler, uint32(os.Geteuid()))
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", client.path)
	}}
	httpClient := &http.Client{Transport: transport, Timeout: time.Second}
	defer transport.CloseIdleConnections()
	valid, _ := json.Marshal(handler.protocol)
	for _, tc := range []struct {
		path, body, media string
		status            int
	}{
		{"/ipc/v1/operations/runtime.inspect", `{}`, "application/json", 409},
		{"/ipc/v1/operations/shell.exec", `{"command":"SECRET"}`, "application/json", 404},
		{"/ipc/v1/handshake", `{"role":"SECRET"}`, "application/json", 400},
		{"/ipc/v1/handshake", `{} {}`, "application/json", 400},
		{"/ipc/v1/handshake", string(valid), "text/plain", 415},
		{"/ipc/v1/handshake", strings.Repeat(" ", MaxBodyBytes+1), "application/json", 413},
	} {
		request, _ := http.NewRequest("POST", "http://mgrd"+tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", tc.media)
		request.Header.Set("X-Peer-UID", "0")
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Errorf("%s got %d want %d: %s", tc.path, response.StatusCode, tc.status, body)
		}
		if strings.Contains(string(body), "SECRET") {
			t.Fatal("response echoed secret")
		}
	}
	// All requests have completed before inspecting the shared log sink.
	if strings.Contains(logs.String(), "SECRET") {
		t.Fatal("logged attacker-controlled payload")
	}
}

func TestClientCancellationDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	observed := make(chan struct{})
	handler := NewHandler(CurrentProtocol("test-build"), authFunc(func(context.Context, string, Operation) error { calls.Add(1); return nil }), nil)
	handler.inspect = func(ctx context.Context) (Health, error) { <-ctx.Done(); close(observed); return Health{}, ctx.Err() }
	client, _ := testServer(t, handler, uint32(os.Geteuid()))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.Inspect(ctx, "synthetic-grant"); err == nil {
		t.Fatal("cancelled operation succeeded")
	}
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("server did not observe cancellation")
	}
	if calls.Load() != 1 {
		t.Fatal("operation was retried")
	}
}

func TestSocketOwnershipSingletonAndStaleRecovery(t *testing.T) {
	opts := SocketOptions{Path: filepath.Join(t.TempDir(), "mgrd.sock"), OwnerUID: uint32(os.Geteuid()), GroupGID: uint32(os.Getegid()), PeerUID: uint32(os.Geteuid())}
	first, err := ListenUnix(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(opts); err == nil {
		t.Fatal("second manager admitted")
	}
	_ = first.Close()
	if _, err := ListenUnix(opts); err == nil {
		t.Fatal("shutdown released singleton before drain")
	}
	first.Release()
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: opts.Path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := os.Chmod(opts.Path, 0660); err != nil {
		t.Fatal(err)
	}
	_ = stale.Close()
	second, err := ListenUnix(opts)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
	target := filepath.Join(filepath.Dir(opts.Path), "preserve.txt")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, opts.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(opts); err == nil {
		t.Fatal("symlink accepted")
	}
	if body, _ := os.ReadFile(target); string(body) != "preserve" {
		t.Fatal("unrelated file modified")
	}
}

func TestSlowHeaderIsBounded(t *testing.T) {
	client, server := testServer(t, NewHandler(CurrentProtocol("test-build"), nil, nil), uint32(os.Geteuid()))
	if server.ReadHeaderTimeout != HeaderTimeout || server.ReadTimeout != RequestTimeout || server.MaxHeaderBytes != MaxHeaderBytes {
		t.Fatal("HTTP bounds not installed")
	}
	conn, err := net.Dial("unix", client.path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(HeaderTimeout + time.Second))
	_, _ = io.WriteString(conn, "POST /ipc/v1/handshake HTTP/1.1\r\n")
	start := time.Now()
	one := make([]byte, 1)
	_, err = conn.Read(one)
	if err == nil || time.Since(start) > HeaderTimeout+500*time.Millisecond {
		t.Fatal("slow headers outlived server budget")
	}
}
