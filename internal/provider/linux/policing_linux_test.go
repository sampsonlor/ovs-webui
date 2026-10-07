//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func tcAttr(kind uint16, payload []byte) []byte {
	b := make([]byte, align4(4+len(payload)))
	binary.NativeEndian.PutUint16(b[:2], uint16(4+len(payload)))
	binary.NativeEndian.PutUint16(b[2:4], kind)
	copy(b[4:], payload)
	return b
}
func tcWord(value uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, value)
	return b
}
func tcWide(value uint64) []byte {
	b := make([]byte, 8)
	binary.NativeEndian.PutUint64(b, value)
	return b
}
func tcRow(kind string, options []byte) []byte {
	b := make([]byte, 20)
	binary.NativeEndian.PutUint32(b[4:8], 7)
	binary.NativeEndian.PutUint32(b[8:12], 0xffff0000)
	binary.NativeEndian.PutUint32(b[12:16], 0xffff0000)
	binary.NativeEndian.PutUint32(b[16:20], 49<<16|3)
	b = append(b, tcAttr(1, append([]byte(kind), 0))...)
	return append(b, tcAttr(2, options)...)
}
func tcPolice() []byte {
	b := make([]byte, 56)
	binary.NativeEndian.PutUint32(b[:4], 123)
	binary.NativeEndian.PutUint32(b[4:8], 2)
	binary.NativeEndian.PutUint32(b[28:32], 125000)
	return append(tcAttr(1, b), tcAttr(5, tcWord(0xffffffff))...)
}
func tcTable(police []byte) []byte {
	entry := append(tcAttr(1, []byte("police\x00")), tcAttr(2, police)...)
	return tcAttr(1, entry)
}

func TestLinuxPolicingNativeLayoutsAndExactRates(t *testing.T) {
	for _, kind := range []string{"basic", "matchall", "u32"} {
		key := map[string]uint16{"basic": 3, "matchall": 2, "u32": 7}[kind]
		police := append(tcPolice(), tcAttr(8, tcWide(18446744073709551615))...)
		police = append(police, tcAttr(10, tcWide(5000))...)
		rows, partial, err := filterPolicing(tcRow(kind, tcAttr(key, tcTable(police))))
		if err != nil || partial || len(rows) != 1 || *rows[0].BytesPerSecond != "18446744073709551615" || *rows[0].PacketsPerSecond != "5000" || rows[0].ExceedAction != "drop" || rows[0].ConformAction != "continue" || rows[0].Index != 123 || rows[0].Priority != 49 || rows[0].FilterKind != kind {
			t.Fatal(kind, rows, partial, err)
		}
	}
	for _, kind := range []string{"basic", "u32"} {
		key := map[string]uint16{"basic": 4, "u32": 6}[kind]
		rows, partial, err := filterPolicing(tcRow(kind, tcAttr(key, tcPolice())))
		if err != nil || partial || len(rows) != 1 || *rows[0].BytesPerSecond != "125000" || rows[0].PacketsPerSecond != nil {
			t.Fatal(rows, partial, err)
		}
	}
}

func TestLinuxPolicingUnsupportedCoverageIsNeverAbsence(t *testing.T) {
	for _, parent := range []uint32{0xffff0000, 0xfffffff1, 0xfffffff2} {
		row := tcRow("matchall", nil)
		binary.NativeEndian.PutUint32(row[12:16], parent)
		if !filterBinding(row, 7, 0xffff0000) {
			t.Fatal("same ingress block alias rejected", parent)
		}
		if filterBinding(row, 8, 0xffff0000) || filterBinding(row, 7, 0xfffffff2) != (parent == 0xfffffff2) {
			t.Fatal("wrong device or clsact hook accepted", parent)
		}
	}
	wrong := tcRow("matchall", nil)
	binary.NativeEndian.PutUint32(wrong[12:16], 0xfffe0000)
	if filterBinding(wrong, 7, 0xffff0000) || filterBinding(wrong[:19], 7, 0xffff0000) {
		t.Fatal("unrelated qdisc or short message accepted")
	}
	for _, row := range [][]byte{
		tcRow("flower", nil),
		tcRow("basic", tcAttr(3, tcAttr(1, tcAttr(1, []byte("future\x00"))))),
		tcRow("basic", append(tcAttr(3, tcTable(tcPolice())), tcAttr(99, nil)...)),
		tcRow("basic", tcAttr(3, tcTable(append(tcPolice(), tcAttr(4, tcWord(100))...)))),
		tcRow("basic", tcAttr(3, tcTable(append(tcPolice(), tcAttr(12, tcWord(100))...)))),
	} {
		_, partial, err := filterPolicing(row)
		if err != nil || !partial {
			t.Fatal("unsupported semantics reported complete", partial, err)
		}
	}
	qdisc := tcRow("ingress", nil)
	binary.NativeEndian.PutUint32(qdisc[12:16], 0xfffffff1)
	parents, partial, err := ingressParents([][]byte{qdisc}, 7)
	if err != nil || partial || len(parents) != 1 || parents[0] != 0xffff0000 {
		t.Fatal(parents, partial, err)
	}
	clsact := tcRow("clsact", nil)
	parents, partial, err = ingressParents([][]byte{clsact}, 7)
	if err != nil || partial || len(parents) != 1 || parents[0] != 0xfffffff2 {
		t.Fatal(parents, partial, err)
	}
	shared := append(qdisc, tcAttr(13, tcWord(42))...)
	parents, partial, err = ingressParents([][]byte{shared}, 7)
	if err != nil || !partial || len(parents) != 0 {
		t.Fatal("shared block treated as no policing", parents, partial, err)
	}
}

