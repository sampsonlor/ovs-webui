package ovsdb

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestOpenFlowObservesEphemeralDPIDInAllSupportedSchemas(t *testing.T) {
	const id = "9967464c-12e9-40ca-9532-000000000002"
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			schema, err := os.ReadFile("testdata/vswitch-" + version + ".ovsschema")
			if err != nil {
				t.Fatal(err)
			}
			d, err := discover(schema)
			if err != nil || !monitoredColumn(d, "Bridge", "datapath_id") {
				t.Fatalf("observed DPID not monitored: %v", err)
			}
			rows := inventory.Rows{}
			wire := []byte(`{"Bridge":{"` + id + `":{"new":{"datapath_id":"aa11223344556677"}}}}`)
			if err := update(d, rows, wire, true); err != nil {
				t.Fatal(err)
			}
			at, ok := rows["Bridge"][id].Values["datapath_id"].([]any)
			if !ok || len(at) != 1 || at[0] != "aa11223344556677" {
				t.Fatalf("incorrect normalized DPID: %v", at)
			}
		})
	}
}

func TestOpenFlowAuxiliaryRetainsTypedGroupAndMeterConfiguration(t *testing.T) {
	f := flowTestHandshake(openFlowTestDPID)
	f = append(f, flowTestFrame(4, 19, 3, flowTestPart(0))...)
	f = append(f, flowTestFrame(4, 19, 4, auxiliaryTestPart(7, 0, auxiliaryTestGroup(11)))...)
	f = append(f, flowTestFrame(4, 19, 5, auxiliaryTestPart(10, 0, auxiliaryTestMeter(7)))...)
	var sent bytes.Buffer
	wire := struct {
		io.Reader
		io.Writer
	}{bytes.NewReader(f), &sent}
	var aux openFlowAuxiliary
	flows, limited, err := exchangeOpenFlowWithAuxiliary(wire, openFlowTestDPID,
		openFlowQuery{table: 2, limit: 5, auxiliary: true}, &aux)
	if err != nil || limited || len(flows) != 0 || aux.groupState != "observed" || aux.meterState != "observed" ||
		len(aux.groups) != 1 || len(aux.meters) != 1 {
		t.Fatalf("wrong bound auxiliary reply: %+v, %+v, %v", flows, aux, err)
	}
	group, meter := aux.groups[0], aux.meters[0]
	if group.ID != 11 || group.TypeCode != 1 || len(group.Buckets) != 1 || group.Buckets[0].Weight != 10 ||
		len(group.Buckets[0].ActionTypes) != 1 || group.Buckets[0].ActionTypes[0] != 17 ||
		meter.ID != 7 || meter.Flags != 1 || len(meter.Bands) != 1 || meter.Bands[0].Rate != 1000 || meter.Bands[0].Burst != 5 {
		t.Fatalf("incorrect typed descriptions: %+v, %+v", group, meter)
	}
	reader := openFlowReader{r: &sent, budget: 1024}
	for i, kind := range []byte{0, 5, 18, 18, 18} {
		got, err := reader.next()
		if err != nil || got.kind != kind || got.xid != uint32(i+1) {
			t.Fatalf("unexpected request: %+v, %v", got, err)
		}
		if i >= 2 && ofOrder.Uint16(got.body[:2]) != []uint16{1, 7, 10}[i-2] {
			t.Fatal("unexpected multipart operation")
		}
	}
	if sent.Len() != 0 {
		t.Fatal("unexpected request after the approved descriptions")
	}
}

func TestOpenFlowAuxiliaryRejectsMalformedAndStopsAtSharedCaps(t *testing.T) {
	q := openFlowQuery{table: openFlowAllTables, limit: 2, auxiliary: true}
	base := append(flowTestHandshake(openFlowTestDPID), flowTestFrame(4, 19, 3, flowTestPart(0))...)
	bad := auxiliaryTestGroup(1)
	bad[9] = 7 // malformed bucket length
	f := append(bytes.Clone(base), flowTestFrame(4, 19, 4, auxiliaryTestPart(7, 0, bad))...)
	var aux openFlowAuxiliary
	wire := struct {
		io.Reader
		io.Writer
	}{bytes.NewReader(f), io.Discard}
	_, _, err := exchangeOpenFlowWithAuxiliary(wire, openFlowTestDPID, q, &aux)
	if err != errOpenFlowInvalid || len(aux.groups) != 0 {
		t.Fatalf("malformed bucket supplied evidence: %+v, %v", aux, err)
	}
	rows := make([][]byte, openFlowAuxiliaryRows+1)
	for i := range rows {
		rows[i] = auxiliaryTestGroup(uint32(i + 1))
	}
	f = append(bytes.Clone(base), flowTestFrame(4, 19, 4, auxiliaryTestPart(7, 0, rows...))...)
	wire.Reader, aux = bytes.NewReader(f), openFlowAuxiliary{}
	_, _, err = exchangeOpenFlowWithAuxiliary(wire, openFlowTestDPID, q, &aux)
	if err != nil || aux.groupState != "truncated" || len(aux.groups) != openFlowAuxiliaryRows ||
		aux.meterState != "unavailable" || aux.meterReason != "OPENFLOW_CAPTURE_LIMIT" {
		t.Fatalf("auxiliary work outlived a capture cap: %+v, %v", aux, err)
	}
	f = append(bytes.Clone(base), flowTestFrame(4, 1, 4, []byte{0, 1, 0, 2})...)
	f = append(f, flowTestFrame(4, 19, 5, auxiliaryTestPart(10, 0))...)
	wire.Reader, aux = bytes.NewReader(f), openFlowAuxiliary{}
	_, _, err = exchangeOpenFlowWithAuxiliary(wire, openFlowTestDPID, q, &aux)
	if err != nil || aux.groupReason != "OPENFLOW_GROUP_DESCRIPTION_UNSUPPORTED" || aux.meterState != "observed" {
		t.Fatalf("native unsupported and empty evidence were confused: %+v, %v", aux, err)
	}
	f = append(bytes.Clone(base), flowTestFrame(4, 1, 4, []byte{0, 1, 0, 5})...)
	wire.Reader, aux = bytes.NewReader(f), openFlowAuxiliary{}
	_, _, err = exchangeOpenFlowWithAuxiliary(wire, openFlowTestDPID, q, &aux)
	if err != errOpenFlowRejected {
		t.Fatalf("permission rejection was mistaken for no native support: %v", err)
	}
}

func auxiliaryTestPart(kind, flags uint16, rows ...[]byte) []byte {
	body := flowTestPart(flags, rows...)
	ofOrder.PutUint16(body[:2], kind)
	return body
}

func auxiliaryTestGroup(id uint32) []byte {
	row := make([]byte, 32)
	ofOrder.PutUint16(row[:2], uint16(len(row)))
	row[2] = 1
	ofOrder.PutUint32(row[4:8], id)
	ofOrder.PutUint16(row[8:10], 24)
	ofOrder.PutUint16(row[10:12], 10)
	ofOrder.PutUint16(row[24:26], 17)
	ofOrder.PutUint16(row[26:28], 8)
	return row
}

func auxiliaryTestMeter(id uint32) []byte {
	row := make([]byte, 24)
	ofOrder.PutUint16(row[:2], uint16(len(row)))
	ofOrder.PutUint16(row[2:4], 1)
	ofOrder.PutUint32(row[4:8], id)
	ofOrder.PutUint16(row[8:10], 1)
	ofOrder.PutUint16(row[10:12], 16)
	ofOrder.PutUint32(row[12:16], 1000)
	ofOrder.PutUint32(row[16:20], 5)
	return row
}
