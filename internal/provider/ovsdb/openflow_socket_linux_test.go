package ovsdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"golang.org/x/sys/unix"
)

func TestOpenFlowUnix(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			listener, binding := flowTestUnixEndpoint(t)
			done := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				payload := append(flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(0, flowTestRow()))...)
				if _, err = conn.Write(payload); err == nil {
					_, err = io.Copy(io.Discard, conn)
				}
				done <- err
			}()
			calls := 0
			observer := NewOpenFlowObserver(func(_ context.Context, id string) (OpenFlowBinding, error) {
				calls++
				if id != binding.BridgeID {
					t.Fatal("resolver saw an unrelated Bridge")
				}
				out := binding
				if changed && calls == 2 {
					out.NativeBridgeUUID = out.InstanceGeneration
				}
				return out, nil
			})
			snapshot, err := observer.ObserveFlows(t.Context(), OpenFlowQuery{BridgeID: binding.BridgeID, Limit: 3})
			if changed {
				var problem *apitypes.Problem
				if !errors.As(err, &problem) || problem.Code != "OPENFLOW_IDENTITY_UNPROVEN" || len(snapshot.Rows) != 0 {
					t.Fatalf("native replacement escaped the fence: %+v, %v", snapshot, err)
				}
			} else if err != nil || len(snapshot.Rows) != 1 || snapshot.Ownership != "unknown" ||
				snapshot.Groups != "unavailable" || snapshot.Meters != "unavailable" || snapshot.ObservedAt.IsZero() {
				t.Fatalf("wrong authorized projection: %+v, %v", snapshot, err)
			}
			if calls != 2 {
				t.Fatalf("need exact pre/post witness, got %d calls", calls)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("isolated socket did not close")
			}
		})
	}
}

func TestOpenFlowUnixRecordedPathRejectsAliasAndWrongInode(t *testing.T) {
	_, binding := flowTestUnixEndpoint(t)
	if !openFlowSocketRecorded(binding) {
		t.Fatal("recorded local socket was rejected")
	}
	bad := binding
	bad.SocketInode++
	if openFlowSocketRecorded(bad) {
		t.Fatal("socket replaced with a different inode")
	}
	bad = binding
	bad.Socket = filepath.Join(filepath.Dir(binding.Socket), "alias.mgmt")
	if err := os.Symlink(binding.Socket, bad.Socket); err != nil {
		t.Fatal(err)
	}
	if openFlowSocketRecorded(bad) {
		t.Fatal("symlink substituted the recorded socket")
	}
}

func TestOpenFlowUnixVerifiesPeerBeforeSending(t *testing.T) {
	listener, binding := flowTestUnixEndpoint(t)
	binding.Daemon += "-incorrect"
	type read struct {
		n   int
		err error
	}
	reads := make(chan read, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			reads <- read{err: err}
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		n, err := conn.Read(b[:])
		reads <- read{n, err}
	}()
	observer := NewOpenFlowObserver(func(context.Context, string) (OpenFlowBinding, error) { return binding, nil })
	got, err := observer.ObserveFlows(t.Context(), OpenFlowQuery{BridgeID: binding.BridgeID, Limit: 1})
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Code != "OPENFLOW_IDENTITY_UNPROVEN" || len(got.Rows) != 0 {
		t.Fatalf("unproven peer returned evidence: %+v, %v", got, err)
	}
	select {
	case result := <-reads:
		if result.n != 0 || !errors.Is(result.err, io.EOF) {
			t.Fatalf("unproven socket was not closed before traffic: read %d bytes, %v", result.n, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unproven peer connection was not closed")
	}
}

func TestOpenFlowQualifiedInstallationRequiresExplicitEndpointAndBuild(t *testing.T) {
	options := QualifiedOpenFlowOptions{
		BridgeSockets: map[string]string{openFlowTestBridge: "/run/openvswitch/br.mgmt"},
		ControlSocket: "/run/openvswitch/ovs-vswitchd.ctl",
		Build: SpanningTreeWitnessBuild{
			Executable: "/usr/sbin/ovs-vswitchd", Manifest: "/etc/ovs-webui/manifest.json",
			ManifestDigest: strings.Repeat("a", 64),
		},
	}
	if !validOpenFlowInstall(options) {
		t.Fatal("explicit qualified installation unexpectedly rejected")
	}
	bad := options
	bad.Build.ManifestDigest = ""
	if validOpenFlowInstall(bad) {
		t.Fatal("unqualified native build accepted")
	}
	bad = options
	bad.BridgeSockets = map[string]string{openFlowTestBridge: "tcp:192.0.2.4:6633"}
	if validOpenFlowInstall(bad) {
		t.Fatal("external controller accepted as management source")
	}
	bad.BridgeSockets = map[string]string{
		openFlowTestBridge:                     "/run/openvswitch/br.mgmt",
		"34183630-17e5-4254-ba76-000000000044": "/run/openvswitch/br.mgmt",
	}
	if validOpenFlowInstall(bad) {
		t.Fatal("same endpoint accepted for independent Bridges")
	}
}

func flowTestUnixEndpoint(t *testing.T) (*net.UnixListener, OpenFlowBinding) {
	t.Helper()
	dir, err := os.MkdirTemp("", "of-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "br.mgmt")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var st unix.Stat_t
	if err := unix.Lstat(socket, &st); err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	proc, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	text := string(proc)
	fields := strings.Fields(text[strings.LastIndex(text, ")")+1:])
	if len(fields) < 20 {
		t.Fatal("proc stat unavailable")
	}
	return l, OpenFlowBinding{
		BridgeID: openFlowTestBridge, NativeBridgeUUID: "34183630-17e5-4254-ba76-000000000002",
		InstanceGeneration: "34183630-17e5-4254-ba76-000000000003", DatapathID: openFlowTestDPID,
		Socket: socket, SocketDevice: uint64(st.Dev), SocketInode: st.Ino, PeerUID: st.Uid,
		Daemon: fmt.Sprintf("%s:%d:%s", strings.TrimSpace(string(boot)), os.Getpid(), fields[19]),
	}
}
