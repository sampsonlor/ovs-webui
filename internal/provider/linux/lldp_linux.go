//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const (
	maxLLDPReadFrames   = 128
	lldpReadBudget      = 2 * time.Second
	lldpReceiveBuffer   = 32 << 10
	linuxVLANPresentBPF = 1<<32 - 4096 + 48 // SKF_AD_OFF + SKF_AD_VLAN_TAG_PRESENT.
)

type lldpReceiver struct {
	fd       int
	openedAt time.Time
}

// PassiveLLDPObserver owns exact per-interface receive sockets. Its manager
// scheduler serializes ObserveLLDP and calls Close after the scheduler stops.
type PassiveLLDPObserver struct {
	epoch     uint64
	scope     []inventory.DeviceRequest
	receivers map[inventory.DeviceRequest]lldpReceiver
}

func NewLLDPObserver() *PassiveLLDPObserver { return &PassiveLLDPObserver{} }

func (p *PassiveLLDPObserver) Close() {
	for req := range p.receivers {
		p.remove(req)
	}
	p.scope = nil
}

func (p *PassiveLLDPObserver) remove(req inventory.DeviceRequest) {
	if capture, ok := p.receivers[req]; ok {
		_ = unix.Close(capture.fd) // Closing also releases the exact multicast membership.
		delete(p.receivers, req)
	}
}

func (p *PassiveLLDPObserver) ObserveLLDP(ctx context.Context, epoch uint64, interfaces []inventory.DeviceRequest) (inventory.LLDPObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, lldpReadBudget)
	defer cancel()
	if len(interfaces) > inventory.MaxLLDPInterfaces {
		p.Close()
		return inventory.LLDPObservation{}, apitypes.Fail(503, "LLDP_SCOPE_INVALID")
	}
	scope, err := neighborScope(interfaces)
	if err != nil {
		p.Close()
		return inventory.LLDPObservation{}, apitypes.Fail(503, "LLDP_SCOPE_INVALID")
	}
	if p.epoch != epoch || !slices.Equal(p.scope, interfaces) {
		p.Close() // Queued packets must never survive an inventory rebind or outage.
		p.epoch = epoch
		p.scope = slices.Clone(interfaces)
	}
	if ctx.Err() != nil {
		p.Close()
		return inventory.LLDPObservation{}, apitypes.Fail(503, "LLDP_OBSERVATION_TIMEOUT")
	}
	result := inventory.LLDPObservation{Frames: []inventory.LLDPFrame{}, Covered: []inventory.DeviceRequest{}}
	if len(scope) == 0 {
		result.ObservedAt = time.Now()
		return result, nil
	}
	routeSocket, err := openTCSocket()
	if err != nil {
		return p.linkFailure(ctx)
	}
	defer unix.Close(routeSocket.fd)
	route := neighborRoute{fd: routeSocket.fd, buffer: make([]byte, 64<<10)}
	before, err := route.links(ctx, scope)
	if err != nil {
		return p.linkFailure(ctx)
	}
	if p.receivers == nil {
		p.receivers = make(map[inventory.DeviceRequest]lldpReceiver, len(interfaces))
	}
	socketFailure := false
	active := make([]inventory.DeviceRequest, 0, len(interfaces))
	for _, req := range interfaces {
		if before[req.IfIndex].hardware != unix.ARPHRD_ETHER {
			p.remove(req)
			continue
		}
		if capture, ok := p.receivers[req]; !ok || !capture.bound(req.IfIndex) {
			// The kernel can revoke a binding between inventory updates. A
			// replacement socket cannot continue the retired socket's evidence.
			result.Truncated = result.Truncated || ok
			p.remove(req)
			capture, err := openLLDPReceiver(req.IfIndex)
			if err != nil {
				socketFailure = true
				continue
			}
			p.receivers[req] = capture
		}
		active = append(active, req)
	}
	if len(active) != 0 && p.readFrames(ctx, slices.Clone(active), &result) {
		socketFailure = true
	}
	if ctx.Err() != nil {
		return p.linkFailure(ctx)
	}
	after, err := route.links(ctx, scope)
	if err != nil {
		return p.linkFailure(ctx)
	}
	covered := make(map[inventory.DeviceRequest]bool, len(active))
	for _, req := range active {
		capture, ok := p.receivers[req]
		if !ok || before[req.IfIndex] != after[req.IfIndex] || !capture.bound(req.IfIndex) {
			p.remove(req)
			continue
		}
		stats, err := unix.GetsockoptTpacketStats(capture.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
		if err != nil {
			p.remove(req)
			socketFailure = true
			continue
		}
		result.Truncated = result.Truncated || stats.Drops > 0
		covered[req] = true
		result.Covered = append(result.Covered, req)
	}
	if len(result.Covered) == 0 && socketFailure {
		return inventory.LLDPObservation{}, apitypes.Fail(503, "LLDP_SOCKET_UNAVAILABLE")
	}
	result.Frames = slices.DeleteFunc(result.Frames, func(f inventory.LLDPFrame) bool { return !covered[f.Interface] })
	if ctx.Err() != nil {
		return p.linkFailure(ctx)
	}
	result.ObservedAt = time.Now()
	return result, nil
}

func (p *PassiveLLDPObserver) linkFailure(ctx context.Context) (inventory.LLDPObservation, error) {
	p.Close()
	if ctx.Err() != nil {
		return inventory.LLDPObservation{}, apitypes.Fail(503, "LLDP_OBSERVATION_TIMEOUT")
	}
	return inventory.LLDPObservation{}, apitypes.Fail(503, "LLDP_LINK_UNPROVEN")
}

func lldpPacketProtocol() uint16 {
	return binary.NativeEndian.Uint16([]byte{0, unix.ETH_P_ALL}) // htons(ETH_P_ALL)
}

