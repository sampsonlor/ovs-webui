//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func neighborFixture(family byte, index int, state uint16, flags byte, ip, mac []byte) []byte {
	row := make([]byte, unix.SizeofNdMsg)
	row[0], row[10], row[11] = family, flags, unix.RTN_UNICAST
	binary.NativeEndian.PutUint32(row[4:8], uint32(index))
	binary.NativeEndian.PutUint16(row[8:10], state)
	if ip != nil {
		row = append(row, tcAttr(unix.NDA_DST, ip)...)
	}
	if mac != nil {
		row = append(row, tcAttr(unix.NDA_LLADDR, mac)...)
	}
	return row
}

func TestLinuxNeighborDecodesScopedARPAndNDP(t *testing.T) {
	eth := inventory.DeviceRequest{Name: "ovs-test1", IfIndex: 42}
	scope := map[int]neighborLink{42: {device: eth, hardware: unix.ARPHRD_ETHER}}
	mac := []byte{2, 0, 0, 0, 0, 2}
	ip4 := netip.MustParseAddr("192.0.2.2")
	ip6 := netip.MustParseAddr("fe80::a05")
	for _, tc := range []struct {
		name   string
		family byte
		ip     netip.Addr
		state  uint16
		label  string
		valid  bool
	}{
		{"arp-reachable", unix.AF_INET, ip4, unix.NUD_REACHABLE, "reachable", true},
		{"ndp-stale", unix.AF_INET6, ip6, unix.NUD_STALE, "stale", true},
		{"delay", unix.AF_INET6, ip6, unix.NUD_DELAY, "delay", true},
		{"probe", unix.AF_INET, ip4, unix.NUD_PROBE, "probe", true},
		{"permanent", unix.AF_INET, ip4, unix.NUD_PERMANENT, "permanent", true},
		{"noarp", unix.AF_INET6, ip6, unix.NUD_NOARP, "noarp", true},
		{"failed", unix.AF_INET, ip4, unix.NUD_FAILED, "failed", false},
		{"incomplete", unix.AF_INET6, ip6, unix.NUD_INCOMPLETE, "incomplete", false},
		{"absent-state", unix.AF_INET, ip4, 0, "unknown", false},
		{"ambiguous-state", unix.AF_INET6, ip6, unix.NUD_FAILED | unix.NUD_STALE, "unknown", false},
		{"future-state", unix.AF_INET6, ip6, 0x100, "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, present, err := decodeNeighbor(neighborFixture(tc.family, 42, tc.state, 0, tc.ip.AsSlice(), mac), tc.family, scope)
			if err != nil || !present || got.Interface != eth || got.IP != tc.ip.String() || got.State != tc.label {
				t.Fatal(got, present, err)
			}
			if tc.valid && (got.MAC == nil || *got.MAC != "02:00:00:00:00:02") || !tc.valid && got.MAC != nil {
				t.Fatal("unexpected link-layer evidence", got)
			}
		})
	}
}

