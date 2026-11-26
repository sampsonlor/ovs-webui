//go:build linux

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
	"runtime"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

const maxFDBWire = 160 << 10

type fdbObserver struct {
	database Options
	control  SpanningTreeControl
	build    SpanningTreeWitnessBuild
}

func (p *Provider) FDBObserver(socket string, uid uint32, build SpanningTreeWitnessBuild) (inventory.FDBObserver, error) {
	if p == nil || !filepath.IsAbs(socket) || len(socket) > 100 || !filepath.IsAbs(build.Executable) || !filepath.IsAbs(build.Manifest) ||
		strings.ContainsAny(socket+build.Executable+build.Manifest, "\x00\r\n") || !witnessHex(build.ManifestDigest) || uid == ^uint32(0) {
		return nil, errors.New("INVALID_FDB_SOURCE")
	}
	return &fdbObserver{database: p.options, control: SpanningTreeControl{Socket: socket, PeerUID: uid}, build: build}, nil
}

func (p *fdbObserver) ObserveFDB(ctx context.Context, req inventory.FDBRequest) (inventory.FDBSample, error) {
	bad := apitypes.Fail(503, "FDB_SOURCE_INVALID")
	if !spanningtree.RuntimeName(req.Name) || req.Bridge.Table != "Bridge" || req.Bridge.State != "active" || !apitypes.UUID(req.Bridge.UUID) {
		return inventory.FDBSample{}, bad
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ctx, release := treeSourceContext(ctx)
	defer release()

	control := p.control
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", control.Socket)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return inventory.FDBSample{}, apitypes.Fail(503, "FDB_SOURCE_PERMISSION")
		}
		return inventory.FDBSample{}, apitypes.Fail(503, "FDB_SOURCE_UNAVAILABLE")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return inventory.FDBSample{}, bad
	}
	daemon, err := spanningTreeDaemon(conn, control.PeerUID)
	if err != nil {
		return inventory.FDBSample{}, bad
	}
	control.ExpectedDaemon = daemon

	unsupported := false
	attest := func(ctx context.Context, wire net.Conn, uid uint32, build SpanningTreeWitnessBuild) (string, string, error) {
		return attestNativeBuild(ctx, wire, uid, build, func(m witnessBuildManifest) (string, bool) {
			profile, supported := ovswitness.LookupFDBBuildProfile(m.Profile)
			unsupported = !supported
			return profile.Schema, supported && m.Format == "ovs-webui.stp-build.v1" && witnessArchitecture(m.Arch, runtime.GOARCH) &&
				(m.Variant == "enabled" || m.Variant == "sanitized") && witnessHex(m.Executable)
		})
	}
	authorize := func(c context.Context) error { return c.Err() }
	first, err := readBoundSpanningTreeWitness(ctx, control, p.database, req.Evidence, p.build, req.Bridge.UUID, authorize, attest)
	if err != nil || first.Witness.Bridge.Name != req.Name || ovswitness.Installed(first.Witness) != nil {
		if unsupported {
			return inventory.FDBSample{}, apitypes.Fail(503, "FDB_SOURCE_UNSUPPORTED")
		}
		return inventory.FDBSample{}, bad
	}
	if !nativeWitnessBuildSchema(ctx, p.database, req.Evidence, first.buildSchema) {
		return inventory.FDBSample{}, bad
	}
	source, err := spanningTreeSource(ctx, conn, control.PeerUID, p.database, req.Evidence, p.build.Executable)
	if err != nil || !strings.HasPrefix(first.buildIdentity, source+"/") {
		return inventory.FDBSample{}, bad
	}
	// The qualified command accepts exactly one installed Bridge identity. Its
	// kernel-backed provider work is capped inside OVS before serialization.
	if json.NewEncoder(conn).Encode(map[string]any{"method": "ovs-webui/fdb-sample-v1", "params": []string{req.Bridge.UUID, req.Name}, "id": 1}) != nil {
		return inventory.FDBSample{}, bad
	}
	data, err := frame(bufio.NewReader(io.LimitReader(conn, maxFDBWire+1)))
	at := time.Now().UTC()
	if err != nil || len(data) > maxFDBWire {
		return inventory.FDBSample{}, bad
	}
	sample, err := decodeFDBReply(data, req, at)
	if err != nil {
		return inventory.FDBSample{}, err
	}
	second, err := readBoundSpanningTreeWitness(ctx, control, p.database, req.Evidence, p.build, req.Bridge.UUID, authorize, attest)
	if err != nil || second.Daemon != first.Daemon || second.buildIdentity != first.buildIdentity || second.buildSchema != first.buildSchema ||
		!sameInstalledWitness(first.Witness, second.Witness) {
		return inventory.FDBSample{}, bad
	}
	current, err := spanningTreeDaemon(conn, control.PeerUID)
	if err != nil || current != daemon || ctx.Err() != nil {
		return inventory.FDBSample{}, bad
	}
	return sample, nil
}

