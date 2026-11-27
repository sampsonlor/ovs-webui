//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const (
	routeEventWindow     = 50 * time.Millisecond
	routeEventIdlePoll   = 100 * time.Millisecond
	routeEventRetryMin   = 250 * time.Millisecond
	routeEventRetryMax   = 5 * time.Second
	routeEventBufferSize = 32 << 10
	routeEventDatagrams  = 64
	routeEventBatch      = 128
)

// WatchDeviceLinks listens for Linux link changes and delivers bounded batches
// of ifindices synchronously. The cache worker schedules the independent reads.
func WatchDeviceLinks(ctx context.Context, publish func(inventory.DeviceLinkEvents)) {
	watchInterfaceEvents(ctx, unix.RTMGRP_LINK, deviceLinkEventIndex, publish)
}

// WatchNeighborEvents listens for IPv4 and IPv6 neighbor changes. Only the
// interface indices are forwarded as hints for a fresh, independent dump.
func WatchNeighborEvents(ctx context.Context, publish func(inventory.InterfaceEvents)) {
	watchInterfaceEvents(ctx, unix.RTMGRP_NEIGH, neighborEventIndex, publish)
}

// A decoder returns zero for an irrelevant message or false for a bad frame.
type routeEventIndex func(kind uint16, payload []byte) (int, bool)

func watchInterfaceEvents(ctx context.Context, group uint32, decode routeEventIndex, publish func(inventory.InterfaceEvents)) {
	retry := routeEventRetryMin
	reportedLoss := false
	for ctx.Err() == nil {
		fd, err := openInterfaceEventSocket(group)
		if err == nil {
			started := time.Now()
			receiveInterfaceEvents(ctx, fd, group, decode, func(batch inventory.InterfaceEvents) {
				reportedLoss = false
				publish(batch)
			})
			_ = unix.Close(fd)
			if ctx.Err() != nil {
				return
			}
			if time.Since(started) >= routeEventRetryMax {
				retry = routeEventRetryMin
				reportedLoss = false
			}
			if !reportedLoss {
				publish(inventory.InterfaceEvents{Lost: true})
				reportedLoss = true
			}
		}
		// Until the first successful subscription, periodic sampling is already
		// authoritative. Failed binds during an outage add no new loss signal.
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		retry = min(retry*2, routeEventRetryMax)
	}
}

func openInterfaceEventSocket(group uint32) (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return -1, err
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: group}); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func receiveInterfaceEvents(ctx context.Context, fd int, group uint32, decode routeEventIndex, publish func(inventory.InterfaceEvents)) {
	buffer := make([]byte, routeEventBufferSize)
	var decoded [routeEventBatch]int
	indices := make([]int, 0, routeEventBatch)
	seen := make(map[int]struct{}, routeEventBatch)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	var deadline time.Time
	datagrams := 0
	for ctx.Err() == nil {
		now := time.Now()
		if !deadline.IsZero() && !now.Before(deadline) {
			if len(indices) > 0 {
				publish(inventory.InterfaceEvents{Indices: slices.Clone(indices)})
			}
			indices = indices[:0]
			clear(seen)
			deadline = time.Time{}
			datagrams = 0
		}
		timeout := routeEventIdlePoll
		if !deadline.IsZero() {
			timeout = min(timeout, deadline.Sub(now))
		}
		ready, err := unix.Poll(poll, int((timeout+time.Millisecond-1)/time.Millisecond))
		if err == unix.EINTR || err == nil && ready == 0 {
			continue
		}
		if err != nil || poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return
		}
		n, _, flags, from, err := unix.Recvmsg(fd, buffer, nil, unix.MSG_DONTWAIT)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			// ENOBUFS, and any other receive failure, break continuity.
			return
		}
		if !kernelRouteEventSender(from, group) {
			continue
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(routeEventWindow)
		}
		datagrams++
		if n <= 0 || n > len(buffer) || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || datagrams > routeEventDatagrams {
			return
		}
		update, ok := decodeRouteEventIndices(buffer[:n], decoded[:0], decode)
		if !ok {
			return
		}
		for _, index := range update {
			if _, exists := seen[index]; exists {
				continue
			}
			if len(indices) == routeEventBatch {
				return
			}
			seen[index] = struct{}{}
			indices = append(indices, index)
		}
	}
}

func kernelRouteEventSender(from unix.Sockaddr, group uint32) bool {
	peer, ok := from.(*unix.SockaddrNetlink)
	// The recvmsg source address, not nlmsg_pid, identifies the sender.
	return ok && peer.Family == unix.AF_NETLINK && peer.Pid == 0 && peer.Groups == group
}

func decodeRouteEventIndices(data []byte, indices []int, decode routeEventIndex) ([]int, bool) {
	for messages := 0; len(data) != 0; messages++ {
		if len(data) < unix.SizeofNlMsghdr || messages == routeEventBatch {
			return nil, false
		}
		n := int(binary.NativeEndian.Uint32(data[:4]))
		if n < unix.SizeofNlMsghdr || n > len(data) || align4(n) > len(data) {
			return nil, false
		}
		kind := binary.NativeEndian.Uint16(data[4:6])
		payload := data[unix.SizeofNlMsghdr:n]
		data = data[align4(n):]
		switch kind {
		case unix.NLMSG_OVERRUN, unix.NLMSG_ERROR, unix.NLMSG_DONE:
			return nil, false
		}
		index, valid := decode(kind, payload)
		if !valid {
			return nil, false
		}
		if index > 0 {
			indices = append(indices, index)
		}
	}
	return indices, true
}

func deviceLinkEventIndex(kind uint16, payload []byte) (int, bool) {
	if kind != unix.RTM_NEWLINK && kind != unix.RTM_DELLINK {
		return 0, true
	}
	if len(payload) < unix.SizeofIfInfomsg || !validRouteEventAttributes(payload[unix.SizeofIfInfomsg:]) {
		return 0, false
	}
	return int(int32(binary.NativeEndian.Uint32(payload[4:8]))), true
}

func neighborEventIndex(kind uint16, payload []byte) (int, bool) {
	if kind != unix.RTM_NEWNEIGH && kind != unix.RTM_DELNEIGH {
		return 0, true
	}
	if len(payload) < unix.SizeofNdMsg || !validRouteEventAttributes(payload[unix.SizeofNdMsg:]) {
		return 0, false
	}
	if family := payload[0]; family != unix.AF_INET && family != unix.AF_INET6 || payload[10]&unix.NTF_PROXY != 0 {
		return 0, true
	}
	return int(int32(binary.NativeEndian.Uint32(payload[4:8]))), true
}

func validRouteEventAttributes(data []byte) bool {
	for len(data) != 0 {
		if len(data) < unix.SizeofRtAttr {
			return false
		}
		n := int(binary.NativeEndian.Uint16(data[:2]))
		if n < unix.SizeofRtAttr || align4(n) > len(data) {
			return false
		}
		data = data[align4(n):]
	}
	return true
}
