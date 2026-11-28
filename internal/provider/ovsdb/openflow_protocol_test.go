package ovsdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const openFlowTestBridge = "34183630-17e5-4254-ba76-000000000001"
const openFlowTestDPID = 0x1000aadbccbbaa33
const openFlowTestCookie = 0xf123456789abcdef

func TestOpenFlowExchangeSendsOnlyFixedReadsAndRetainsMetadata(t *testing.T) {
	body := flowTestPart(0, flowTestRow())
	input := flowTestHandshake(openFlowTestDPID)
	input = append(input, flowTestFrame(4, 19, 3, body)...)
	var output bytes.Buffer
	wire := struct {
		io.Reader
		io.Writer
	}{bytes.NewReader(input), &output}
	rows, truncated, err := exchangeOpenFlow(wire, openFlowTestDPID, openFlowQuery{table: 2, hasCookie: true, cookie: openFlowTestCookie, limit: 4})
	if err != nil || truncated || len(rows) != 1 {
		t.Fatalf("expected one complete flow, got %+v %v %v", rows, truncated, err)
	}
	row := rows[0]
	if row.Table != 2 || row.Priority != 500 || row.Cookie != "0xf123456789abcdef" ||
		row.Packets == nil || *row.Packets != "18446744073709551614" || row.Bytes != nil ||
		len(row.Match) != 1 || row.Match[0] != (OpenFlowMatchField{Class: 0xffff, Field: 7, Masked: false, PayloadBytes: 8}) ||
		len(row.Instructions) != 1 || len(row.Instructions[0].Actions) != 1 || row.Instructions[0].Actions[0] != 17 {
		t.Fatalf("wrong typed fields: %+v", row)
	}
	encoded, _ := json.Marshal(row)
	if strings.Contains(string(encoded), "PRIVATE!") {
		t.Fatal("raw vendor bytes leaked into metadata")
	}
	sent := openFlowReader{r: &output, budget: 256}
	for i, kind := range []byte{0, 5, 18} {
		frame, err := sent.next()
		if err != nil || frame.version != 4 || frame.kind != kind || frame.xid != uint32(i+1) {
			t.Fatalf("unexpected outbound frame: %+v, %v", frame, err)
		}
		if kind == 18 && (len(frame.body) != 48 || frame.body[8] != 2 ||
			ofOrder.Uint32(frame.body[12:16]) != ^uint32(0) || ofOrder.Uint32(frame.body[16:20]) != ^uint32(0) ||
			ofOrder.Uint64(frame.body[24:32]) != openFlowTestCookie || ofOrder.Uint64(frame.body[32:40]) != ^uint64(0) ||
			ofOrder.Uint16(frame.body[42:44]) != 4) {
			t.Fatalf("wrong fixed flow request: %x", frame.body)
		}
	}
	if output.Len() != 0 {
		t.Fatal("unexpected fourth protocol operation")
	}
	if err := sendOpenFlow(&output, 14, 4, nil); err != errOpenFlowInvalid || output.Len() != 0 {
		t.Fatal("flow-mod request was not fenced at wire writer")
	}
	unapprovedMultipart := openFlowRequest(openFlowQuery{table: openFlowAllTables})
	ofOrder.PutUint16(unapprovedMultipart[:2], 12)
	if err := sendOpenFlow(&output, 18, 3, unapprovedMultipart); err != errOpenFlowInvalid || output.Len() != 0 {
		t.Fatal("arbitrary multipart body was not fenced at wire writer")
	}
}

