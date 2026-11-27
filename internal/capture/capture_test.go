package capture

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestCaptureHeaderLimitsAndProfiles(t *testing.T) {
	r := Request{InterfaceID: "11111111-1111-4111-8111-111111111111", Seconds: 30, MaxBytes: MaxBytes, Profile: ARP}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Profile = "caller-supplied filter"
	if err := r.Validate(); err == nil {
		t.Fatal("unapproved filter was accepted")
	}
	r.Profile = ARP
	r.MaxBytes++
	if err := r.Validate(); err == nil {
		t.Fatal("oversize capture was accepted")
	}
	if len(initialPCAP()) != headerSize || binary.LittleEndian.Uint32(initialPCAP()[16:]) != frameBytes {
		t.Fatal("PCAP global header has the wrong snap length")
	}
}

func TestCaptureRecordNeverIncludesPacketPayload(t *testing.T) {
	original := append(bytes.Repeat([]byte{0x21}, frameBytes), []byte("synthetic never exported")...)
	original[12], original[13] = 8, 6
	now := time.Unix(1000, 123456000)
	base := initialPCAP()
	if _, ok := emitEthernetHeader(base, now, original, len(original), ARP.etherType()); ok {
		t.Fatal("unbounded buffer was accepted as an Ethernet header")
	}
	record, ok := emitEthernetHeader(base, now, original[:frameBytes], len(original), ARP.etherType())
	if !ok || len(record) != headerSize+recordSize || bytes.Contains(record, original[frameBytes:]) ||
		binary.LittleEndian.Uint32(record[headerSize+8:]) != frameBytes ||
		binary.LittleEndian.Uint32(record[headerSize+12:]) != uint32(len(original)) {
		t.Fatalf("incorrectly bounded capture record: %x", record)
	}
	if _, ok := emitEthernetHeader(base, now, original[:frameBytes], len(original), LLDP.etherType()); ok {
		t.Fatal("unrelated protocol was accepted")
	}
}
