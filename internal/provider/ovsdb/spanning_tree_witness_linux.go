//go:build linux

package ovsdb

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
	"golang.org/x/sys/unix"
)

var witnessUnproven = errors.New("SPANNING_TREE_WITNESS_UNPROVEN")

// SpanningTreeWitnessBuild is an independent root-supplied build pin. There is
// no flag, public request, environment lookup or default manifest discovery.
// This experimental prerequisite does not qualify installation or execution.
type SpanningTreeWitnessBuild struct {
	Executable     string
	Manifest       string
	ManifestDigest string
}

type witnessBuildManifest struct {
	Format     string `json:"format"`
	Profile    string `json:"profile_sha256"`
	Arch       string `json:"architecture"`
	Variant    string `json:"variant"`
	Executable string `json:"executable_sha256"`
}

type SpanningTreeWitnessSample struct {
	Daemon        string
	ObservedAt    time.Time
	Witness       ovswitness.Sample
	buildIdentity string
	buildSchema   string
}

// SpanningTreeWitnessPair is transient read evidence, not a seal or a claim
// that the server still grants the IDL lock after either observation.
type SpanningTreeWitnessPair struct {
	First  SpanningTreeWitnessSample
	Second SpanningTreeWitnessSample
}

func witnessHex(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}

func witnessArchitecture(build, host string) bool {
	return (host == "amd64" || host == "arm64") && build == host
}

// Hash the opened file, never a pathname cache. Both work and bytes are bounded;
// an over-budget file or expired whole preflight fails rather than relaxing the
// two-second deadline. No content trust survives a call.
func witnessFileHash(ctx context.Context, path string, max int64, executable bool) (string, unix.Stat_t, error) {
	var empty unix.Stat_t
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", empty, witnessUnproven
	}
	f := os.NewFile(uintptr(fd), "witness-file")
	defer f.Close()
	var before, after, current unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Uid != 0 || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0022 != 0 || before.Size <= 0 || before.Size > max || executable && before.Mode&0111 == 0 {
		return "", empty, witnessUnproven
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var size int64
	for {
		if ctx.Err() != nil {
			return "", empty, witnessUnproven
		}
		n, err := f.Read(buf)
		size += int64(n)
		if size > max {
			return "", empty, witnessUnproven
		}
		_, _ = h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", empty, witnessUnproven
		}
	}
	if size != before.Size || unix.Fstat(fd, &after) != nil || unix.Stat(path, &current) != nil || !witnessSameFile(before, after) || !witnessSameFile(before, current) || ctx.Err() != nil {
		return "", empty, witnessUnproven
	}
	return hex.EncodeToString(h.Sum(nil)), before, nil
}

