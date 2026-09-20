//go:build linux

package safety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type tcpProbe struct {
	address, device, domain string
	index                   int
}

// NewTCPProbe accepts a numeric, explicitly configured management endpoint.
// DNS, HTTP redirects, arbitrary client URLs and alternate interfaces cannot
// change the probe path. This proves TCP reachability, not client confirmation.
func NewTCPProbe(address, device string) (Probe, error) {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil || endpoint.Port() == 0 || !endpoint.Addr().IsGlobalUnicast() || endpoint.Addr().IsLoopback() || endpoint.Addr().IsLinkLocalUnicast() || endpoint.Addr().Zone() != "" {
		return nil, errors.New("INVALID_MANAGEMENT_PROBE")
	}
	iface, err := net.InterfaceByName(device)
	if err != nil || iface.Flags&net.FlagLoopback != 0 || len(device) > 15 {
		return nil, errors.New("INVALID_MANAGEMENT_INTERFACE")
	}
	sum := sha256.Sum256([]byte(endpoint.String() + "\x00" + device + "\x00" + strconv.Itoa(iface.Index)))
	return &tcpProbe{endpoint.String(), device, hex.EncodeToString(sum[:]), iface.Index}, nil
}
func (p *tcpProbe) Domain() string { return p.domain }
func (p *tcpProbe) Check(ctx context.Context) error {
	iface, err := net.InterfaceByName(p.device)
	if err != nil || iface.Index != p.index || iface.Flags&net.FlagUp == 0 {
		return errors.New("MANAGEMENT_INTERFACE_CHANGED")
	}
	d := net.Dialer{Timeout: 2 * time.Second, Control: func(_, _ string, raw syscall.RawConn) error {
		var err error
		if outer := raw.Control(func(fd uintptr) {
			err = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, p.device)
		}); outer != nil {
			return outer
		}
		return err
	}}
	conn, err := d.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return err
	}
	return conn.Close()
}