func TestLinuxNeighborWithholdsForeignAndAmbiguousEvidence(t *testing.T) {
	device := inventory.DeviceRequest{Name: "ovs-test1", IfIndex: 42}
	scope := map[int]neighborLink{42: {device: device, hardware: unix.ARPHRD_ETHER}}
	ip := netip.MustParseAddr("192.0.2.5").AsSlice()
	mac := []byte{2, 0, 0, 0, 0, 2}
	for _, row := range [][]byte{
		neighborFixture(unix.AF_INET, 999, unix.NUD_REACHABLE, 0, nil, nil), // Scope before parsing attributes.
		neighborFixture(unix.AF_INET, 42, unix.NUD_NONE, unix.NTF_PROXY, ip, nil),
		neighborFixture(unix.AF_INET, 42, unix.NUD_NOARP, 0, []byte{224, 0, 0, 1}, nil),
		neighborFixture(unix.AF_INET, 42, unix.NUD_NOARP, 0, []byte{255, 255, 255, 255}, nil),
		neighborFixture(unix.AF_INET, 42, unix.NUD_NOARP, 0, make([]byte, 4), nil),
	} {
		got, present, err := decodeNeighbor(row, unix.AF_INET, scope)
		if err != nil || present || got.IP != "" || got.MAC != nil {
			t.Fatal("unscoped neighbor was exposed", got, present, err)
		}
	}
	for _, unusable := range [][]byte{nil, {2, 0, 0, 0, 0}, make([]byte, 6), {1, 0, 0, 0, 0, 2}} {
		got, present, err := decodeNeighbor(neighborFixture(unix.AF_INET, 42, unix.NUD_REACHABLE, 0, ip, unusable), unix.AF_INET, scope)
		if err != nil || !present || got.MAC != nil {
			t.Fatal("non-Ethernet address was qualified", got, present, err)
		}
	}
	wrongHardware := map[int]neighborLink{42: {device: device, hardware: unix.ARPHRD_LOOPBACK}}
	got, present, err := decodeNeighbor(neighborFixture(unix.AF_INET, 42, unix.NUD_REACHABLE, 0, ip, mac), unix.AF_INET, wrongHardware)
	if err != nil || !present || got.MAC != nil {
		t.Fatal("address qualified without Ethernet link type", got, present, err)
	}
	good := neighborFixture(unix.AF_INET, 42, unix.NUD_REACHABLE, 0, ip, mac)
	for _, row := range [][]byte{
		good[:11], append(slices.Clone(good), tcAttr(unix.NDA_DST, ip)...),
		neighborFixture(unix.AF_INET6, 42, unix.NUD_REACHABLE, 0, ip, mac),
		neighborFixture(unix.AF_INET, 42, unix.NUD_REACHABLE, 0, nil, mac),
		neighborFixture(unix.AF_INET, 42, unix.NUD_REACHABLE, 0, ip[:3], mac),
	} {
		if _, _, err := decodeNeighbor(row, unix.AF_INET, scope); err == nil || err.Error() != "NEIGHBORS_DUMP_UNPROVEN" {
			t.Fatal("misframed eligible row accepted", err)
		}
	}
}

func TestLinuxNeighborDiscardsReplacedLink(t *testing.T) {
	changed := inventory.DeviceRequest{Name: "ovs-test1", IfIndex: 42}
	stable := inventory.DeviceRequest{Name: "ovs-test2", IfIndex: 43}
	afterOnly := inventory.DeviceRequest{Name: "ovs-test3", IfIndex: 44}
	newName := inventory.DeviceRequest{Name: "other-name", IfIndex: 42}
	a, b := neighborLink{device: changed, hardware: unix.ARPHRD_ETHER}, neighborLink{device: stable, hardware: unix.ARPHRD_ETHER}
	result := inventory.NeighborObservation{Neighbors: []inventory.Neighbor{{Interface: changed}, {Interface: stable}}}
	before := map[int]neighborLink{42: a, 43: b}
	after := map[int]neighborLink{42: {device: newName, hardware: unix.ARPHRD_ETHER}, 43: b, 44: {device: afterOnly, hardware: unix.ARPHRD_ETHER}}
	retainConfirmedNeighbors(&result, []inventory.DeviceRequest{changed, stable, afterOnly}, before, after)
	if !slices.Equal(result.Covered, []inventory.DeviceRequest{stable}) || len(result.Neighbors) != 1 || result.Neighbors[0].Interface != stable {
		t.Fatal("missing/replaced device influenced scoped observation", result)
	}
}