func TestOpenFlowRejectsUnrelatedAndMalformedResponses(t *testing.T) {
	q := openFlowQuery{table: 2, limit: 2}
	row := flowTestRow()
	good := flowTestFrame(4, 19, 3, flowTestPart(0, row))
	short := good[:len(good)-1]
	invalidTLV := bytes.Clone(row)
	invalidTLV[49] = 0 // match type is no longer OXM
	mask := bytes.Clone(row)
	mask[54], mask[55] = 15, 7 // experimenter header, followed by invalid odd mask
	badAction := bytes.Clone(row)
	badAction[len(badAction)-5] = 7
	wrongTable := bytes.Clone(row)
	wrongTable[2] = 3
	for _, tt := range []struct {
		name   string
		prefix []byte
		reply  []byte
		want   error
	}{
		{"different DPID", flowTestHandshake(openFlowTestDPID + 1), good, errOpenFlowIdentity},
		{"old protocol", flowTestFrame(1, 0, 55, nil), nil, errOpenFlowUnsupported},
		{"bitmap omits 1.3", flowTestFrame(6, 0, 55, []byte{0, 1, 0, 8, 0, 0, 0, 64}), nil, errOpenFlowUnsupported},
		{"wrong xid", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 5, flowTestPart(0, row)), errOpenFlowInvalid},
		{"wrong table", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(0, wrongTable)), errOpenFlowInvalid},
		{"short frame", flowTestHandshake(openFlowTestDPID), short, errOpenFlowInvalid},
		{"missing terminal part", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(1, row)), errOpenFlowInvalid},
		{"invalid match", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(0, invalidTLV)), errOpenFlowInvalid},
		{"invalid mask", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(0, mask)), errOpenFlowInvalid},
		{"invalid action", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(0, badAction)), errOpenFlowInvalid},
		{"unknown flags", flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(2, row)), errOpenFlowInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			wire := struct {
				io.Reader
				io.Writer
			}{bytes.NewReader(append(bytes.Clone(tt.prefix), tt.reply...)), &out}
			rows, truncated, err := exchangeOpenFlow(wire, openFlowTestDPID, q)
			if err != tt.want || len(rows) != 0 || truncated {
				t.Fatalf("unexpected evidence: %+v %v %v", rows, truncated, err)
			}
		})
	}
}

func TestOpenFlowDistinguishesExplicitCapsFromCompleteEmptyResult(t *testing.T) {
	row := flowTestRow()
	for _, tt := range []struct {
		name      string
		parts     [][]byte
		count     int
		truncated bool
	}{
		{"empty", [][]byte{flowTestPart(0)}, 0, false},
		{"exact limit", [][]byte{flowTestPart(0, row)}, 1, false},
		{"row limit", [][]byte{flowTestPart(0, row, row)}, 1, true},
		{"more than one frame", [][]byte{flowTestPart(1), flowTestPart(0, row)}, 1, false},
		{"multipart cap", bytesPartsForTest(), 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := flowTestHandshake(openFlowTestDPID)
			for _, part := range tt.parts {
				input = append(input, flowTestFrame(4, 19, 3, part)...)
			}
			wire := struct {
				io.Reader
				io.Writer
			}{bytes.NewReader(input), io.Discard}
			rows, truncated, err := exchangeOpenFlow(wire, openFlowTestDPID, openFlowQuery{table: openFlowAllTables, limit: 1})
			if err != nil || rows == nil || len(rows) != tt.count || truncated != tt.truncated {
				t.Fatalf("unexpected limits: %+v %v %v", rows, truncated, err)
			}
		})
	}
	reader := openFlowReader{r: bytes.NewReader(flowTestFrame(4, 19, 3, flowTestPart(0, row))), budget: 16}
	if rows, truncated, err := readOpenFlowRows(&reader, openFlowQuery{limit: 1}); err != nil || !truncated || len(rows) != 0 {
		t.Fatalf("wire limit did not stop the sample: %+v %v %v", rows, truncated, err)
	}
}

