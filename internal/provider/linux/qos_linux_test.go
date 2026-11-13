//go:build linux

package linux

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func TestLinuxQoSClassBindingAndExactCounters(t *testing.T) {
	row := func(kind string, handle, parent uint32, options []byte) []byte {
		b := tcRow(kind, options)
		binary.NativeEndian.PutUint32(b[8:12], handle)
		binary.NativeEndian.PutUint32(b[12:16], parent)
		return b
	}
	first := make([]byte, 20)
	second := make([]byte, 20)
	binary.NativeEndian.PutUint32(second[16:], 7)
	a, err := findEgressRoot([][]byte{row("htb", qosRootHandle, qosRootParent, tcAttr(htbInit, first))}, 7)
	if err != nil || a.kind != "htb" || a.offloaded {
		t.Fatal("invalid root", a, err)
	}
	b, err := findEgressRoot([][]byte{row("htb", qosRootHandle, qosRootParent, tcAttr(htbInit, second))}, 7)
	if err != nil || a != b {
		t.Fatal("live root counter invalidated stable scheduler evidence", a, b, err)
	}
	offload := append(tcAttr(htbInit, first), tcAttr(htbOffload, nil)...)
	b, err = findEgressRoot([][]byte{row("htb", qosRootHandle, qosRootParent, offload)}, 7)
	if err != nil || !b.offloaded {
		t.Fatal("offloaded root reported as software", b, err)
	}
	for _, kind := range []string{"htb", "hfsc"} {
		req := inventory.QoSRuntimeRequest{Device: inventory.DeviceRequest{IfIndex: 7}, Kind: kind, QueueIDs: []uint32{0, 7}}
		root := row(kind, qosParentClass, qosRootParent, nil)
		queue0 := row(kind, qosRootHandle+1, qosParentClass, nil)
		basic := append(tcWide(18446744073709551615), tcWord(3)...)
		queueStats := make([]byte, 20)
		stats := append(tcAttr(statsBasic, basic), tcAttr(statsPacket64, tcWide(4294967299))...)
		stats = append(stats, tcAttr(statsQueue, queueStats)...)
		queue7 := append(row(kind, qosRootHandle+8, qosParentClass, nil), tcAttr(unix.TCA_STATS2, stats)...)
		got, err := readQoSClasses([][]byte{queue7, root, queue0}, req)
		if err != nil || len(got) != 2 || got[0].QueueID != 0 || got[0].Packets != nil || got[1].QueueID != 7 {
			t.Fatal("wrong class mapping or invented counter", got, err)
		}
		want := []string{"18446744073709551615", "4294967299", "0"}
		if q := got[1]; q.Bytes == nil || q.Packets == nil || q.Drops == nil || !reflect.DeepEqual([]string{*q.Bytes, *q.Packets, *q.Drops}, want) {
			t.Fatal("64-bit precision or zero lost", q)
		}
		got, err = readQoSClasses([][]byte{queue7}, req)
		if err != nil || len(got) != 0 {
			t.Fatal("reported queue without its parent", got, err)
		}
		if _, err := readQoSClasses([][]byte{queue7, root, queue7}, req); err == nil {
			t.Fatal("accepted duplicate classes")
		}
	}
	if _, err := qosCounters(7, tcAttr(statsBasic, make([]byte, 8))); err == nil {
		t.Fatal("accepted truncated statistics")
	}
}