func TestLinuxNeighborMultipartCompletionAndBudgets(t *testing.T) {
	const seq = 17
	msg := tcMessage(unix.RTM_NEWNEIGH, unix.NLM_F_MULTI, seq, []byte{2, 3, 4})
	done := tcMessage(unix.NLMSG_DONE, unix.NLM_F_MULTI, seq, tcWord(0))
	// Kernel dump replies echo the destination's local netlink port ID here.
	binary.NativeEndian.PutUint32(msg[12:16], 19442)
	binary.NativeEndian.PutUint32(done[12:16], 19442)
	count := 0
	visit := func(data []byte) error {
		count++
		if !slices.Equal(data, []byte{2, 3, 4}) {
			t.Fatal(data)
		}
		return nil
	}
	route := neighborRoute{sequence: seq}
	if complete, err := route.consume(msg, unix.RTM_NEWNEIGH, visit); complete || err != nil || count != 1 {
		t.Fatal(complete, count, err)
	}
	if complete, err := route.consume(append(slices.Clone(msg), done...), unix.RTM_NEWNEIGH, visit); !complete || err != nil || count != 2 {
		t.Fatal(complete, count, err)
	}
	for _, bad := range [][]byte{
		{1, 2}, msg[:len(msg)-1], append(slices.Clone(msg), 1), append(slices.Clone(done), msg...),
		tcMessage(unix.RTM_NEWNEIGH, 0, seq, nil),
		tcMessage(unix.RTM_NEWNEIGH, unix.NLM_F_MULTI|unix.NLM_F_DUMP_INTR, seq, nil),
		tcMessage(unix.RTM_NEWNEIGH, unix.NLM_F_MULTI|unix.NLM_F_DUMP_FILTERED, seq, nil),
		tcMessage(unix.RTM_NEWLINK, unix.NLM_F_MULTI, seq, nil),
		tcMessage(unix.RTM_NEWNEIGH, unix.NLM_F_MULTI, seq+1, nil),
		tcMessage(unix.NLMSG_DONE, 0, seq, nil),
		tcMessage(unix.NLMSG_DONE, unix.NLM_F_MULTI, seq, tcWord(0xfffffffa)),
		tcMessage(unix.NLMSG_ERROR, 0, seq, tcWord(0)),
	} {
		route := neighborRoute{sequence: seq}
		if complete, err := route.consume(bad, unix.RTM_NEWNEIGH, visit); complete || err == nil || err.Error() != "NEIGHBORS_DUMP_UNPROVEN" {
			t.Fatal("malformed or interrupted dump accepted", complete, err)
		}
	}
	errorReply := tcMessage(unix.NLMSG_ERROR, 0, seq, tcWord(0xffffffff))
	if _, err := route.consume(errorReply, unix.RTM_NEWNEIGH, visit); err == nil || err.Error() != "NEIGHBORS_DUMP_UNAVAILABLE" {
		t.Fatal("kernel failure was lost", err)
	}
	for _, route := range []neighborRoute{
		{sequence: seq, bytes: neighborDumpBytes - 1}, {sequence: seq, messages: neighborDumpMessages},
	} {
		if complete, err := route.consume(done, unix.RTM_NEWNEIGH, visit); complete || err == nil || err.Error() != "NEIGHBORS_DUMP_BUDGET" {
			t.Fatal("budget overrun accepted", complete, err)
		}
	}
}

func TestLinuxNeighborHostScope(t *testing.T) {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise real kernel multipart framing without creating a neighbor or
	// publishing any other host interface from the unfiltered kernel dumps.
	provider := New()
	request := inventory.DeviceRequest{Name: lo.Name, IfIndex: lo.Index}
	start := time.Now()
	observation, err := provider.ObserveNeighbors(t.Context(), []inventory.DeviceRequest{request})
	if err != nil || !slices.Equal(observation.Covered, []inventory.DeviceRequest{request}) || observation.ObservedAt.Before(start) || observation.Truncated {
		t.Fatal("real kernel observation failed", observation, err)
	}
	for _, found := range observation.Neighbors {
		if found.Interface != request || found.MAC != nil {
			t.Fatal("loopback scope advertised other link evidence", found)
		}
	}
	missing := inventory.DeviceRequest{Name: "synthetic-lo", IfIndex: lo.Index}
	observation, err = provider.ObserveNeighbors(t.Context(), []inventory.DeviceRequest{missing})
	if err != nil || len(observation.Covered) != 0 || len(observation.Neighbors) != 0 {
		t.Fatal("wrong name acquired host binding", observation, err)
	}
	for _, invalid := range [][]inventory.DeviceRequest{
		{request, request}, {request, missing}, {{Name: "bad/path", IfIndex: lo.Index}}, {{Name: lo.Name}},
		{request, {Name: request.Name, IfIndex: request.IfIndex + 1}},
	} {
		observation, err = provider.ObserveNeighbors(t.Context(), invalid)
		if err == nil || err.Error() != "NEIGHBORS_INVALID_BINDINGS" || !observation.ObservedAt.IsZero() {
			t.Fatal("ambiguous scope acquired host binding", invalid, observation, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	observation, err = provider.ObserveNeighbors(ctx, []inventory.DeviceRequest{request})
	if err == nil || err.Error() != "NEIGHBORS_DUMP_TIMEOUT" || !observation.ObservedAt.IsZero() {
		t.Fatal("cancelled collection returned evidence", observation, err)
	}
	if _, err := neighborScope(make([]inventory.DeviceRequest, inventory.MaxRows+1)); err == nil || err.Error() != "NEIGHBORS_DUMP_BUDGET" {
		t.Fatal("oversized scope was admitted", err)
	}
}
