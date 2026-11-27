//go:build linux

package linux

import (
	"bytes"
	"context"
	"encoding/binary"
	"slices"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func TestLinuxDeviceLinkIndicesDecodeOnlyLinkNotifications(t *testing.T) {
	link := deviceLinkNotification(unix.RTM_NEWLINK, 7, tcAttr(unix.IFLA_IFNAME, []byte("sample\x00")))
	// The message header's port ID is not the datagram sender's port ID.
	binary.NativeEndian.PutUint32(link[12:16], 800)
	data := bytes.Join([][]byte{
		link,
		tcMessage(unix.RTM_NEWNEIGH, 0, 0, nil),
		deviceLinkNotification(unix.RTM_NEWLINK, 0, nil),
		deviceLinkNotification(unix.RTM_NEWLINK, -1, nil),
		deviceLinkNotification(unix.RTM_DELLINK, 21, nil),
	}, nil)
	indices, ok := decodeRouteEventIndices(data, nil, deviceLinkEventIndex)
	if !ok || !slices.Equal(indices, []int{7, 21}) {
		t.Fatalf("got indices %v, accepted %v", indices, ok)
	}
}

func TestLinuxDeviceLinkIndicesRejectIncompleteDatagrams(t *testing.T) {
	valid := deviceLinkNotification(unix.RTM_NEWLINK, 7, nil)
	tests := map[string][]byte{
		"trailing bytes":              append(slices.Clone(valid), 1),
		"truncated message":           valid[:len(valid)-1],
		"short link header":           tcMessage(unix.RTM_NEWLINK, 0, 0, make([]byte, unix.SizeofIfInfomsg-1)),
		"invalid attribute":           deviceLinkNotification(unix.RTM_DELLINK, 7, []byte{0, 0, 0, 0}),
		"missing attr alignment":      deviceLinkNotification(unix.RTM_NEWLINK, 7, []byte{5, 0, 1, 0, 9}),
		"overrun after valid message": append(slices.Clone(valid), tcMessage(unix.NLMSG_OVERRUN, 0, 0, nil)...),
		"unexpected control error":    tcMessage(unix.NLMSG_ERROR, 0, 0, nil),
		"too many messages":           bytes.Repeat(valid, routeEventBatch+1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if indices, ok := decodeRouteEventIndices(data, nil, deviceLinkEventIndex); ok || len(indices) != 0 {
				t.Fatalf("bad datagram delivered %v, accepted %v", indices, ok)
			}
		})
	}
}

func TestLinuxDeviceLinkRequiresKernelMulticastSender(t *testing.T) {
	if !kernelRouteEventSender(&unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_LINK}, unix.RTMGRP_LINK) {
		t.Fatal("kernel link multicast denied")
	}
	for _, addr := range []unix.Sockaddr{
		nil,
		&unix.SockaddrUnix{},
		&unix.SockaddrNetlink{Family: unix.AF_NETLINK, Pid: 1, Groups: unix.RTMGRP_LINK},
		&unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_LINK << 1},
		&unix.SockaddrNetlink{Family: unix.AF_NETLINK},
	} {
		if kernelRouteEventSender(addr, unix.RTMGRP_LINK) {
			t.Fatalf("accepted unexpected sender %#v", addr)
		}
	}
}

func TestLinuxDeviceLinkSubscriberBoundAndCancelable(t *testing.T) {
	assertRouteEventSubscription(t, unix.RTMGRP_LINK, deviceLinkEventIndex)
}

func assertRouteEventSubscription(t *testing.T, group uint32, decode routeEventIndex) {
	t.Helper()
	fd, err := openInterfaceEventSocket(group)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	addr, err := unix.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	if peer, ok := addr.(*unix.SockaddrNetlink); !ok || peer.Groups != group {
		t.Fatalf("expected netlink multicast group %d, got %#v", group, addr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		receiveInterfaceEvents(ctx, fd, group, decode, func(inventory.InterfaceEvents) {})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not stop after cancellation")
	}
}

func deviceLinkNotification(kind uint16, index int32, attributes []byte) []byte {
	payload := make([]byte, unix.SizeofIfInfomsg)
	binary.NativeEndian.PutUint32(payload[4:8], uint32(index))
	return tcMessage(kind, 0, 0, append(payload, attributes...))
}