func tcMessage(kind, flags uint16, seq uint32, data []byte) []byte {
	b := make([]byte, align4(16+len(data)))
	binary.NativeEndian.PutUint32(b[:4], uint32(16+len(data)))
	binary.NativeEndian.PutUint16(b[4:6], kind)
	binary.NativeEndian.PutUint16(b[6:8], flags)
	binary.NativeEndian.PutUint32(b[8:12], seq)
	copy(b[16:], data)
	return b
}

func TestLinuxPolicingMalformedAndInterruptedDumps(t *testing.T) {
	good := tcMessage(unix.RTM_NEWTFILTER, unix.NLM_F_MULTI, 1, tcRow("basic", tcAttr(3, tcTable(tcPolice()))))
	done := tcMessage(unix.NLMSG_DONE, unix.NLM_F_MULTI, 1, tcWord(0))
	rows, complete, _, err := tcMessages(append(append([]byte{}, good...), done...), 1, unix.RTM_NEWTFILTER, true)
	if err != nil || !complete || len(rows) != 1 {
		t.Fatal(rows, complete, err)
	}
	_, complete, _, err = tcMessages(good, 1, unix.RTM_NEWTFILTER, true)
	if err != nil || complete {
		t.Fatal("a data message completed a dump", complete, err)
	}
	for _, bad := range [][]byte{
		{1, 2, 3}, good[:len(good)-1],
		tcMessage(unix.RTM_NEWTFILTER, unix.NLM_F_DUMP_INTR, 1, nil),
		tcMessage(unix.RTM_NEWTFILTER, 0, 2, nil),
		tcMessage(unix.RTM_NEWQDISC, 0, 1, nil),
		tcMessage(unix.NLMSG_DONE, 0, 1, tcWord(0xfffffffb)),
		tcMessage(unix.NLMSG_ERROR, 0, 1, nil),
		append(append([]byte{}, done...), good...),
	} {
		if _, _, _, err := tcMessages(bad, 1, unix.RTM_NEWTFILTER, true); err == nil {
			t.Fatal("bad netlink evidence accepted", bad)
		}
	}
	_, _, _, err = tcMessages(tcMessage(unix.NLMSG_ERROR, 0, 1, tcWord(0xffffffff)), 1, unix.RTM_NEWTFILTER, true)
	if !errors.Is(err, unix.EPERM) {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{{1}, {3, 0, 1, 0}, append(tcAttr(1, tcWord(1)), tcAttr(1, tcWord(2))...)} {
		if _, err := tcAttributes(bad); err == nil {
			t.Fatal("malformed attributes accepted", bad)
		}
	}
	for _, police := range [][]byte{tcAttr(1, make([]byte, 55)), append(tcPolice(), tcAttr(8, tcWord(100))...), append(tcPolice(), tcAttr(10, tcWord(100))...)} {
		if _, _, err := policeAction(police); err == nil {
			t.Fatal("malformed police accepted")
		}
	}
}

func TestLinuxPolicingHostBindingBudgetAndCancellation(t *testing.T) {
	link, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	start := time.Now()
	sample := p.ObservePolicing(context.Background(), inventory.DeviceRequest{Name: "lo", IfIndex: link.Index})
	if sample.Availability != "known" || sample.IfIndex != link.Index || sample.ObservedAt.IsZero() || time.Since(start) > time.Second {
		t.Fatal(sample)
	}
	wrong := p.ObservePolicing(context.Background(), inventory.DeviceRequest{Name: "not-real-ovs", IfIndex: link.Index})
	if wrong.Reason != "LINUX_IFINDEX_MISMATCH" || len(wrong.Actions) != 0 {
		t.Fatal(wrong)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sample := p.ObservePolicing(ctx, inventory.DeviceRequest{Name: "lo", IfIndex: link.Index}); sample.Reason != "LINUX_OBSERVATION_CANCELLED" {
		t.Fatal(sample)
	}
	for i := 0; i < cap(p.policingSlots); i++ {
		p.policingSlots <- struct{}{}
	}
	if sample := p.ObservePolicing(context.Background(), inventory.DeviceRequest{Name: "lo", IfIndex: link.Index}); sample.Reason != "LINUX_PROVIDER_BUSY" {
		t.Fatal(sample)
	}
	for len(p.policingSlots) != 0 {
		<-p.policingSlots
	}
	if sample := p.ObservePolicing(context.Background(), inventory.DeviceRequest{Name: "../lo", IfIndex: link.Index}); sample.Reason != "LINUX_DEVICE_NAME_INVALID" {
		t.Fatal(sample)
	}
}
