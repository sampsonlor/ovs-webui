package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestEndpointsFiltersKeepIndependentCoverage(t *testing.T) {
	f := newNeighborTest(t)
	page := func(q url.Values) map[string]any {
		value, err := f.service.Read(t.Context(), "listEndpoints", nil, q, f.claims)
		if err != nil {
			t.Fatal(err)
		}
		return value.(map[string]any)
	}
	if initial := page(nil); initial["availability"] != "unavailable" || f.reads != 0 {
		t.Fatalf("read collected native state or implied coverage: %v, calls %d", initial, f.reads)
	}
	f.service.SampleNeighbors(t.Context())
	wanted := "2001:db8::2"
	matched := page(url.Values{"ip": {"2001:0db8:0:0:0:0:0:2"}})
	sources := matched["sources"].([]endpointSourceCoverage)
	items := matched["items"].([]map[string]any)
	if len(items) != 1 || matched["freshness"] != "fresh" || matched["availability"] != "degraded" ||
		sources[0].ScopeReason != "IP_NOT_REPORTED" || sources[0].Reason != "disabled" ||
		sources[1].Availability != "complete" || sources[2].ScopeReason != "IP_NOT_REPORTED" {
		t.Fatalf("address filtered with false coverage: %v", matched)
	}
	row := items[0]["endpoint"].(map[string]any)
	if r := row["evidence"].([]endpointEvidence); len(r) != 2 || r[0].IP != wanted || r[1].IP != "192.0.2.2" {
		t.Fatalf("wrong exact IP order or lost dual-stack evidence: %+v", r)
	}
	checkEndpointContract(t, "/endpoints", matched)
	detail, err := f.service.Read(t.Context(), "readEndpoint", map[string]string{"endpoint_id": items[0]["id"].(string)}, url.Values{"ip": {wanted}}, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	checkEndpointContract(t, "/endpoints/"+items[0]["id"].(string), detail)
	if got := detail.(map[string]any)["endpoint"].(map[string]any)["evidence"].([]endpointEvidence)[0].IP; got != wanted {
		t.Fatalf("detail did not retain address priority: %v", got)
	}

	vlan := page(url.Values{"vlan": {"0"}})
	unqualified := vlan["sources"].([]endpointSourceCoverage)
	if vlan["availability"] != "unavailable" || vlan["matched"] != 0 ||
		unqualified[1].ScopeReason != "VLAN_NOT_REPORTED" || unqualified[2].ScopeReason != "VLAN_NOT_REPORTED" ||
		unqualified[1].Source["freshness"] != "fresh" {
		t.Fatalf("Linux neighbor was attributed to an unsupported VLAN or lost source freshness: %v", vlan)
	}
	first := page(url.Values{"limit": {"1"}})
	token, ok := first["next_cursor"].(string)
	if !ok || len(first["items"].([]map[string]any)) != 1 {
		t.Fatalf("expected a bounded first page: %v", first)
	}
	last := page(url.Values{"limit": {"1"}, "cursor": {token}})
	if last["snapshot_id"] != first["snapshot_id"] || last["next_cursor"] != nil || last["matched"] != 2 {
		t.Fatalf("continuation used mixed snapshots: %v", last)
	}
	_, err = f.service.Read(t.Context(), "listEndpoints", nil, url.Values{"limit": {"1"}, "cursor": {token}, "mac": {"02:00:00:00:00:07"}}, f.claims)
	neighborProblem(t, err, 410, "CURSOR_EXPIRED")

	f.failure = apitypes.Fail(503, "NEIGHBORS_DUMP_TIMEOUT")
	f.service.SampleNeighbors(t.Context())
	after := page(nil)
	if after["availability"] != "unavailable" || after["matched"] != 0 ||
		after["sources"].([]endpointSourceCoverage)[1].Reason != "NEIGHBORS_DUMP_TIMEOUT" || f.reads != 2 {
		t.Fatalf("timeout retained stale rows or GET triggered a source: %v, calls %d", after, f.reads)
	}
	_, err = f.service.Read(t.Context(), "readEndpoint", map[string]string{"endpoint_id": items[0]["id"].(string)}, nil, f.claims)
	neighborProblem(t, err, 503, "ENDPOINT_COVERAGE_LIMITED")
	for _, caps := range [][]string{{"state.read"}, {"inventory.read"}} {
		claims := f.claims
		claims.Capabilities = caps
		_, err := f.service.Read(t.Context(), "listEndpoints", nil, nil, claims)
		neighborProblem(t, err, 403, "CAPABILITY_DENIED")
		_, err = f.service.Read(t.Context(), "readEndpoint", map[string]string{"endpoint_id": repository.NewID()}, nil, claims)
		neighborProblem(t, err, 403, "CAPABILITY_DENIED")
	}
}

func TestEndpointsExpiredLLDPPeerInvalidatesPageWithoutRemovingNeighborData(t *testing.T) {
	f := newNeighborTest(t)
	f.service.SampleNeighbors(t.Context())
	req := f.sample.Covered[0]
	if err := f.service.SetLLDPObserver(lldpObserverFunc(func(_ context.Context, _ uint64, _ []DeviceRequest) (LLDPObservation, error) {
		return LLDPObservation{ObservedAt: f.now, Covered: []DeviceRequest{req}, Frames: []LLDPFrame{{
			Interface: req, ChassisID: LLDPID{Subtype: 4, Encoding: "mac", Value: "02:00:00:00:00:07"},
			PortID: LLDPID{Subtype: 5, Encoding: "text", Value: "remote-port"}, TTL: 2, ReceivedAt: f.now,
		}}}, nil
	}), []string{f.binding.ManagementID}); err != nil {
		t.Fatal(err)
	}
	f.service.SampleLLDP(t.Context())
	query := url.Values{"limit": {"1"}}
	raw, err := f.service.Read(t.Context(), "listEndpoints", nil, query, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	first := raw.(map[string]any)
	if first["matched"] != 3 {
		t.Fatalf("neighbor address and LLDP should be independent groups: %v", first)
	}

	f.now = f.now.Add(2 * time.Second)
	query.Set("cursor", first["next_cursor"].(string))
	_, err = f.service.Read(t.Context(), "listEndpoints", nil, query, f.claims)
	neighborProblem(t, err, 410, "CURSOR_EXPIRED")
	query.Del("cursor")
	raw, err = f.service.Read(t.Context(), "listEndpoints", nil, query, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	after := raw.(map[string]any)
	if after["matched"] != 2 || after["freshness"] != "fresh" {
		t.Fatalf("expiring LLDP withdrew current neighbor evidence: %v", after)
	}
}

func checkEndpointContract(t *testing.T, path string, result any) {
	t.Helper()
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ := contract.Match("GET", "/api/v1"+path)
	raw, err := json.Marshal(result)
	if op == nil || err != nil {
		t.Fatal("missing Endpoints response contract", path, err)
	}
	if err := op.ValidateResponse(200, raw); err != nil {
		t.Fatal(err)
	}
}