func TestOpenFlowObserverValidatesBeforeResolutionAndPreservesCancellation(t *testing.T) {
	entered := make(chan struct{})
	o := NewOpenFlowObserver(func(ctx context.Context, _ string) (OpenFlowBinding, error) {
		close(entered)
		<-ctx.Done()
		return OpenFlowBinding{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := o.ObserveFlows(ctx, OpenFlowQuery{BridgeID: "/tmp/another.mgmt", Limit: 1}); err == nil {
		t.Fatal("unexpected public socket selector accepted")
	}
	query := OpenFlowQuery{BridgeID: openFlowTestBridge, Limit: 1}
	finished := make(chan error, 1)
	go func() {
		_, err := o.ObserveFlows(ctx, query)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("observer did not start its first call")
	}
	_, err := o.ObserveFlows(ctx, query)
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Code != "OPENFLOW_PROVIDER_BUSY" {
		t.Fatalf("expected bounded overlap rejection: %v", err)
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not preserved: %v", err)
	}
}

func TestOpenFlowMetadataAndSerializedByteBudgets(t *testing.T) {
	fields := make([]byte, 4)
	for i := 0; i < 2*openFlowMetadata; i++ {
		fields = append(fields, 0x80, 0, byte(i<<1), 1, 1)
	}
	ofOrder.PutUint16(fields[:2], 1)
	ofOrder.PutUint16(fields[2:4], uint16(len(fields)))
	fields = append(fields, make([]byte, alignedOpenFlow(len(fields))-len(fields))...)
	row := append(bytes.Clone(flowTestRow()[:48]), fields...)
	ofOrder.PutUint16(row[:2], uint16(len(row)))
	got, _, err := decodeOpenFlowRow(row, false)
	if err != nil || !got.MetadataTruncated || len(got.Match) != openFlowMetadata {
		t.Fatalf("metadata budget failed: %+v, %v", got, err)
	}
	all := make([][]byte, MaxOpenFlowRows)
	for i := range all {
		all[i] = row
	}
	frame := flowTestFrame(4, 19, 3, flowTestPart(0, all...))
	reader := openFlowReader{r: bytes.NewReader(frame), budget: openFlowWireBytes}
	rows, truncated, err := readOpenFlowRows(&reader, openFlowQuery{table: openFlowAllTables, limit: MaxOpenFlowRows})
	encoded, marshalErr := json.Marshal(rows)
	if err != nil || marshalErr != nil || !truncated || len(rows) == 0 || len(rows) >= MaxOpenFlowRows || len(encoded) > MaxOpenFlowBytes {
		t.Fatalf("serialized byte limit failed on %d rows and %d bytes: %v", len(rows), len(encoded), err)
	}
}

func bytesPartsForTest() [][]byte {
	parts := make([][]byte, openFlowReplyParts)
	for i := range parts {
		parts[i] = flowTestPart(1)
	}
	return parts
}

func flowTestFrame(version, kind byte, xid uint32, body []byte) []byte {
	header := []byte{version, kind, 0, 0, 0, 0, 0, 0}
	ofOrder.PutUint16(header[2:4], uint16(len(header)+len(body)))
	ofOrder.PutUint32(header[4:], xid)
	return append(header, body...)
}

func flowTestHandshake(datapath uint64) []byte {
	hello := flowTestFrame(6, 0, 55, []byte{0, 1, 0, 8, 0, 0, 0, 80})
	features := make([]byte, 24)
	ofOrder.PutUint64(features[:8], datapath)
	features[12] = 4
	return append(hello, flowTestFrame(4, 6, 2, features)...)
}

func flowTestPart(flags uint16, rows ...[]byte) []byte {
	header := make([]byte, 8)
	ofOrder.PutUint16(header[:2], 1)
	ofOrder.PutUint16(header[2:4], flags)
	for _, row := range rows {
		header = append(header, row...)
	}
	return header
}

func flowTestRow() []byte {
	row := make([]byte, 48)
	row[2] = 2
	ofOrder.PutUint32(row[4:8], 10)
	ofOrder.PutUint32(row[8:12], 999)
	ofOrder.PutUint16(row[12:14], 500)
	ofOrder.PutUint64(row[24:32], openFlowTestCookie)
	ofOrder.PutUint64(row[32:40], ^uint64(0)-1)
	ofOrder.PutUint64(row[40:48], ^uint64(0))
	row = append(row, 0, 1, 0, 16, 0xff, 0xff, 14, 8)
	row = append(row, []byte("PRIVATE!")...)
	row = append(row, 0, 4, 0, 16, 0, 0, 0, 0, 0, 17, 0, 8, 0x81, 0, 0, 0)
	ofOrder.PutUint16(row[:2], uint16(len(row)))
	return row
}
