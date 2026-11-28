package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/openflowview"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestOpenFlowSnapshotWirePreservesCookieAndExactCounters(t *testing.T) {
	const cookie = "0xf123456789abcdef"
	const decimalCookie = "17375808098319191535"
	const counter = "18446744073709551614"
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	bridge, generation, snapshot := repository.NewID(), repository.NewID(), repository.NewID()
	path := "/api/v1/openflow/bridges/" + bridge + "/snapshots"
	observe, params, _ := contract.Match(http.MethodGet, path)
	read, _, _ := contract.Match(http.MethodGet, "/api/v1/openflow/snapshots/"+snapshot)
	if observe == nil || read == nil {
		t.Fatal("OpenFlow snapshot operations are missing")
	}
	argument := url.Values{"table": {"7"}, "cookie": {decimalCookie}}
	if err := observe.ValidateParameters(params, argument, nil); err != nil {
		t.Fatal(err)
	}
	argument.Set("cookie", cookie)
	if err := observe.ValidateParameters(params, argument, nil); err == nil {
		t.Fatal("wire cookie format was accepted as a filter argument")
	}
	table, packets := uint8(7), counter
	page := openFlowPage{
		Snapshot: openflowview.Reference{
			ID: snapshot, BridgeID: bridge, Generation: generation,
			ObservedAt: now, ExpiresAt: now.Add(30 * time.Second), Rows: 1,
		},
		Evidence: ovsdb.OpenFlowSnapshot{
			BridgeID: bridge, InstanceGeneration: generation, ObservedAt: now,
			Query:  ovsdb.OpenFlowCapturedQuery{Table: &table, Cookie: cookie, Limit: 6, IncludeGroupsMeters: true},
			Source: "openflow-1.3-unix", Ownership: "unknown", FieldCoverage: "metadata-only",
			Groups: "observed", GroupRows: []ovsdb.OpenFlowGroup{
				{ID: 17, Buckets: []ovsdb.OpenFlowGroupBucket{{ActionTypes: []uint16{0}}}},
			},
			Meters: "unavailable", MeterReason: "OPENFLOW_METER_CONFIGURATION_UNSUPPORTED", GroupMeterStatistics: "unknown",
			Rows: []ovsdb.OpenFlowRow{
				{Table: table, Priority: 405, Cookie: cookie, Packets: &packets,
					Match: []ovsdb.OpenFlowMatchField{}, Instructions: []ovsdb.OpenFlowInstruction{}},
			},
		},
	}
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []*apicontract.Operation{observe, read} {
		if err := op.ValidateResponse(http.StatusOK, data); err != nil {
			t.Fatal(err)
		}
	}
}