func decodeFDBReply(data []byte, req inventory.FDBRequest, at time.Time) (inventory.FDBSample, error) {
	bad := apitypes.Fail(503, "FDB_SOURCE_INVALID")
	var envelope struct {
		ID     int             `json:"id"`
		Result *string         `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if len(data) > maxFDBWire || ipc.DecodeStrict(data, &envelope) != nil || envelope.ID != 1 {
		return inventory.FDBSample{}, bad
	}
	if envelope.Result == nil {
		var code string
		if json.Unmarshal(envelope.Error, &code) == nil && strings.TrimSpace(code) == "FDB_SOURCE_UNAVAILABLE" {
			return inventory.FDBSample{}, apitypes.Fail(503, "FDB_SOURCE_UNAVAILABLE")
		}
		return inventory.FDBSample{}, bad
	}
	if string(envelope.Error) != "null" {
		return inventory.FDBSample{}, bad
	}
	var reply struct {
		Version int     `json:"version"`
		UUID    string  `json:"bridge_uuid"`
		Name    string  `json:"bridge_name"`
		Total   *uint32 `json:"total"`
		Rows    []struct {
			MAC        string          `json:"mac"`
			VLAN       *uint16         `json:"vlan"`
			OFPort     json.RawMessage `json:"ofport"`
			PortName   json.RawMessage `json:"port_name"`
			AgeSeconds json.RawMessage `json:"age_seconds"`
		} `json:"rows"`
	}
	if ipc.DecodeStrict([]byte(*envelope.Result), &reply) != nil || reply.Version != 1 ||
		reply.UUID != req.Bridge.UUID || reply.Name != req.Name || reply.Total == nil ||
		reply.Rows == nil || int64(len(reply.Rows)) != min(int64(*reply.Total), inventory.MaxFDBRows) {
		return inventory.FDBSample{}, bad
	}
	rows := make([]inventory.FDBNativeRow, 0, len(reply.Rows))
	for _, native := range reply.Rows {
		row := inventory.FDBNativeRow{MAC: native.MAC, VLAN: native.VLAN}
		// Explicit null is native evidence. An omitted or malformed age, number
		// or name cannot be reinterpreted as static or unmapped.
		if json.Unmarshal(native.OFPort, &row.OFPort) != nil || json.Unmarshal(native.PortName, &row.PortName) != nil ||
			json.Unmarshal(native.AgeSeconds, &row.AgeSeconds) != nil || row.VLAN == nil {
			return inventory.FDBSample{}, bad
		}
		if row.PortName != nil {
			name := *row.PortName
			if len(name) == 0 || len(name) > 64 || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r > '~' }) {
				return inventory.FDBSample{}, bad
			}
		}
		rows = append(rows, row)
	}
	return inventory.FDBSample{ObservedAt: at, Total: *reply.Total, Rows: rows}, nil
}
