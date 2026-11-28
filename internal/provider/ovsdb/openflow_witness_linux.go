package ovsdb

import (
	"context"
	"errors"
	"maps"
	"net"
	"path/filepath"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
)

// QualifiedOpenFlowOptions is trusted root configuration. BridgeSockets ties
// each allowed management Bridge ID to one explicitly recorded canonical
// service listener. The build must qualify the repository's installed Bridge
// witness. A stock daemon or a missing endpoint has no binding fallback.
type QualifiedOpenFlowOptions struct {
	BridgeSockets map[string]string
	ControlSocket string
	PeerUID       uint32
	Build         SpanningTreeWitnessBuild
}

func (p *Provider) QualifiedOpenFlowObserver(service *inventory.Service, options QualifiedOpenFlowOptions) (*OpenFlowObserver, error) {
	if p == nil || service == nil || validateOptions(p.options) != nil || !validOpenFlowInstall(options) {
		return nil, errors.New("OPENFLOW_WITNESS_CONFIGURATION_INVALID")
	}
	options.BridgeSockets = maps.Clone(options.BridgeSockets)
	return NewOpenFlowObserver(func(ctx context.Context, bridgeID string) (OpenFlowBinding, error) {
		return openFlowInstalledBinding(ctx, p.options, service, options, bridgeID)
	}), nil
}

func validOpenFlowInstall(o QualifiedOpenFlowOptions) bool {
	if len(o.BridgeSockets) < 1 || len(o.BridgeSockets) > 128 || o.PeerUID == ^uint32(0) ||
		!filepath.IsAbs(o.ControlSocket) || filepath.Clean(o.ControlSocket) != o.ControlSocket || len(o.ControlSocket) > 100 ||
		!filepath.IsAbs(o.Build.Executable) || !filepath.IsAbs(o.Build.Manifest) || !witnessHex(o.Build.ManifestDigest) ||
		strings.ContainsAny(o.ControlSocket+o.Build.Executable+o.Build.Manifest, "\x00\r\n") {
		return false
	}
	paths := make(map[string]bool, len(o.BridgeSockets))
	for id, path := range o.BridgeSockets {
		if !apitypes.ManagementID(id) || !validOpenFlowSocketPath(path) || paths[path] || path == o.ControlSocket {
			return false
		}
		paths[path] = true
	}
	return true
}

func openFlowInstalledBinding(ctx context.Context, database Options, service *inventory.Service, o QualifiedOpenFlowOptions, id string) (OpenFlowBinding, error) {
	var empty OpenFlowBinding
	path, configured := o.BridgeSockets[id]
	if !configured {
		return empty, errOpenFlowNoEndpoint
	}
	before, err := service.OpenFlowSource(ctx, id)
	if err != nil {
		var problem *apitypes.Problem
		if errors.As(err, &problem) && problem.Code == "OPENFLOW_DPID_UNOBSERVED" {
			return empty, errOpenFlowNoDPID
		}
		return empty, err
	}
	if filepath.Base(path) != before.Name+".mgmt" {
		return empty, errOpenFlowIdentity
	}
	device, inode, ok := openFlowSocketRecord(path, o.PeerUID)
	if !ok {
		return empty, errOpenFlowIdentity
	}
	ctx, release := treeSourceContext(ctx)
	defer release()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", o.ControlSocket)
	if err != nil {
		return empty, err
	}
	defer conn.Close()
	daemon, err := spanningTreeDaemon(conn, o.PeerUID)
	if err != nil {
		return empty, errOpenFlowIdentity
	}
	control := SpanningTreeControl{Socket: o.ControlSocket, PeerUID: o.PeerUID, ExpectedDaemon: daemon}
	w, err := ReadBoundSpanningTreeWitness(ctx, control, database, before.Evidence, o.Build, before.Bridge.UUID,
		func(c context.Context) error { return c.Err() })
	if err != nil || w.Daemon != daemon || w.Witness.Bridge.Name != before.Name ||
		w.Witness.Bridge.UUID != before.Bridge.UUID || ovswitness.Installed(w.Witness) != nil ||
		!nativeWitnessBuildSchema(ctx, database, before.Evidence, w.buildSchema) {
		return empty, errOpenFlowIdentity
	}
	after, err := service.OpenFlowSource(ctx, id)
	if err != nil || openFlowInventoryAuthority(before) != openFlowInventoryAuthority(after) {
		return empty, errOpenFlowIdentity
	}
	binding := OpenFlowBinding{
		BridgeID: id, NativeBridgeUUID: before.Bridge.UUID, InstanceGeneration: before.Generation,
		DatapathID: before.DatapathID, Socket: path, SocketDevice: device, SocketInode: inode,
		PeerUID: o.PeerUID, Daemon: daemon,
		installedProof: inventory.Digest([]any{openFlowInventoryAuthority(after), w.Witness.Bridge, w.buildIdentity, w.buildSchema}),
	}
	if !openFlowSocketRecorded(binding) || ctx.Err() != nil {
		return empty, errOpenFlowIdentity
	}
	return binding, nil
}

func openFlowInventoryAuthority(v inventory.OpenFlowBridgeSource) string {
	e := v.Evidence
	return inventory.Digest([]any{v.Bridge, v.Generation, v.Name, v.DatapathID, e.Endpoint, e.Database,
		e.Root, e.Schema, e.Peer, e.Boot, e.File.Device, e.File.Inode})
}
