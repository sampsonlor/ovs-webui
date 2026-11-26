//go:build linux

package linux

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func lldpTestTLV(kind byte, value ...byte) []byte {
	return append(binary.BigEndian.AppendUint16(nil, uint16(kind)<<9|uint16(len(value))), value...)
}

func lldpTestFrame(tlvs ...[]byte) []byte {
	header := make([]byte, lldpEtherHeader)
	copy(header[:6], lldpNearestBridge[:])
	copy(header[6:12], []byte{2, 0, 0, 0, 0, 3})
	binary.BigEndian.PutUint16(header[12:14], unix.ETH_P_LLDP)
	return slices.Concat(append([][]byte{header}, tlvs...)...)
}

func TestLinuxLLDPDecodesAdvertisementAndWithdrawal(t *testing.T) {
	chassis := lldpTestTLV(1, 4, 2, 0, 0, 0, 0, 3)
	port := lldpTestTLV(2, append([]byte{5}, []byte("synthetic-port")...)...)
	name := lldpTestTLV(5, []byte("synthetic-switch")...)
	for _, ttl := range []uint16{120, 0} {
		ttlField := lldpTestTLV(3, binary.BigEndian.AppendUint16(nil, ttl)...)
		frame := lldpTestFrame(chassis, port, ttlField, name, lldpTestTLV(127, 1, 2, 3, 4), lldpTestTLV(0), []byte{0x7a, 0x99})
		got, valid := decodeLLDP(frame)
		if !valid || got.TTL != ttl || got.SystemName != "synthetic-switch" ||
			got.ChassisID != (inventory.LLDPID{Subtype: 4, Encoding: "mac", Value: "02:00:00:00:00:03"}) ||
			got.PortID != (inventory.LLDPID{Subtype: 5, Encoding: "text", Value: "synthetic-port"}) {
			t.Fatal("incorrect LLDP evidence", got, valid)
		}
	}
	got, valid := decodeLLDP(lldpTestFrame(
		lldpTestTLV(1, 5, 1, 192, 0, 2, 41), lldpTestTLV(2, 6, 0, 0xfe), lldpTestTLV(3, 0, 120),
		lldpTestTLV(5, 'n', 0x01), lldpTestTLV(0),
	))
	if !valid || got.SystemName != "" || got.ChassisID != (inventory.LLDPID{Subtype: 5, Encoding: "text", Value: "192.0.2.41"}) ||
		got.PortID != (inventory.LLDPID{Subtype: 6, Encoding: "hex", Value: "00fe"}) {
		t.Fatal("opaque ID or unsafe label was not handled correctly", got, valid)
	}
}

func TestLinuxLLDPRejectsUnprovenFrame(t *testing.T) {
	c, p, ttl, end := lldpTestTLV(1, 7, 'c'), lldpTestTLV(2, 7, 'p'), lldpTestTLV(3, 0, 120), lldpTestTLV(0)
	valid := lldpTestFrame(c, p, ttl, end)
	wrongType, wrongDestination := slices.Clone(valid), slices.Clone(valid)
	binary.BigEndian.PutUint16(wrongType[12:14], unix.ETH_P_IP)
	wrongDestination[5] = 3
	for _, raw := range [][]byte{
		valid[:13], wrongType, wrongDestination,
		lldpTestFrame(p, c, ttl, end), lldpTestFrame(c, p, end), lldpTestFrame(c, p, ttl),
		lldpTestFrame(c, p, ttl, c, end), lldpTestFrame(c, p, ttl, p, end), lldpTestFrame(c, p, ttl, ttl, end),
		lldpTestFrame(c, p, lldpTestTLV(3, 1), end), lldpTestFrame(c, p, lldpTestTLV(3, 1, 2, 3), end),
		lldpTestFrame(lldpTestTLV(1, 4, 2, 3), p, ttl, end), lldpTestFrame(lldpTestTLV(1, 5, 1, 192), p, ttl, end),
		lldpTestFrame(lldpTestTLV(1, 0, 'x'), p, ttl, end), lldpTestFrame(lldpTestTLV(1, 8, 'x'), p, ttl, end),
		lldpTestFrame(c, p, ttl, lldpTestTLV(5, 'a'), lldpTestTLV(5, 'b'), end), lldpTestFrame(c, p, ttl, lldpTestTLV(0, 1)),
		lldpTestFrame(c, p, ttl, []byte{0xff, 0xff}, end), append(slices.Clone(valid), make([]byte, maxLLDPFrame)...),
	} {
		if got, ok := decodeLLDP(raw); ok {
			t.Fatal("unproven frame accepted", got)
		}
	}
	if !lldpIngress(&unix.SockaddrLinklayer{Ifindex: 7, Hatype: unix.ARPHRD_ETHER, Pkttype: unix.PACKET_MULTICAST}, 7) {
		t.Fatal("expected qualified multicast ingress")
	}
	for _, from := range []unix.Sockaddr{
		&unix.SockaddrLinklayer{Ifindex: 7, Hatype: unix.ARPHRD_ETHER, Pkttype: unix.PACKET_OUTGOING},
		&unix.SockaddrLinklayer{Ifindex: 8, Hatype: unix.ARPHRD_ETHER, Pkttype: unix.PACKET_MULTICAST},
		&unix.SockaddrLinklayer{Ifindex: 7, Hatype: unix.ARPHRD_LOOPBACK, Pkttype: unix.PACKET_MULTICAST},
		&unix.SockaddrUnix{}, nil,
	} {
		if lldpIngress(from, 7) {
			t.Fatal("wrong ingress admitted", from)
		}
	}
}

