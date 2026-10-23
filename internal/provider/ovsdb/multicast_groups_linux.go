//go:build linux

package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

type MulticastGroupOptions struct {
	ControlSocket, Executable, Manifest, ManifestDigest string
	PeerUID                                             uint32
}
type multicastGroupObserver struct {
	database Options
	options  MulticastGroupOptions
	busy     chan struct{}
}

// Optional root-pinned source only; absent/unqualified native UUID/name
// witnesses remain Unavailable. This constructor grants no write authority.
func (p *Provider) MulticastGroupObserver(o MulticastGroupOptions) (inventory.MulticastGroupObserver, error) {
	if p == nil || !filepath.IsAbs(o.ControlSocket) || len(o.ControlSocket) > 100 || !filepath.IsAbs(o.Executable) || !filepath.IsAbs(o.Manifest) || strings.ContainsAny(o.ControlSocket+o.Executable+o.Manifest, "\x00\r\n") || !witnessHex(o.ManifestDigest) || o.PeerUID == ^uint32(0) {
		return nil, errors.New("INVALID_MULTICAST_GROUP_SOURCE")
	}
	return &multicastGroupObserver{database: p.options, options: o, busy: make(chan struct{}, 1)}, nil
}

func (p *multicastGroupObserver) ObserveMulticast(ctx context.Context, req inventory.MulticastGroupRequest) (inventory.MulticastGroupSample, error) {
	bad := errors.New("MULTICAST_GROUP_OBSERVATION_UNPROVEN")
	select {
	case p.busy <- struct{}{}:
		defer func() { <-p.busy }()
	default:
		return inventory.MulticastGroupSample{}, bad
	}
	if !spanningtree.RuntimeName(req.Name) || req.Bridge.Table != "Bridge" || req.Bridge.State != "active" {
		return inventory.MulticastGroupSample{}, bad
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ctx, release := treeSourceContext(ctx)
	defer release()
	o := p.options
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", o.ControlSocket)
	if err != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	defer conn.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer closeOnCancel()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	daemon, err := spanningTreeDaemon(conn, o.PeerUID)
	if err != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	control := SpanningTreeControl{Socket: o.ControlSocket, PeerUID: o.PeerUID, ExpectedDaemon: daemon}
	build := SpanningTreeWitnessBuild{Executable: o.Executable, Manifest: o.Manifest, ManifestDigest: o.ManifestDigest}
	first, err := ReadBoundSpanningTreeWitness(ctx, control, p.database, req.Evidence, build, req.Bridge.UUID, func(c context.Context) error { return c.Err() })
	if err != nil || first.Witness.Bridge.Name != req.Name || ovswitness.Installed(first.Witness) != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	if !multicastBuildSchema(ctx, p.database, req.Evidence, first.buildSchema) {
		return inventory.MulticastGroupSample{}, bad
	}
	if _, err = spanningTreeSource(ctx, conn, o.PeerUID, p.database, req.Evidence, o.Executable); err != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	// Exactly one fixed read-only method and a confirmed native Bridge name.
	// There is no subprocess, shell, write RPC, retry or client-selected verb.
	if json.NewEncoder(conn).Encode(map[string]any{"method": "mdb/show", "params": []string{req.Name}, "id": 1}) != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	data, err := frame(bufio.NewReader(io.LimitReader(conn, 2*multicast.MaxGroupWire+1)))
	at := time.Now().UTC()
	if err != nil || len(data) > 2*multicast.MaxGroupWire || validateJSON(data) != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	var reply struct {
		ID     int             `json:"id"`
		Result *string         `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reply) != nil || reply.ID != 1 || reply.Result == nil || string(reply.Error) != "null" {
		return inventory.MulticastGroupSample{}, bad
	}
	groups, err := multicast.DecodeGroups([]byte(*reply.Result))
	if err != nil {
		return inventory.MulticastGroupSample{}, err
	}
	second, err := ReadBoundSpanningTreeWitness(ctx, control, p.database, req.Evidence, build, req.Bridge.UUID, func(c context.Context) error { return c.Err() })
	if err != nil || second.Daemon != first.Daemon || second.buildIdentity != first.buildIdentity || second.buildSchema != first.buildSchema || !sameMulticastWitness(first.Witness, second.Witness) {
		return inventory.MulticastGroupSample{}, bad
	}
	current, err := spanningTreeDaemon(conn, o.PeerUID)
	if err != nil || current != daemon || ctx.Err() != nil {
		return inventory.MulticastGroupSample{}, bad
	}
	return inventory.MulticastGroupSample{ObservedAt: at, Groups: groups}, nil
}

func sameMulticastWitness(a, b ovswitness.Sample) bool {
	// Nonce and wall time are deliberately different; the daemon incarnation,
	// UUID/name binding and installed configuration/IDL epochs must be equal.
	return a.Daemon == b.Daemon && inventory.Digest(a.Database) == inventory.Digest(b.Database) && a.Bridge == b.Bridge && inventory.Digest(a.Configuration) == inventory.Digest(b.Configuration) && inventory.Digest(a.Native) == inventory.Digest(b.Native)
}

// The attested binary's compiled schema must match the actual DB schema, not
// merely the raw inventory digest or a same-name configured database.
func multicastBuildSchema(ctx context.Context, o Options, expected inventory.Evidence, buildSchema string) bool {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", o.Socket)
	if err != nil {
		return false
	}
	if retainTreeSourceConnection(ctx, conn) != nil {
		return false
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, ok := ctx.Deadline()
	if !ok || conn.SetDeadline(deadline) != nil {
		return false
	}
	identity, _, err := peer(conn, o.PeerUID)
	if err != nil || identity != expected.Peer {
		return false
	}
	data, err := call(conn, bufio.NewReader(conn), 1, "get_schema", []any{"Open_vSwitch"})
	if err != nil {
		return false
	}
	d, err := discover(data)
	return err == nil && d.public.Digest == expected.Schema && inventory.Digest(d.native) == buildSchema && ctx.Err() == nil
}
