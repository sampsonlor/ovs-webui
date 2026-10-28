//go:build linux

package ovsdb

import (
	"context"
	"errors"
	"net"
	"runtime"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
)

// No control RPC is sent. SO_PEERCRED binds the opened control connection to
// the root-owned executable. Admission/dispatch/Applied additionally prove its
// actual established connection to this exact observed DB/file/root/schema.
// No trust cache or fallback to an executable name/version is used.
func (p *Provider) mirrorRuntime(ctx context.Context, o *MirrorExecutionOptions, evidence inventory.Evidence, source bool) (string, error) {
	if o == nil {
		return "", errors.New("MIRROR_RUNTIME_UNQUALIFIED")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ctx, release := treeSourceContext(ctx)
	defer release()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", o.ControlSocket)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return "", witnessUnproven
	}
	daemon, _, err := peer(conn, o.PeerUID)
	if err != nil {
		return "", err
	}
	build := SpanningTreeWitnessBuild{Executable: o.Executable, Manifest: o.Manifest, ManifestDigest: o.ManifestDigest}
	qualify := func(m witnessBuildManifest) (string, bool) {
		return evidence.Schema, m.Format == "ovs-webui.mirror-build.v1" && witnessArchitecture(m.Arch, runtime.GOARCH) && witnessHex(m.Executable) && mirror.LookupBuildProfile(m.Profile, evidence.Schema, m.Variant)
	}
	a, _, err := attestNativeBuild(ctx, conn, o.PeerUID, build, qualify)
	if err != nil {
		return "", err
	}
	association := ""
	if source {
		association, err = spanningTreeSource(ctx, conn, o.PeerUID, p.options, evidence, o.Executable)
		if err != nil {
			return "", err
		}
	}
	b, _, err := attestNativeBuild(ctx, conn, o.PeerUID, build, qualify)
	current, _, peerErr := peer(conn, o.PeerUID)
	if err != nil || peerErr != nil || current != daemon || a != b || ctx.Err() != nil {
		return "", witnessUnproven
	}
	review := candidate.Digest([]any{daemon, a, evidence.Endpoint, evidence.Peer, evidence.Boot, evidence.Root, evidence.Schema, evidence.File.Device, evidence.File.Inode})
	if source {
		return review + "/" + candidate.Digest(association), nil
	}
	return review, nil
}