func TestLinuxLLDPKernelFilterUsesBoundedWireShape(t *testing.T) {
	// A connected Unix datagram pair exercises the actual Linux filter VM with
	// synthetic bytes, without creating AF_PACKET sockets or sending on a link.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	if err := attachLLDPFilter(fds[1]); err != nil {
		t.Fatal(err)
	}
	good := lldpTestFrame(lldpTestTLV(1, 7, 'c'), lldpTestTLV(2, 7, 'p'), lldpTestTLV(3, 0, 120), lldpTestTLV(0))
	wrongType, wrongDestination := slices.Clone(good), slices.Clone(good)
	wrongType[12], wrongType[13] = 0x81, 0
	wrongDestination[5] = 0
	for _, data := range [][]byte{wrongType, wrongDestination, good[:13], append(slices.Clone(good), make([]byte, maxLLDPFrame)...), good} {
		if _, err := unix.Write(fds[0], data); err != nil {
			t.Fatal(err)
		}
	}
	read := make([]byte, 2*maxLLDPFrame)
	n, err := unix.Read(fds[1], read)
	if err != nil || !slices.Equal(read[:n], good) {
		t.Fatalf("wrong payload admitted: bytes=%d err=%v", n, err)
	}
	if _, err := unix.Read(fds[1], read); !errors.Is(err, unix.EAGAIN) {
		t.Fatal("additional datagram survived filter", err)
	}
	unlock := unix.SetsockoptInt(fds[1], unix.SOL_SOCKET, unix.SO_LOCK_FILTER, 0)
	if !errors.Is(unlock, unix.EPERM) {
		t.Fatal("LLDP filter was not locked", unlock)
	}
}

func lldpControlTime(sec, nsec int64) []byte {
	control := make([]byte, unix.CmsgSpace(16))
	header := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
	header.Level, header.Type = unix.SOL_SOCKET, unix.SO_TIMESTAMPNS_NEW
	header.SetLen(unix.CmsgLen(16))
	data := control[unix.CmsgLen(0):]
	binary.NativeEndian.PutUint64(data[:8], uint64(sec))
	binary.NativeEndian.PutUint64(data[8:16], uint64(nsec))
	return control
}

func TestLinuxLLDPKernelTimestampAndEpoch(t *testing.T) {
	opened, now := time.Unix(1500, 0), time.Unix(1502, 0)
	control := lldpControlTime(1501, 100)
	if received, ok := lldpTimestamp(control, opened, now); !ok || !received.Equal(time.Unix(1501, 100)) {
		t.Fatal("kernel timestamp lost", received, ok)
	}
	for _, invalid := range [][]byte{
		nil, control[:4], slices.Concat(control, control), lldpControlTime(1499, 0), lldpControlTime(1503, 0), lldpControlTime(1501, 1e9),
	} {
		if received, ok := lldpTimestamp(invalid, opened, now); ok {
			t.Fatal("unqualified receive clock accepted", received)
		}
	}
	fake, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := NewLLDPObserver()
	p.epoch = 1
	defer p.Close()
	p.receivers = map[inventory.DeviceRequest]lldpReceiver{{Name: "synthetic", IfIndex: 7}: {fd: fake, openedAt: opened}}
	result, err := p.ObserveLLDP(t.Context(), 2, nil)
	if err != nil || len(result.Frames) != 0 || len(result.Covered) != 0 || result.ObservedAt.IsZero() || len(p.receivers) != 0 {
		t.Fatal("old receive queue did not retire across epoch", result, err)
	}
	if _, err := unix.Getsockname(fake); !errors.Is(err, unix.EBADF) {
		t.Fatal("old device socket still open", err)
	}
}