func witnessSameFile(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Size == b.Size && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func attestWitnessBuild(ctx context.Context, conn net.Conn, uid uint32, pin SpanningTreeWitnessBuild) (string, string, error) {
	if !filepath.IsAbs(pin.Executable) || !filepath.IsAbs(pin.Manifest) || strings.ContainsAny(pin.Executable+pin.Manifest, "\x00\r\n") || !witnessHex(pin.ManifestDigest) {
		return "", "", witnessUnproven
	}
	// Read the small pinned manifest from the same FD whose identity is checked.
	hash, identity, err := witnessFileHash(ctx, pin.Manifest, 4096, false)
	if err != nil || hash != pin.ManifestDigest {
		return "", "", witnessUnproven
	}
	fd, err := unix.Open(pin.Manifest, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", "", witnessUnproven
	}
	f := os.NewFile(uintptr(fd), "witness-manifest")
	defer f.Close()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || !witnessSameFile(identity, stat) {
		return "", "", witnessUnproven
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	actualHash := sha256.Sum256(data)
	var manifest witnessBuildManifest
	if err != nil || len(data) > 4096 || hex.EncodeToString(actualHash[:]) != pin.ManifestDigest || ipc.DecodeStrict(data, &manifest) != nil || manifest.Format != "ovs-webui.stp-build.v1" || !witnessArchitecture(manifest.Arch, runtime.GOARCH) || manifest.Variant != "enabled" && manifest.Variant != "sanitized" || !witnessHex(manifest.Executable) {
		return "", "", witnessUnproven
	}
	profile, ok := ovswitness.LookupBuildProfile(manifest.Profile)
	if !ok {
		return "", "", witnessUnproven
	}
	var after, named unix.Stat_t
	if unix.Fstat(fd, &after) != nil || unix.Stat(pin.Manifest, &named) != nil || !witnessSameFile(identity, after) || !witnessSameFile(identity, named) {
		return "", "", witnessUnproven
	}
	_, pid, err := peer(conn, uid)
	if err != nil {
		return "", "", witnessUnproven
	}
	exeHash, exe, err := witnessFileHash(ctx, fmt.Sprintf("/proc/%d/exe", pid), 128<<20, true)
	var configured unix.Stat_t
	if err != nil || exeHash != manifest.Executable || unix.Stat(pin.Executable, &configured) != nil || !witnessSameFile(exe, configured) {
		return "", "", witnessUnproven
	}
	// Bind both file identities, not just their digests, across the two requests.
	stable := func(s unix.Stat_t) string {
		return fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d:%d:%d", s.Dev, s.Ino, s.Size, s.Mode, s.Uid, s.Gid, s.Mtim.Sec, s.Mtim.Nsec, s.Ctim.Sec, s.Ctim.Nsec)
	}
	return hash + "/" + stable(identity) + "/" + exeHash + "/" + stable(exe), profile.Schema, nil
}

// ReadBoundSpanningTreeWitness has exactly one fixed UUID/nonce method. It
// retains the M2c2 actual DB socket-pair, file, schema and process checks, and
// checks current authorization before and after each source/build inspection.
// It has no retry, subprocess, discovery, cache, setter or lock request.
func ReadBoundSpanningTreeWitness(ctx context.Context, control SpanningTreeControl, database Options, expected inventory.Evidence, build SpanningTreeWitnessBuild, bridgeUUID string, authorize func(context.Context) error) (SpanningTreeWitnessSample, error) {
	empty := SpanningTreeWitnessSample{}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if authorize == nil {
		return empty, witnessUnproven
	}
	if err := authorize(ctx); err != nil {
		return empty, err
	}
	if validateOptions(database) != nil || control.ExpectedDaemon == "" || !apitypes.UUID(bridgeUUID) || bridgeUUID != strings.ToLower(bridgeUUID) || !filepath.IsAbs(control.Socket) || len(control.Socket) > 100 || strings.ContainsAny(control.Socket, "\x00\r\n") || control.PeerUID == ^uint32(0) {
		return empty, witnessUnproven
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", control.Socket)
	if err != nil {
		return empty, witnessUnproven
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return empty, witnessUnproven
	}
	var original, buildSchema string
	check := func() error {
		if err := authorize(ctx); err != nil {
			return err
		}
		daemon, err := spanningTreeDaemon(conn, control.PeerUID)
		if err != nil || daemon != control.ExpectedDaemon {
			return witnessUnproven
		}
		source, err := spanningTreeSource(ctx, conn, control.PeerUID, database, expected, build.Executable)
		if err != nil {
			return err
		}
		attestation, schema, err := attestWitnessBuild(ctx, conn, control.PeerUID, build)
		if err != nil {
			return err
		}
		identity := source + "/" + attestation
		if original != "" && original != identity {
			return witnessUnproven
		}
		original, buildSchema = identity, schema
		if err := authorize(ctx); err != nil {
			return err
		}
		current, err := spanningTreeDaemon(conn, control.PeerUID)
		if err != nil || current != daemon {
			return witnessUnproven
		}
		return ctx.Err()
	}
	if err = check(); err != nil {
		return empty, err
	}
	sample, receivedAt, err := requestSpanningTreeWitness(conn, bridgeUUID)
	if err != nil {
		return empty, err
	}
	if err = check(); err != nil {
		return empty, err
	}
	if sample.Database.RootUUID != expected.Root || ctx.Err() != nil {
		return empty, witnessUnproven
	}
	return SpanningTreeWitnessSample{Daemon: control.ExpectedDaemon, ObservedAt: receivedAt, Witness: sample, buildIdentity: original, buildSchema: buildSchema}, nil
}

func requestSpanningTreeWitness(conn net.Conn, bridgeUUID string) (ovswitness.Sample, time.Time, error) {
	var nonce [32]byte
	empty := ovswitness.Sample{}
	if _, err := rand.Read(nonce[:]); err != nil {
		return empty, time.Time{}, witnessUnproven
	}
	n := hex.EncodeToString(nonce[:])
	if json.NewEncoder(conn).Encode(map[string]any{"method": "ovs-webui/stp-witness-v1", "params": []string{bridgeUUID, n}, "id": 1}) != nil {
		return empty, time.Time{}, witnessUnproven
	}
	data, err := frame(bufio.NewReader(io.LimitReader(conn, ovswitness.MaxWireBytes+1)))
	receivedAt := time.Now().UTC()
	if err != nil {
		return empty, time.Time{}, witnessUnproven
	}
	sample, err := ovswitness.Decode(data, n, bridgeUUID)
	if err != nil {
		return empty, time.Time{}, err
	}
	return sample, receivedAt, nil
}

func sameWitnessPair(a, b SpanningTreeWitnessSample) bool {
	x, y := a.Witness, b.Witness
	if a.Daemon == "" || a.Daemon != b.Daemon || a.buildIdentity == "" || a.buildIdentity != b.buildIdentity || !witnessHex(a.buildSchema) || a.buildSchema != b.buildSchema || x.Nonce == y.Nonce || a.ObservedAt.IsZero() || b.ObservedAt.Before(a.ObservedAt) {
		return false
	}
	x.Nonce, y.Nonce = "", ""
	return reflect.DeepEqual(x, y)
}
