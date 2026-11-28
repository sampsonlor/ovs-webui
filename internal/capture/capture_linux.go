//go:build linux

package capture

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"golang.org/x/sys/unix"
)

// Observe collects only Ethernet headers for one fixed Ethertype on one
// root-approved local Interface. The socket is never promiscuous and has
// no write path. Native packet payloads are not delivered to the writer.
func Observe(ctx context.Context, resolve Resolver, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if resolve == nil {
		return Result{}, apitypes.Fail(503, "CAPTURE_AUTHORITY_UNAVAILABLE")
	}
	links, err := listenLinkChanges()
	if err != nil {
		return Result{}, apitypes.Fail(503, "CAPTURE_LINK_WITNESS_UNAVAILABLE")
	}
	defer unix.Close(links)
	witness, err := resolve(ctx, req.InterfaceID)
	if err != nil {
		return Result{}, err
	}
	if !witness.ValidFor(req.InterfaceID) || !currentKernelLink(witness) {
		return Result{}, apitypes.Fail(409, "CAPTURE_INTERFACE_UNPROVEN")
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	protocol := req.Profile.etherType()
	fd, err := listenEthernet(witness.IfIndex, protocol)
	if err != nil {
		return Result{}, apitypes.Fail(503, "CAPTURE_PROVIDER_UNAVAILABLE")
	}
	defer unix.Close(fd)

	check := func() error {
		if !unchangedCaptureLink(links, witness.IfIndex) {
			return apitypes.Fail(409, "CAPTURE_INTERFACE_CHANGED")
		}
		current, err := resolve(ctx, req.InterfaceID)
		if err != nil {
			return err
		}
		if current != witness || !currentKernelLink(witness) {
			return apitypes.Fail(409, "CAPTURE_INTERFACE_CHANGED")
		}
		return nil
	}
	if err := check(); err != nil {
		return Result{}, err
	}
	now := time.Now()
	end, lastCheck := now.Add(time.Duration(req.Seconds)*time.Second), now
	pcap := initialPCAP()
	out := Result{State: "complete", StartedAt: now.UTC()}
	var frame [frameBytes]byte
	poll := []unix.PollFd{{Fd: int32(links), Events: unix.POLLIN}, {Fd: int32(fd), Events: unix.POLLIN}}
	for now.Before(end) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if now.Sub(lastCheck) >= 500*time.Millisecond {
			if err := check(); err != nil {
				return Result{}, err
			}
			lastCheck = now
		}
		remaining := min(100*time.Millisecond, end.Sub(now))
		ready, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
		if err != nil && !errors.Is(err, unix.EINTR) {
			return Result{}, apitypes.Fail(503, "CAPTURE_PROVIDER_UNAVAILABLE")
		}
		now = time.Now()
		if ready < 1 || !now.Before(end) {
			continue
		}
		if poll[0].Revents&(unix.POLLNVAL|unix.POLLERR|unix.POLLHUP) != 0 ||
			poll[0].Revents&unix.POLLIN != 0 && !unchangedCaptureLink(links, witness.IfIndex) {
			return Result{}, apitypes.Fail(409, "CAPTURE_INTERFACE_CHANGED")
		}
		if poll[1].Revents&(unix.POLLNVAL|unix.POLLERR|unix.POLLHUP) != 0 {
			return Result{}, apitypes.Fail(409, "CAPTURE_INTERFACE_CHANGED")
		}
		if poll[1].Revents&unix.POLLIN == 0 {
			continue
		}
		n, address, err := unix.Recvfrom(fd, frame[:], unix.MSG_TRUNC)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		peer, ok := address.(*unix.SockaddrLinklayer)
		if err != nil || !ok || peer.Ifindex != witness.IfIndex {
			return Result{}, apitypes.Fail(409, "CAPTURE_INTERFACE_CHANGED")
		}
		if n < len(frame) || frame[12] != byte(protocol>>8) || frame[13] != byte(protocol) {
			out.State, out.Reason = "partial", "CAPTURE_FRAME_UNQUALIFIED"
			continue
		}
		next, ok := emitEthernetHeader(pcap, now, frame[:], n, protocol)
		if !ok {
			out.State, out.Reason = "partial", "CAPTURE_FRAME_UNQUALIFIED"
			continue
		}
		pcap = next
		out.Packets++
		if out.Packets == MaxPackets || len(pcap)+recordSize > req.MaxBytes {
			out.State, out.Reason = "truncated", "CAPTURE_RECORD_LIMIT"
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := check(); err != nil {
		return Result{}, err
	}
	if counters, err := unix.GetsockoptTpacketStats(fd, unix.SOL_PACKET, unix.PACKET_STATISTICS); err != nil {
		if out.State == "complete" {
			out.State, out.Reason = "partial", "CAPTURE_KERNEL_DROP_COUNT_UNKNOWN"
		}
	} else if counters.Drops > 0 {
		out.State, out.Reason = "truncated", "CAPTURE_KERNEL_DROP"
	}
	if out.Packets == 0 && out.State == "complete" {
		out.State = "no-data"
	}
	out.EndedAt, out.Bytes = time.Now().UTC(), pcap
	return out, nil
}

func currentKernelLink(b Binding) bool {
	link, err := net.InterfaceByIndex(b.IfIndex)
	return err == nil && link.Name == b.Name && link.Flags&net.FlagLoopback == 0 && len(link.HardwareAddr) == 6
}

func listenLinkChanges() (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return -1, err
	}
	if err = unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_LINK}); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func unchangedCaptureLink(fd, ifindex int) bool {
	var buffer [32 << 10]byte
	for i := 0; i < 32; i++ {
		length, _, flags, address, err := unix.Recvmsg(fd, buffer[:], nil, 0)
		if errors.Is(err, unix.EAGAIN) {
			return true
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		peer, ok := address.(*unix.SockaddrNetlink)
		if err != nil || !ok || peer.Pid != 0 || flags&unix.MSG_TRUNC != 0 || length == 0 {
			return false
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:length])
		if err != nil || len(messages) == 0 {
			return false
		}
		for _, message := range messages {
			switch message.Header.Type {
			case unix.NLMSG_ERROR, unix.NLMSG_OVERRUN:
				return false
			case unix.RTM_DELLINK, unix.RTM_NEWLINK:
				if len(message.Data) < unix.SizeofIfInfomsg {
					return false
				}
				index := int(int32(binary.NativeEndian.Uint32(message.Data[4:8])))
				if index == ifindex {
					// Conservatively abort a capture on any event for the target.
					return false
				}
			}
		}
	}
	return false // The link witness must keep up to support a result.
}

func listenEthernet(ifindex int, protocol uint16) (int, error) {
	var bytes [2]byte
	binary.BigEndian.PutUint16(bytes[:], protocol)
	networkOrder := binary.NativeEndian.Uint16(bytes[:])
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 32<<10); err == nil {
		err = unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: networkOrder, Ifindex: ifindex})
	}
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}
