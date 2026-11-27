package capture_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/capture"
	"golang.org/x/sys/unix"
)

const (
	hostedCaptureEnv = "OVS_CAPTURE_HOSTED_FIXTURE"
	captureNS        = "OVS_CAPTURE_FIXTURE_NAMESPACE"
	captureReceiver  = "capture-rx0"
	captureSender    = "capture-tx0"
)

var fixtureNamespace = regexp.MustCompile(`^ovscap-[0-9a-f]{12}$`)

func requireHostedCaptureFixture(t *testing.T) {
	t.Helper()
	if os.Getenv(hostedCaptureEnv) != "1" {
		t.Skip("explicit isolated capture fixture on GitHub-hosted Ubuntu")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Geteuid() != 0 {
		t.Fatal("capture fixture requested without the hosted runner or fixture privileges")
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains("\n"+string(release), "\nID=ubuntu\n") ||
		!strings.Contains("\n"+string(release), "\nVERSION_ID=\"24.04\"\n") {
		t.Fatalf("unexpected hosted fixture OS: %v", err)
	}
}

func TestNativeCaptureInIsolatedNamespace(t *testing.T) {
	requireHostedCaptureFixture(t)
	if os.Getenv(captureNS) != "" {
		t.Fatal("the fixture parent cannot run inside a capture namespace")
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	namespace := "ovscap-" + hex.EncodeToString(random[:])
	nativeCaptureIP(t, "netns", "add", namespace)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, "ip", "netns", "del", namespace).CombinedOutput(); err != nil {
			t.Errorf("delete isolation fixture: %v, %s", err, output)
		}
	})
	nativeCaptureIP(t, "-n", namespace, "link", "add", captureReceiver, "type", "veth", "peer", "name", captureSender)
	nativeCaptureIP(t, "-n", namespace, "link", "set", captureReceiver, "address", "02:46:00:00:00:01", "up")
	nativeCaptureIP(t, "-n", namespace, "link", "set", captureSender, "address", "02:46:00:00:00:02", "up")

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "ip", "netns", "exec", namespace, executable,
		"-test.run", "^TestNativeCaptureFixtureChild$", "-test.v", "-test.timeout=20s")
	child.Env = append(os.Environ(), captureNS+"="+namespace)
	output, err := child.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("--- PASS: TestNativeCaptureFixtureChild")) {
		t.Fatalf("isolated packet-header fixture did not pass: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}

func TestNativeCaptureFixtureChild(t *testing.T) {
	namespace := os.Getenv(captureNS)
	if namespace == "" {
		t.Skip("capture child is entered only by the namespace fixture")
	}
	requireHostedCaptureFixture(t)
	if !fixtureNamespace.MatchString(namespace) {
		t.Fatal("invalid namespace fixture identity")
	}
	created, err := os.Stat(filepath.Join("/run/netns", namespace))
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat("/proc/self/ns/net")
	if err != nil || !os.SameFile(created, current) {
		t.Fatalf("child is outside the test network namespace: %v", err)
	}
	links, err := net.Interfaces()
	if err != nil || len(links) != 3 {
		t.Fatalf("fixture doesn't contain exactly loopback and the synthetic veth pair: %v", err)
	}
	receiver := nativeCaptureLink(t, captureReceiver, "02:46:00:00:00:01")
	sender := nativeCaptureLink(t, captureSender, "02:46:00:00:00:02")
	if _, err := net.InterfaceByName("lo"); err != nil {
		t.Fatal(err)
	}
	binding := capture.Binding{
		InterfaceID: "00000000-0000-4000-8000-000000000046",
		Generation:  "00000000-0000-4000-8000-000000000047",
		NativeUUID:  "00000000-0000-4000-8000-000000000048",
		IfIndex:     receiver.Index, Name: receiver.Name, KernelEpoch: 1,
	}

	for _, tc := range []struct {
		profile capture.Profile
		ether   uint16
	}{{capture.ARP, 0x0806}, {capture.LACP, 0x8809}, {capture.LLDP, 0x88cc}} {
		t.Run(string(tc.profile), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			pending := startNativeCapture(t, ctx, binding, tc.profile)
			putFixtureEthernet(t, sender, receiver, 0x0800)
			want := putFixtureEthernet(t, sender, receiver, tc.ether)
			putFixtureEthernet(t, sender, receiver, tc.ether)
			out := receivedNativeCapture(t, ctx, pending)
			if out.err != nil || out.result.State != "truncated" || out.result.Reason != "CAPTURE_RECORD_LIMIT" ||
				out.result.Packets != 1 || len(out.result.Bytes) != 54 {
				t.Fatalf("header capture = %+v, %v", out.result, out.err)
			}
			data := out.result.Bytes
			if binary.LittleEndian.Uint32(data[16:20]) != 14 || binary.LittleEndian.Uint32(data[32:36]) != 14 ||
				binary.LittleEndian.Uint32(data[36:40]) != uint32(len(want)) || !bytes.Equal(data[40:54], want[:14]) ||
				bytes.Contains(data, []byte("fixture-payload-omitted")) {
				t.Fatal("capture included a payload, nonmatching protocol, or invalid record framing")
			}
		})
	}

	t.Run("cancellation exports no capture", func(t *testing.T) {
		budget, finish := context.WithTimeout(t.Context(), 3*time.Second)
		defer finish()
		ctx, cancel := context.WithCancel(budget)
		defer cancel()
		pending := startNativeCapture(t, ctx, binding, capture.ARP)
		cancel()
		out := receivedNativeCapture(t, budget, pending)
		if !errors.Is(out.err, context.Canceled) || len(out.result.Bytes) != 0 {
			t.Fatalf("canceled capture = %+v, %v", out.result, out.err)
		}
	})
	t.Run("target link change exports no capture", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		pending := startNativeCapture(t, ctx, binding, capture.ARP)
		nativeCaptureIP(t, "link", "set", receiver.Name, "down")
		out := receivedNativeCapture(t, ctx, pending)
		var problem *apitypes.Problem
		if !errors.As(out.err, &problem) || problem.Code != "CAPTURE_INTERFACE_CHANGED" || len(out.result.Bytes) != 0 {
			t.Fatalf("changed-link capture = %+v, %v", out.result, out.err)
		}
	})
}

