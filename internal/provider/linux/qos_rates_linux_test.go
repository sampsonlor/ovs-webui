//go:build linux

package linux

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestLinuxQoSReadsOnlyCompleteClassRates(t *testing.T) {
	htb := make([]byte, 16)
	binary.NativeEndian.PutUint32(htb[8:12], 1)
	hfsc := make([]byte, 2)
	binary.NativeEndian.PutUint16(hfsc, 1)
	row := func(kind string, handle, parent uint32, options []byte) []byte {
		b := tcRow(kind, options)
		binary.NativeEndian.PutUint32(b[8:12], handle)
		binary.NativeEndian.PutUint32(b[12:16], parent)
		return b
	}
	for _, kind := range []string{"htb", "hfsc"} {
		t.Run(kind, func(t *testing.T) {
			opts := htb
			rates := htbClassOptions
			if kind == "hfsc" {
				opts, rates = hfsc, hfscClassOptions
			}
			req := inventory.QoSRuntimeRequest{Device: inventory.DeviceRequest{IfIndex: 7}, Kind: kind, QueueIDs: []uint32{0, 7}}
			root := egressRoot{kind: kind, handle: qosRootHandle, options: string(opts)}
			parent := row(kind, qosParentClass, qosRootHandle, rates(1000000, 1000000))
			queue0 := row(kind, qosRootHandle+1, qosParentClass, rates(1500, 1000000))
			queue7 := row(kind, qosRootHandle+8, qosParentClass, rates(125000, 375000))
			rows := [][]byte{queue7, queue0, parent}
			if kind == "hfsc" {
				rows = append(rows, row(kind, qosRootHandle, qosRootParent, nil))
			}
			got := readQoSClassRates(rows, req, root)
			if got == nil || got.MaximumBPS != "8000000" || len(got.Queues) != 2 ||
				got.Queues[0].MinimumBPS != "12000" || got.Queues[1] != (inventory.QoSInstalledQueueRate{QueueID: 7, MinimumBPS: "1000000", MaximumBPS: "3000000"}) {
				t.Fatalf("unexpected rates: %+v", got)
			}
			for _, unexpected := range [][][]byte{
				{parent, queue0},
				{parent, queue0, queue7, queue7},
				{parent, queue0, queue7, row(kind, qosRootHandle+9, qosParentClass, rates(1, 2))},
				{parent, queue0, row(kind, qosRootHandle+8, qosRootHandle+1, rates(125000, 375000))},
				{parent, queue0, row(kind, qosRootHandle+8, qosParentClass, rates(125000, 1000001))},
			} {
				if got := readQoSClassRates(unexpected, req, root); got != nil {
					t.Fatalf("published rates for incomplete or unexpected tree: %+v", got)
				}
			}
			root.options = string(make([]byte, len(opts)))
			if got := readQoSClassRates(rows, req, root); got != nil {
				t.Fatalf("accepted a different default class: %+v", got)
			}
		})
	}
}

func TestLinuxQoSClassRatesRejectUnsupportedNativeOptions(t *testing.T) {
	wire := append(htbClassOptions(125000, math.MaxUint32), tcAttr(htbCeiling64, tcWide(4294967297))...)
	if got := parseClassRates("htb", wire); got != (classRates{125000, 4294967297}) {
		t.Fatal("lost 64-bit HTB ceiling", got)
	}
	for _, data := range [][]byte{
		tcAttr(htbParameters, make([]byte, 40)),
		append(wire, tcAttr(htbOffload, nil)...),
	} {
		if got := parseClassRates("htb", data); got != (classRates{}) {
			t.Fatal("accepted malformed or offloaded HTB class", got)
		}
	}
	hfsc := hfscClassOptions(125000, 375000)
	hfsc[4] = 1 // nonlinear first segment of the real-time curve
	if got := parseClassRates("hfsc", hfsc); got != (classRates{}) {
		t.Fatal("flattened a nonlinear HFSC class", got)
	}
}

func htbClassOptions(minimum, maximum uint32) []byte {
	options := make([]byte, 44)
	binary.NativeEndian.PutUint32(options[8:12], minimum)
	binary.NativeEndian.PutUint32(options[20:24], maximum)
	return tcAttr(htbParameters, options)
}

func hfscClassOptions(minimum, maximum uint32) []byte {
	curve := func(id uint16, rate uint32) []byte {
		b := make([]byte, 12)
		binary.NativeEndian.PutUint32(b[8:], rate)
		return tcAttr(id, b)
	}
	opts := append(curve(hfscRealtime, minimum), curve(hfscLinkShare, minimum)...)
	return append(opts, curve(hfscUpper, maximum)...)
}
