package ovsdb

import "testing"

func TestOpenFlowRetainsExactBoundedRawFieldsOnlyWhenRequested(t *testing.T) {
	field := []byte{0xff, 0xff, 0x04, 0x06, 0x12, 0x34, 0x56, 0x78, 0x80, 0x00}
	match, truncated, err := decodeOpenFlowMatch(field, true)
	if err != nil || truncated || len(match) != 1 || match[0].Class != 0xffff ||
		match[0].Field != 2 || match[0].RawPayload != "123456788000" {
		t.Fatalf("experimenter field bytes changed: %+v, %t, %v", match, truncated, err)
	}
	match, _, err = decodeOpenFlowMatch(field, false)
	if err != nil || len(match) != 1 || match[0].RawPayload != "" {
		t.Fatalf("metadata-only match kept a raw value: %+v, %v", match, err)
	}
	small := []byte{0x77, 0x01, 0x00, 8, 0x00, 0x03, 0x00, 0xff}
	large := make([]byte, openFlowRawFieldMax+8)
	ofOrder.PutUint16(large, 0x7702)
	ofOrder.PutUint16(large[2:], uint16(len(large)))
	wire := append(append([]byte(nil), small...), large...)
	instructions, truncated, err := decodeOpenFlowInstructions(wire, true)
	if err != nil || !truncated || len(instructions) != 2 ||
		instructions[0].RawPayload != "000300ff" || instructions[1].RawPayload != "" {
		t.Fatalf("raw instructions were altered or cut partway: %+v, %t, %v", instructions, truncated, err)
	}
	instructions, truncated, err = decodeOpenFlowInstructions(wire, false)
	if err != nil || truncated || len(instructions) != 2 || instructions[0].RawPayload != "" {
		t.Fatalf("metadata-only instructions returned raw behavior: %+v, %t, %v", instructions, truncated, err)
	}
}