func nativeCaptureIP(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput(); err != nil {
		t.Fatalf("configure dedicated capture fixture: %v, %s", err, output)
	}
}

func nativeCaptureLink(t *testing.T, name, mac string) *net.Interface {
	t.Helper()
	link, err := net.InterfaceByName(name)
	if err != nil || link.HardwareAddr.String() != mac || link.Flags&net.FlagUp == 0 {
		t.Fatalf("unqualified synthetic fixture link %s: %v", name, err)
	}
	return link
}

type nativeCaptureResult struct {
	result capture.Result
	err    error
}

func startNativeCapture(t *testing.T, ctx context.Context, witness capture.Binding, profile capture.Profile) <-chan nativeCaptureResult {
	t.Helper()
	bound := make(chan struct{})
	done := make(chan nativeCaptureResult, 1)
	var calls atomic.Uint32
	resolve := func(_ context.Context, id string) (capture.Binding, error) {
		if id != witness.InterfaceID {
			return capture.Binding{}, errors.New("fixture requested an unrelated interface")
		}
		if calls.Add(1) == 2 {
			close(bound)
		}
		return witness, nil
	}
	go func() {
		result, err := capture.Observe(ctx, resolve, capture.Request{
			InterfaceID: witness.InterfaceID, Seconds: 10, MaxBytes: 54, Profile: profile,
		})
		done <- nativeCaptureResult{result, err}
	}()
	select {
	case <-bound:
	case early := <-done:
		t.Fatalf("native socket never bound: %v", early.err)
	case <-ctx.Done():
		t.Fatalf("waiting on bound synthetic capture socket: %v", ctx.Err())
	}
	return done
}

func receivedNativeCapture(t *testing.T, ctx context.Context, done <-chan nativeCaptureResult) nativeCaptureResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatalf("isolated capture did not end: %v", ctx.Err())
		return nativeCaptureResult{}
	}
}

// Send only fixed synthetic frame headers inside the verified veth namespace.
func putFixtureEthernet(t *testing.T, sender, receiver *net.Interface, protocol uint16) [64]byte {
	t.Helper()
	var frame [64]byte
	copy(frame[:6], receiver.HardwareAddr)
	copy(frame[6:12], sender.HardwareAddr)
	binary.BigEndian.PutUint16(frame[12:14], protocol)
	copy(frame[14:], []byte("fixture-payload-omitted"))
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := unix.Sendto(fd, frame[:], 0, &unix.SockaddrLinklayer{
		Ifindex: sender.Index, Protocol: protocol<<8 | protocol>>8,
	}); err != nil {
		t.Fatal(err)
	}
	return frame
}
