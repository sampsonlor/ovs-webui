//go:build linux

package ovsdb

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

// Retain one transport per DB monitor session. Opening/closing a control
// connection for each publish can interrupt the concurrent strict socket dump.
// This holds no build trust: each observation rehashes the current opened files
// and rechecks both peer identities. A failed read closes the transport and
// withdraws availability; it never reconnects/retries within that observation.
type mirrorObservationConnection struct {
	conn   net.Conn
	daemon string
}

func (o *mirrorObservationConnection) close() {
	if o.conn != nil {
		_ = o.conn.Close()
	}
	o.conn = nil
	o.daemon = ""
}
func (o *mirrorObservationConnection) get(ctx context.Context, path string, uid uint32) (net.Conn, error) {
	failure := func() (net.Conn, error) { o.close(); return nil, errors.New("MIRROR_CONTROL_OR_BUILD_UNPROVEN") }
	if ctx.Err() != nil {
		return failure()
	}
	if o.conn == nil {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
		if err != nil {
			return failure()
		}
		o.conn = conn
		o.daemon, _, err = peer(conn, uid)
		if err != nil {
			return failure()
		}
	}
	current, _, err := peer(o.conn, uid)
	if err != nil || current != o.daemon {
		return failure()
	}
	c, ok := o.conn.(*net.UnixConn)
	if !ok {
		return failure()
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return failure()
	}
	alive := false
	if raw.Control(func(fd uintptr) {
		var data [1]byte
		n, _, err := unix.Recvfrom(int(fd), data[:], unix.MSG_PEEK|unix.MSG_DONTWAIT)
		alive = n > 0 || err == unix.EAGAIN || err == unix.EWOULDBLOCK
	}) != nil || !alive || ctx.Err() != nil {
		return failure()
	}
	return o.conn, nil
}
func (p *Provider) mirrorObservation() (func(context.Context, inventory.Evidence) (string, error), func()) {
	holder := &mirrorObservationConnection{}
	read := func(ctx context.Context, evidence inventory.Evidence) (string, error) {
		if p.mirror == nil {
			return "", errors.New("MIRROR_RUNTIME_NOT_CONFIGURED")
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		conn, err := holder.get(ctx, p.mirror.ControlSocket, p.mirror.PeerUID)
		if err != nil {
			return "", err
		}
		proof, err := p.mirrorRuntimeConnection(ctx, p.mirror, evidence, conn, false)
		if err == nil {
			_, err = holder.get(ctx, p.mirror.ControlSocket, p.mirror.PeerUID)
		}
		if err != nil {
			holder.close()
			return "", err
		}
		return proof, nil
	}
	return read, holder.close
}
