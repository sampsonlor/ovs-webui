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

// Dispatch performs two fresh checks around admission. Hold its own read-only
// probe sockets through both checks so asynchronously closing accepted FDs do
// not invalidate the next strict /proc inspection. No trust is reused.
func mirrorAttemptContext(ctx context.Context, enabled bool) (context.Context, func()) {
	if !enabled {
		return ctx, func() {}
	}
	return treeSourceContext(ctx)
}

// No control RPC is sent. SO_PEERCRED binds the opened control connection to
// the root-owned executable. Admission/dispatch/Applied additionally prove its
// actual established connection to this exact observed DB/file/root/schema.
// No trust cache or fallback to an executable name/version is used.
func (p *Provider) mirrorRuntime(ctx context.Context, o *MirrorExecutionOptions, evidence inventory.Evidence, source bool) (string, error) {
	if o == nil {
		return "", errors.New("MIRROR_RUNTIME_NOT_CONFIGURED")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ctx, release := treeSourceContext(ctx)
	defer release()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", o.ControlSocket)
	if err != nil {
		return "", errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN")
	}
	if retainTreeSourceConnection(ctx, conn) != nil {
		return "", errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return "", errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN")
	}
	daemon, _, err := peer(conn, o.PeerUID)
	if err != nil {
		return "", errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN")
	}
	build := SpanningTreeWitnessBuild{Executable: o.Executable, Manifest: o.Manifest, ManifestDigest: o.ManifestDigest}
	qualify := func(m witnessBuildManifest) (string, bool) {
		return evidence.Schema, m.Format == "ovs-webui.mirror-build.v1" && witnessArchitecture(m.Arch, runtime.GOARCH) && witnessHex(m.Executable) && mirror.LookupBuildProfile(m.Profile, evidence.Schema, m.Variant)
	}
	a, _, err := attestNativeBuild(ctx, conn, o.PeerUID, build, qualify)
	if err != nil {
		return "", errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN")
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
		return "", errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN")
	}
	review := candidate.Digest([]any{daemon, a, evidence.Endpoint, evidence.Peer, evidence.Boot, evidence.Root, evidence.Schema, evidence.File.Device, evidence.File.Inode})
	if source {
		return review + "/" + candidate.Digest(association), nil
	}
	return review, nil
}
