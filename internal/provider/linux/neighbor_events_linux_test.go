//go:build linux

package linux

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxNeighborEventsDecodeOnlyIPNeighborHints(t *testing.T) {
	ip4 := tcAttr(unix.NDA_DST, []byte{192, 0, 2, 1})
	ip6 := tcAttr(unix.NDA_DST, []byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	fixture := bytes.Join([][]byte{
		neighborNotification(unix.RTM_NEWNEIGH, unix.AF_INET, 0, 7, ip4),
		neighborNotification(unix.RTM_DELNEIGH, unix.AF_INET6, 0, 21, ip6),
		neighborNotification(unix.RTM_NEWNEIGH, unix.AF_BRIDGE, 0, 22, nil),
		neighborNotification(unix.RTM_NEWNEIGH, unix.AF_INET, unix.NTF_PROXY, 23, ip4),
		neighborNotification(unix.RTM_DELNEIGH, unix.AF_INET6, 0, 0, ip6),
		neighborNotification(unix.RTM_NEWNEIGH, unix.AF_INET6, 0, -2, ip6),
		neighborNotification(unix.RTM_GETNEIGH, unix.AF_INET, 0, 24, ip4),
		deviceLinkNotification(unix.RTM_NEWLINK, 25, nil),
	}, nil)
	indices, ok := decodeRouteEventIndices(fixture, nil, neighborEventIndex)
	if !ok || !slices.Equal(indices, []int{7, 21}) {
		t.Fatalf("got indices %v, accepted %v", indices, ok)
	}
}

func TestLinuxNeighborEventsRejectIncompleteDatagrams(t *testing.T) {
	valid := neighborNotification(unix.RTM_NEWNEIGH, unix.AF_INET, 0, 7, nil)
	tests := map[string][]byte{
		"truncated header after valid neighbor": append(slices.Clone(valid), 1),
		"short ndmsg":                           tcMessage(unix.RTM_NEWNEIGH, 0, 0, make([]byte, unix.SizeofNdMsg-1)),
		"bad attribute length":                  neighborNotification(unix.RTM_DELNEIGH, unix.AF_INET6, 0, 7, []byte{0, 0, 0, 0}),
		"overrun discards valid prefix":         append(slices.Clone(valid), tcMessage(unix.NLMSG_OVERRUN, 0, 0, nil)...),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if indices, ok := decodeRouteEventIndices(data, nil, neighborEventIndex); ok || len(indices) != 0 {
				t.Fatalf("bad datagram delivered %v, accepted %v", indices, ok)
			}
		})
	}
}

func TestLinuxNeighborEventsRequireKernelMulticastSender(t *testing.T) {
	peer := &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_NEIGH}
	if !kernelRouteEventSender(peer, unix.RTMGRP_NEIGH) || kernelRouteEventSender(peer, unix.RTMGRP_LINK) {
		t.Fatal("neighbor multicast was not confined to its own group")
	}
	peer.Pid = 1
	if kernelRouteEventSender(peer, unix.RTMGRP_NEIGH) {
		t.Fatal("accepted userspace sender")
	}
}

func TestLinuxNeighborEventsSubscriberBoundAndCancelable(t *testing.T) {
	assertRouteEventSubscription(t, unix.RTMGRP_NEIGH, neighborEventIndex)
}

func neighborNotification(kind uint16, family, flags byte, index int32, attributes []byte) []byte {
	payload := make([]byte, unix.SizeofNdMsg)
	payload[0], payload[10] = family, flags
	binary.NativeEndian.PutUint32(payload[4:8], uint32(index))
	return tcMessage(kind, 0, 0, append(payload, attributes...))
}
