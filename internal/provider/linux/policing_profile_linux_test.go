//go:build linux

package linux

import (
	"encoding/binary"
	"testing"
)

func qualifiedFilter() []byte {
	entry := append(tcAttr(1, []byte("police\x00")), tcAttr(2, tcPolice())...)
	entry = append(entry, tcAttr(10, tcWord(0))...)
	row := tcRow("matchall", tcAttr(2, tcAttr(1, entry)))
	binary.NativeEndian.PutUint32(row[8:12], 1)
	binary.NativeEndian.PutUint32(row[16:20], 1<<16|3)
	return row
}
func TestLinuxPolicingWriteProfileRejectsSelectorsExtraActionsAndOffload(t *testing.T) {
	qdisc := tcRow("ingress", nil)
	binary.NativeEndian.PutUint32(qdisc[12:16], 0xfffffff1)
	if kind, ok := policingIngressProfile([][]byte{qdisc}, 7); !ok || kind != "ingress" {
		t.Fatal(kind, ok)
	}
	if kind, ok := policingIngressProfile(nil, 7); !ok || kind != "none" {
		t.Fatal(kind, ok)
	}
	for _, attr := range [][]byte{tcAttr(13, tcWord(1)), tcAttr(12, []byte{1}), tcAttr(99, nil)} {
		bad := append(append([]byte{}, qdisc...), attr...)
		if _, ok := policingIngressProfile([][]byte{bad}, 7); ok {
			t.Fatal("foreign qdisc semantics")
		}
	}
	row := qualifiedFilter()
	actions, partial, err := filterPolicing(row)
	if err != nil || partial || len(actions) != 1 {
		t.Fatal(actions, partial, err)
	}
	if _, ok := policingFilterProfile(row, actions); !ok {
		t.Fatal("canonical software action")
	}
	for _, kind := range []string{"priority", "handle", "chain", "classid", "offload", "cookie", "hw-count", "second-action"} {
		bad := qualifiedFilter()
		switch kind {
		case "priority":
			binary.NativeEndian.PutUint32(bad[16:20], 2<<16|3)
		case "handle":
			binary.NativeEndian.PutUint32(bad[8:12], 2)
		case "chain":
			bad = append(bad, tcAttr(11, tcWord(1))...)
		default:
			entry := append(tcAttr(1, []byte("police\x00")), tcAttr(2, tcPolice())...)
			if kind == "cookie" {
				entry = append(entry, tcAttr(6, []byte("other-tool"))...)
			}
			if kind == "hw-count" {
				entry = append(entry, tcAttr(10, tcWord(1))...)
			}
			table := tcAttr(1, entry)
			if kind == "second-action" {
				table = append(table, tcAttr(2, entry)...)
			}
			opts := tcAttr(2, table)
			if kind == "classid" {
				opts = append(opts, tcAttr(1, tcWord(1))...)
			}
			if kind == "offload" {
				opts = append(opts, tcAttr(3, tcWord(4))...)
			}
			bad = tcRow("matchall", opts)
			binary.NativeEndian.PutUint32(bad[8:12], 1)
			binary.NativeEndian.PutUint32(bad[16:20], 1<<16|3)
		}
		if _, ok := policingFilterProfile(bad, actions); ok {
			t.Fatal(kind)
		}
	}
}
func TestLinuxPolicingKernelFingerprintIgnoresCountersButSealsNativeBurst(t *testing.T) {
	row := qualifiedFilter()
	before, _, err := filterPolicing(row)
	if err != nil {
		t.Fatal(err)
	}
	tbf := make([]byte, 56)
	binary.NativeEndian.PutUint32(tbf[:4], 123)
	binary.NativeEndian.PutUint32(tbf[4:8], 2)
	binary.NativeEndian.PutUint32(tbf[28:32], 125000)
	binary.NativeEndian.PutUint32(tbf[44:48], 17)
	binary.NativeEndian.PutUint32(tbf[48:52], 4)
	params := append(tcAttr(1, tbf), tcAttr(5, tcWord(0xffffffff))...)
	makeRow := func(p []byte) []byte { return tcRow("matchall", tcAttr(2, tcTable(p))) }
	now, _, err := filterPolicing(makeRow(params))
	if err != nil || before[0].ParameterDigest != now[0].ParameterDigest {
		t.Fatal("transient reference counters changed fingerprint", err)
	}
	binary.NativeEndian.PutUint32(tbf[12:16], 1234)
	changed, _, err := filterPolicing(makeRow(append(tcAttr(1, tbf), tcAttr(5, tcWord(0xffffffff))...)))
	if err != nil || before[0].ParameterDigest == changed[0].ParameterDigest {
		t.Fatal("burst not sealed", err)
	}
}