func openLLDPReceiver(index int) (lldpReceiver, error) {
	// Protocol zero cannot deliver any frames during setup.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return lldpReceiver{}, err
	}
	var result lldpReceiver
	defer func() {
		if result.openedAt.IsZero() {
			_ = unix.Close(fd)
		}
	}()
	if err := attachLLDPFilter(fd); err != nil {
		return result, err
	}
	for _, option := range []struct{ level, key, value int }{
		{unix.SOL_PACKET, unix.PACKET_IGNORE_OUTGOING, 1},
		{unix.SOL_SOCKET, unix.SO_TIMESTAMPNS_NEW, 1},
		{unix.SOL_SOCKET, unix.SO_RCVBUF, lldpReceiveBuffer},
	} {
		if err := unix.SetsockoptInt(fd, option.level, option.key, option.value); err != nil {
			return result, err
		}
	}
	openedAt := time.Now()
	// OVS can consume ingress before protocol-specific taps. ETH_P_ALL taps
	// receive that ingress, but only the locked LLDP filter admits packets.
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: lldpPacketProtocol(), Ifindex: index}); err != nil {
		return result, err
	}
	var address [8]uint8
	copy(address[:], lldpNearestBridge[:])
	if err := unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, &unix.PacketMreq{
		Ifindex: int32(index), Type: unix.PACKET_MR_MULTICAST, Alen: 6, Address: address,
	}); err != nil {
		return result, err
	}
	result = lldpReceiver{fd: fd, openedAt: openedAt}
	return result, nil
}

func attachLLDPFilter(fd int) error {
	// All jump failure targets are the final drop instruction. Check metadata
	// too because Linux may strip a VLAN tag before the packet tap sees it.
	code := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: linuxVLANPresentBPF},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 9, K: 0},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_LEN},
		{Code: unix.BPF_JMP | unix.BPF_JGT | unix.BPF_K, Jt: 7, K: maxLLDPFrame},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 5, K: 0x0180c200},
		{Code: unix.BPF_LD | unix.BPF_H | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 3, K: 0x000e},
		{Code: unix.BPF_LD | unix.BPF_H | unix.BPF_ABS, K: 12},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: unix.ETH_P_LLDP},
		{Code: unix.BPF_RET | unix.BPF_K, K: maxLLDPFrame},
		{Code: unix.BPF_RET | unix.BPF_K, K: 0},
	}
	if err := unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &unix.SockFprog{Len: uint16(len(code)), Filter: &code[0]}); err != nil {
		return err
	}
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_LOCK_FILTER, 1)
}

func (c lldpReceiver) bound(index int) bool {
	addr, err := unix.Getsockname(c.fd)
	link, ok := addr.(*unix.SockaddrLinklayer)
	return err == nil && ok && link.Ifindex == index && link.Hatype == unix.ARPHRD_ETHER && link.Protocol == lldpPacketProtocol()
}

func (p *PassiveLLDPObserver) readFrames(ctx context.Context, pending []inventory.DeviceRequest, result *inventory.LLDPObservation) (socketFailure bool) {
	frame := make([]byte, maxLLDPFrame)
	control := make([]byte, unix.CmsgSpace(16)) // __kernel_timespec in SCM_TIMESTAMPNS_NEW.
	read := 0
	for len(pending) != 0 && read < maxLLDPReadFrames && ctx.Err() == nil {
		next := pending[:0]
		for _, req := range pending {
			if read == maxLLDPReadFrames || ctx.Err() != nil {
				break
			}
			capture := p.receivers[req]
			n, oob, flags, addr, err := unix.Recvmsg(capture.fd, frame, control, unix.MSG_DONTWAIT)
			switch err {
			case unix.EAGAIN:
				continue
			case unix.EINTR:
				next = append(next, req)
				continue
			case nil:
			default:
				p.remove(req)
				socketFailure = true
				continue
			}
			read++
			next = append(next, req)
			if !lldpIngress(addr, req.IfIndex) {
				continue
			}
			if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n < 0 || n > len(frame) || oob < 0 || oob > len(control) {
				result.Truncated = true
				continue
			}
			row, valid := decodeLLDP(frame[:n])
			if !valid {
				continue
			}
			received, valid := lldpTimestamp(control[:oob], capture.openedAt, time.Now())
			if !valid {
				result.Truncated = true
				continue
			}
			row.Interface, row.ReceivedAt = req, received
			result.Frames = append(result.Frames, row)
		}
		pending = next
	}
	result.Truncated = result.Truncated || read == maxLLDPReadFrames
	return socketFailure
}

func lldpIngress(addr unix.Sockaddr, index int) bool {
	link, ok := addr.(*unix.SockaddrLinklayer)
	return ok && link.Ifindex == index && link.Hatype == unix.ARPHRD_ETHER && link.Pkttype == unix.PACKET_MULTICAST
}

func lldpTimestamp(control []byte, openedAt, now time.Time) (time.Time, bool) {
	messages, err := unix.ParseSocketControlMessage(control)
	if err != nil || len(messages) != 1 {
		return time.Time{}, false
	}
	msg := messages[0]
	if msg.Header.Level != unix.SOL_SOCKET || msg.Header.Type != unix.SO_TIMESTAMPNS_NEW || len(msg.Data) != 16 {
		return time.Time{}, false
	}
	sec := int64(binary.NativeEndian.Uint64(msg.Data[:8]))
	nsec := int64(binary.NativeEndian.Uint64(msg.Data[8:]))
	if sec <= 0 || nsec < 0 || nsec >= int64(time.Second) {
		return time.Time{}, false
	}
	at := time.Unix(sec, nsec)
	return at, !at.Before(openedAt) && !at.After(now)
}
