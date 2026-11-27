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
	linkEventWindow     = 50 * time.Millisecond
	linkEventIdlePoll   = 100 * time.Millisecond
	linkEventRetryMin   = 250 * time.Millisecond
	linkEventRetryMax   = 5 * time.Second
	linkEventBufferSize = 32 << 10
	linkEventDatagrams  = 64
	linkEventBatch      = 128
)

// WatchDeviceLinks listens for Linux link changes and delivers bounded batches
// of ifindices synchronously. The cache worker schedules the independent reads.
func WatchDeviceLinks(ctx context.Context, publish func(inventory.DeviceLinkEvents)) {
	retry := linkEventRetryMin
	reportedLoss := false
	for ctx.Err() == nil {
		fd, err := openDeviceLinkSocket()
		if err == nil {
			started := time.Now()
			receiveDeviceLinkEvents(ctx, fd, func(batch inventory.DeviceLinkEvents) {
				reportedLoss = false
				publish(batch)
			})
			_ = unix.Close(fd)
			if ctx.Err() != nil {
				return
			}
			if time.Since(started) >= linkEventRetryMax {
				retry = linkEventRetryMin
				reportedLoss = false
			}
			if !reportedLoss {
				publish(inventory.DeviceLinkEvents{Lost: true})
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
		retry = min(retry*2, linkEventRetryMax)
	}
}

func openDeviceLinkSocket() (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return -1, err
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_LINK}); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func receiveDeviceLinkEvents(ctx context.Context, fd int, publish func(inventory.DeviceLinkEvents)) {
	buffer := make([]byte, linkEventBufferSize)
	var decoded [linkEventBatch]int
	indices := make([]int, 0, linkEventBatch)
	seen := make(map[int]struct{}, linkEventBatch)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	var deadline time.Time
	datagrams := 0
	for ctx.Err() == nil {
		now := time.Now()
		if !deadline.IsZero() && !now.Before(deadline) {
			if len(indices) > 0 {
				publish(inventory.DeviceLinkEvents{Indices: slices.Clone(indices)})
			}
			indices = indices[:0]
			clear(seen)
			deadline = time.Time{}
			datagrams = 0
		}
		timeout := linkEventIdlePoll
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
		if !kernelDeviceLinkSender(from) {
			continue
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(linkEventWindow)
		}
		datagrams++
		if n <= 0 || n > len(buffer) || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || datagrams > linkEventDatagrams {
			return
		}
		update, ok := decodeDeviceLinkIndices(buffer[:n], decoded[:0])
		if !ok {
			return
		}
		for _, index := range update {
			if _, exists := seen[index]; exists {
				continue
			}
			if len(indices) == linkEventBatch {
				return
			}
			seen[index] = struct{}{}
			indices = append(indices, index)
		}
	}
}

func kernelDeviceLinkSender(from unix.Sockaddr) bool {
	peer, ok := from.(*unix.SockaddrNetlink)
	// The recvmsg source address, not nlmsg_pid, identifies the sender.
	return ok && peer.Family == unix.AF_NETLINK && peer.Pid == 0 && peer.Groups == unix.RTMGRP_LINK
}

func decodeDeviceLinkIndices(data []byte, indices []int) ([]int, bool) {
	for messages := 0; len(data) != 0; messages++ {
		if len(data) < unix.SizeofNlMsghdr || messages == linkEventBatch {
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
		case unix.RTM_NEWLINK, unix.RTM_DELLINK:
			if len(payload) < unix.SizeofIfInfomsg || !validDeviceLinkAttributes(payload[unix.SizeofIfInfomsg:]) {
				return nil, false
			}
			index := int(int32(binary.NativeEndian.Uint32(payload[4:8])))
			if index > 0 {
				indices = append(indices, index)
			}
		}
	}
	return indices, true
}

func validDeviceLinkAttributes(data []byte) bool {
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
