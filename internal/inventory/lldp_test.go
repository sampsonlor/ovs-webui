package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type lldpObserverFunc func(context.Context, uint64, []DeviceRequest) (LLDPObservation, error)

func (f lldpObserverFunc) ObserveLLDP(ctx context.Context, epoch uint64, devices []DeviceRequest) (LLDPObservation, error) {
	return f(ctx, epoch, devices)
}

type lldpTest struct {
	*deviceTest
	sample   LLDPObservation
	failure  error
	reads    int
	requests []DeviceRequest
}

func newLLDPTest(t *testing.T) *lldpTest {
	t.Helper()
	f := &lldpTest{deviceTest: newDeviceTest(t)}
	f.claims.Capabilities = []string{"inventory.read", "state.read"}
	f.claims.CredentialID = repository.NewID()
	device := DeviceRequest{Name: f.observation.Rows["Interface"][f.binding.UUID].Values["name"].(string), IfIndex: 7}
	f.sample = LLDPObservation{
		ObservedAt: f.now,
		Covered:    []DeviceRequest{device},
		Frames: []LLDPFrame{{
			Interface:  device,
			ChassisID:  LLDPID{Subtype: 4, Encoding: "mac", Value: "02:00:00:00:00:07"},
			PortID:     LLDPID{Subtype: 5, Encoding: "text", Value: "Ethernet1"},
			SystemName: "synthetic-switch",
			TTL:        60,
			ReceivedAt: f.now.Add(-time.Second),
		}},
	}
	if err := f.service.SetLLDPObserver(lldpObserverFunc(func(_ context.Context, _ uint64, devices []DeviceRequest) (LLDPObservation, error) {
		f.reads++
		f.requests = append([]DeviceRequest(nil), devices...)
		return f.sample, f.failure
	}), []string{f.binding.ManagementID}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *lldpTest) page(t *testing.T, query url.Values) map[string]any {
	t.Helper()
	value, err := f.service.Read(t.Context(), "listLldp", nil, query, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func (f *lldpTest) first(t *testing.T) map[string]any {
	t.Helper()
	rows := f.page(t, nil)["items"].([]map[string]any)
	if len(rows) == 0 {
		t.Fatal("missing received LLDP advertisement")
	}
	return rows[0]
}

func lastSeenLLDP(t *testing.T, row map[string]any) time.Time {
	t.Helper()
	body, err := json.Marshal(row["lldp"])
	if err != nil {
		t.Fatal(err)
	}
	var advertisement struct {
		LastSeen time.Time `json:"last_seen_at"`
	}
	if err = json.Unmarshal(body, &advertisement); err != nil {
		t.Fatal(err)
	}
	return advertisement.LastSeen
}

func TestLLDPDefaultDisabledAndIndependentReadAuthority(t *testing.T) {
	f := newDeviceTest(t)
	value, err := f.service.Read(t.Context(), "listLldp", nil, nil, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	page := value.(map[string]any)
	if page["availability"] != "unavailable" || page["reason"] != "LLDP_PROVIDER_DISABLED" ||
		len(page["items"].([]map[string]any)) != 0 {
		t.Fatal("default-disabled provider appeared observed", page)
	}
	active := newLLDPTest(t)
	for _, capability := range []string{"inventory.read", "state.read"} {
		claims := active.claims
		claims.Capabilities = []string{capability}
		for _, operation := range []string{"listLldp", "readLldpNeighbor"} {
			_, err := active.service.Read(t.Context(), operation, map[string]string{"lldp_neighbor_id": repository.NewID()}, nil, claims)
			neighborProblem(t, err, 403, "CAPABILITY_DENIED")
		}
	}
	if active.reads != 0 {
		t.Fatal("read authorization attempted packet capture")
	}
}

func TestLLDPSharedCaptureAndCompatibleListDetail(t *testing.T) {
	f := newLLDPTest(t)
	pending := f.page(t, nil)
	if pending["availability"] != "unavailable" || f.reads != 0 {
		t.Fatal("GET created a packet observation", pending)
	}
	f.service.SampleLLDP(t.Context())
	page := f.page(t, nil)
	row := f.first(t)
	if page["availability"] != "complete" || len(f.requests) != 1 || f.requests[0] != f.sample.Covered[0] {
		t.Fatal("capture scope changed", page, f.requests)
	}
	source := page["source"].(map[string]any)
	if source["authority"] != "linux-passive-lldp" || source["freshness"] != "fresh" ||
		source["observed_at"] != f.now || !lastSeenLLDP(t, row).Equal(f.sample.Frames[0].ReceivedAt) {
		t.Fatal("packet receive time was conflated with sampling", row, source)
	}
	for range 5 {
		if f.page(t, nil)["snapshot_id"] != page["snapshot_id"] {
			t.Fatal("GET replaced a shared packet observation")
		}
	}
	id := row["id"].(string)
	detail, err := f.service.Read(t.Context(), "readLldpNeighbor", map[string]string{"lldp_neighbor_id": id}, nil, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	firstBody, _ := json.Marshal(row)
	detailBody, _ := json.Marshal(detail)
	if string(firstBody) != string(detailBody) || f.reads != 1 {
		t.Fatal("list and detail use different packet evidence")
	}
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	for path, response := range map[string]any{"/lldp": page, "/lldp/" + id: detail} {
		op, _, _ := contract.Match("GET", "/api/v1"+path)
		body, err := json.Marshal(response)
		if op == nil || err != nil {
			t.Fatal("missing LLDP response contract", path, err)
		}
		if err = op.ValidateResponse(200, body); err != nil {
			t.Fatal(err, string(body))
		}
	}
}

func TestLLDPOnlyCapturesProvenOptedInInterfaces(t *testing.T) {
	f := newLLDPTest(t)
	observation := f.observation
	observation.Rows = maps.Clone(observation.Rows)
	observation.Rows["Interface"] = maps.Clone(observation.Rows["Interface"])
	var other Binding
	for _, binding := range f.decision.Bindings {
		if binding.Table == "Interface" && binding.UUID != f.binding.UUID {
			other = binding
		}
	}
	row := observation.Rows["Interface"][other.UUID]
	row.Values = maps.Clone(row.Values)
	row.Values["type"], row.Values["ifindex"] = "system", []any{"8"}
	observation.Rows["Interface"][other.UUID] = row
	f.service.install(observation, f.decision)
	f.service.SampleLLDP(t.Context())
	if len(f.requests) != 1 || f.requests[0] != f.sample.Covered[0] {
		t.Fatal("ungranted managed Interface entered capture scope", f.requests)
	}
	uncovered := f.page(t, url.Values{"interface_id": {other.ManagementID}})
	if uncovered["availability"] != "unavailable" || uncovered["reason"] != "LLDP_SCOPE_NOT_COVERED" ||
		len(uncovered["items"].([]map[string]any)) != 0 {
		t.Fatal("unconfigured Interface appeared observed", uncovered)
	}
	f.service.install(f.withDeviceValues(map[string]any{"type": "dpdk"}), f.decision)
	f.service.SampleLLDP(t.Context())
	if len(f.requests) != 0 {
		t.Fatal("unsupported link retained a receive handle", f.requests)
	}
}

func TestLLDPRepeatRefreshesLastSeenAndWithdrawalRemovesPeer(t *testing.T) {
	f := newLLDPTest(t)
	f.service.SampleLLDP(t.Context())
	first := f.first(t)
	id := first["id"].(string)
	f.now = f.now.Add(time.Second)
	f.sample.ObservedAt = f.now
	f.sample.Frames[0].ReceivedAt = f.now
	f.service.SampleLLDP(t.Context())
	second := f.first(t)
	if second["id"] != id || !lastSeenLLDP(t, second).Equal(f.now) || lastSeenLLDP(t, second).Equal(lastSeenLLDP(t, first)) {
		t.Fatal("unchanged repeated advertisement did not refresh receive time", first, second)
	}
	f.now = f.now.Add(time.Second)
	f.sample.ObservedAt = f.now
	f.sample.Frames[0].ReceivedAt = f.now
	f.sample.Frames[0].TTL = 0
	f.service.SampleLLDP(t.Context())
	if len(f.page(t, nil)["items"].([]map[string]any)) != 0 {
		t.Fatal("TTL-zero withdrawal retained the peer")
	}
	_, err := f.service.Read(t.Context(), "readLldpNeighbor", map[string]string{"lldp_neighbor_id": id}, nil, f.claims)
	neighborProblem(t, err, 404, "NOT_FOUND")
}

func TestLLDPTTLExpiryAndCaptureExpiryRemainDistinct(t *testing.T) {
	f := newLLDPTest(t)
	f.sample.Frames[0].TTL = 2
	f.sample.Frames[0].ReceivedAt = f.now
	f.service.SampleLLDP(t.Context())
	id := f.first(t)["id"].(string)
	f.now = f.now.Add(3 * time.Second)
	page := f.page(t, nil)
	if page["availability"] != "complete" || len(page["items"].([]map[string]any)) != 0 || f.reads != 1 {
		t.Fatal("TTL expiry required a new read or appeared unavailable", page)
	}
	_, err := f.service.Read(t.Context(), "readLldpNeighbor", map[string]string{"lldp_neighbor_id": id}, nil, f.claims)
	neighborProblem(t, err, 404, "NOT_FOUND")
	f.now = f.now.Add(lldpFreshFor)
	f.observation.Evidence.ObservedAt = f.now
	f.service.install(f.observation, f.decision)
	expired := f.page(t, nil)
	if expired["availability"] != "unavailable" || expired["reason"] != "LLDP_EXPIRED" {
		t.Fatal("expired capture appeared as a current empty scope", expired)
	}
}

func TestLLDPMalformedOrForeignObservationsWithholdPriorPeers(t *testing.T) {
	for _, change := range []string{"foreign-link", "bad-chassis", "future-frame", "control-text", "foreign-coverage", "provider-failure"} {
		t.Run(change, func(t *testing.T) {
			f := newLLDPTest(t)
			f.service.SampleLLDP(t.Context())
			_ = f.first(t)
			switch change {
			case "foreign-link":
				f.sample.Frames[0].Interface.IfIndex = 8
			case "bad-chassis":
				f.sample.Frames[0].ChassisID.Value = "not-a-mac"
			case "future-frame":
				f.sample.Frames[0].ReceivedAt = f.now.Add(time.Second)
			case "control-text":
				f.sample.Frames[0].SystemName = "private\x00diagnostic"
			case "foreign-coverage":
				f.sample.Covered[0].IfIndex = 8
			case "provider-failure":
				f.failure = errors.New("private packet collector diagnostic")
			}
			f.service.SampleLLDP(t.Context())
			page := f.page(t, nil)
			if page["availability"] != "unavailable" || len(page["items"].([]map[string]any)) != 0 {
				t.Fatal("failed capture retained prior peer data", page)
			}
			body, _ := json.Marshal(page)
			if strings.Contains(string(body), "private") {
				t.Fatal("private packet diagnostics escaped", string(body))
			}
		})
	}
}

func TestLLDPCaptureRejectsObservedRebindingAndRecovery(t *testing.T) {
	for _, change := range []string{"index-and-revert", "outage-and-recover"} {
		t.Run(change, func(t *testing.T) {
			f := newLLDPTest(t)
			var capturedEpoch uint64
			err := f.service.SetLLDPObserver(lldpObserverFunc(func(_ context.Context, epoch uint64, _ []DeviceRequest) (LLDPObservation, error) {
				capturedEpoch = epoch
				if change == "index-and-revert" {
					f.service.install(f.withDeviceValues(map[string]any{"ifindex": []any{"8"}}), f.decision)
				} else {
					f.service.Unavailable("OVSDB_UNAVAILABLE")
				}
				f.service.install(f.observation, f.decision)
				return f.sample, nil
			}), []string{f.binding.ManagementID})
			if err != nil {
				t.Fatal(err)
			}
			f.service.SampleLLDP(t.Context())
			page := f.page(t, nil)
			if page["availability"] != "unavailable" || len(page["items"].([]map[string]any)) != 0 ||
				f.service.lldp.epoch == capturedEpoch {
				t.Fatal("A-to-B-to-A association published in-flight packet evidence", page)
			}
		})
	}
}

func TestLLDPCursorsBindCredentialFilterAndCapture(t *testing.T) {
	f := newLLDPTest(t)
	secondFrame := f.sample.Frames[0]
	secondFrame.PortID.Value = "Ethernet2"
	f.sample.Frames = append(f.sample.Frames, secondFrame)
	f.service.SampleLLDP(t.Context())
	first := f.page(t, url.Values{"limit": {"1"}})
	token, ok := first["next_cursor"].(string)
	if !ok || first["truncated"] != false {
		t.Fatal("normal pagination was reported as capture truncation", first)
	}
	query := url.Values{"limit": {"1"}, "cursor": {token}}
	second := f.page(t, query)
	if second["items"].([]map[string]any)[0]["id"] == first["items"].([]map[string]any)[0]["id"] {
		t.Fatal("cursor repeated its boundary advertisement")
	}
	for _, change := range []string{"credential", "permission", "filter", "scope"} {
		t.Run(change, func(t *testing.T) {
			claims, q := f.claims, maps.Clone(query)
			switch change {
			case "credential":
				claims.CredentialID = repository.NewID()
			case "permission":
				claims.Revision = repository.NewID()
			case "filter":
				q.Set("filter", "Ethernet1")
			case "scope":
				q.Set("interface_id", f.binding.ManagementID)
			}
			_, err := f.service.Read(t.Context(), "listLldp", nil, q, claims)
			neighborProblem(t, err, 410, "CURSOR_EXPIRED")
		})
	}
	f.now = f.now.Add(time.Second)
	f.sample.ObservedAt = f.now
	f.sample.Frames[0].ReceivedAt = f.now
	f.service.SampleLLDP(t.Context())
	_, err := f.service.Read(t.Context(), "listLldp", nil, query, f.claims)
	neighborProblem(t, err, 410, "CURSOR_EXPIRED")
}

func TestLLDPTruncatedCaptureDropsUnrefreshedPeers(t *testing.T) {
	f := newLLDPTest(t)
	f.service.SampleLLDP(t.Context())
	old := f.first(t)["id"]
	f.sample.Truncated = true
	f.sample.Frames[0].PortID.Value = "Ethernet2"
	f.service.SampleLLDP(t.Context())
	page := f.page(t, nil)
	rows := page["items"].([]map[string]any)
	if page["availability"] != "degraded" || page["reason"] != "LLDP_CAPTURE_TRUNCATED" ||
		page["truncated"] != true || page["next_cursor"] != nil || len(rows) != 1 || rows[0]["id"] == old {
		t.Fatal("capture loss carried peers across a potentially missed withdrawal", page)
	}
	f.sample.Truncated, f.sample.Frames = false, nil
	f.service.SampleLLDP(t.Context())
	if stable := f.page(t, nil); stable["availability"] != "degraded" || stable["truncated"] != true {
		t.Fatal("lossy capture regained complete coverage without resetting its epoch", stable)
	}
}
