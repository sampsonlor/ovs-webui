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
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type neighborObserverFunc func(context.Context, []DeviceRequest) (NeighborObservation, error)

func (f neighborObserverFunc) ObserveNeighbors(ctx context.Context, devices []DeviceRequest) (NeighborObservation, error) {
	return f(ctx, devices)
}

type neighborTest struct {
	*deviceTest
	sample  NeighborObservation
	failure error
	reads   int
}

func newNeighborTest(t *testing.T) *neighborTest {
	t.Helper()
	f := &neighborTest{deviceTest: newDeviceTest(t)}
	f.claims.Capabilities = []string{"inventory.read", "state.read"}
	f.claims.CredentialID = repository.NewID()
	device := DeviceRequest{Name: f.observation.Rows["Interface"][f.binding.UUID].Values["name"].(string), IfIndex: 7}
	mac := "02:00:00:00:00:07"
	f.sample = NeighborObservation{
		ObservedAt: f.now,
		Covered:    []DeviceRequest{device},
		Neighbors: []Neighbor{
			{Interface: device, IP: "192.0.2.2", MAC: &mac, State: "reachable"},
			{Interface: device, IP: "2001:db8::2", MAC: &mac, State: "stale"},
			{Interface: device, IP: "192.0.2.3", MAC: &mac, State: "incomplete"},
		},
	}
	f.service.SetNeighborObserver(neighborObserverFunc(func(_ context.Context, devices []DeviceRequest) (NeighborObservation, error) {
		f.reads++
		if len(devices) == 0 {
			t.Error("collector received no eligible Interfaces")
		}
		return f.sample, f.failure
	}))
	return f
}

func (f *neighborTest) page(t *testing.T, query url.Values) map[string]any {
	t.Helper()
	value, err := f.service.Read(t.Context(), "listNeighbors", nil, query, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func neighborProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Status != status || problem.Code != code {
		t.Fatalf("wanted %d %s, got %v", status, code, err)
	}
}

func TestNeighborsSharedSnapshotPermissionsAndContract(t *testing.T) {
	f := newNeighborTest(t)
	if page := f.page(t, nil); page["availability"] != "unavailable" || f.reads != 0 {
		t.Fatal("GET initiated collection or fabricated an empty observation", page)
	}
	f.service.SampleNeighbors(t.Context())
	first := f.page(t, nil)
	items := first["items"].([]map[string]any)
	if first["availability"] != "complete" || len(items) != 3 {
		t.Fatal("missing proven neighbor snapshot", first)
	}
	source := first["source"].(map[string]any)
	if source["authority"] != "linux-rtnetlink-neighbors" || source["provider_id"] != "linux" ||
		source["freshness"] != "fresh" || source["observed_at"] != f.now {
		t.Fatal("incorrect neighbor provenance", source)
	}
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	validate := func(path string, value any) {
		t.Helper()
		op, _, _ := contract.Match("GET", "/api/v1"+path)
		body, err := json.Marshal(value)
		if op == nil || err != nil {
			t.Fatal("missing typed neighbor response", path, err)
		}
		if err = op.ValidateResponse(200, body); err != nil {
			t.Fatal(err, string(body))
		}
	}
	validate("/neighbors", first)
	for _, item := range items {
		id := item["id"].(string)
		detail, err := f.service.Read(t.Context(), "readNeighbor", map[string]string{"neighbor_id": id}, nil, f.claims)
		if err != nil {
			t.Fatal(err)
		}
		validate("/neighbors/"+id, detail)
		body, _ := json.Marshal(item)
		detailBody, _ := json.Marshal(detail)
		if string(body) != string(detailBody) {
			t.Fatal("detail changed the shared observation")
		}
		if item["state"] == "incomplete" && item["mac"] != nil {
			t.Fatal("unresolved entry exposed a stale MAC", item)
		}
	}
	for range 5 {
		other := f.claims
		other.PrincipalID, other.CredentialID = repository.NewID(), repository.NewID()
		result, err := f.service.Read(t.Context(), "listNeighbors", nil, nil, other)
		if err != nil || result.(map[string]any)["snapshot_id"] != first["snapshot_id"] {
			t.Fatal("readers did not share one snapshot", err)
		}
	}
	items[0]["mac"] = "client-side change"
	if f.page(t, nil)["items"].([]map[string]any)[0]["mac"] == "client-side change" || f.reads != 1 {
		t.Fatal("GET mutated or recollected the shared sample")
	}
	for _, capability := range []string{"inventory.read", "state.read"} {
		denied := f.claims
		denied.Capabilities = []string{capability}
		for _, operation := range []string{"listNeighbors", "readNeighbor"} {
			_, err := f.service.Read(t.Context(), operation, map[string]string{"neighbor_id": items[0]["id"].(string)}, nil, denied)
			neighborProblem(t, err, 403, "CAPABILITY_DENIED")
		}
	}
}

func TestNeighborsFiltersPaginationAndCredentialFencing(t *testing.T) {
	f := newNeighborTest(t)
	f.service.SampleNeighbors(t.Context())
	for _, binding := range f.decision.Bindings {
		key := map[string]string{"Port": "port_id", "Bridge": "bridge_id"}[binding.Table]
		if key != "" {
			rows := f.page(t, url.Values{key: {binding.ManagementID}})["items"].([]map[string]any)
			if len(rows) != 3 {
				t.Fatal("parent scope lost its member Interface neighbors", key, rows)
			}
		}
	}
	for _, query := range []url.Values{
		{"family": {"ipv6"}}, {"filter": {"2001:DB8"}}, {"filter": {"02:00:00:00:00:07"}},
		{"filter": {strings.ToUpper(f.sample.Covered[0].Name)}}, {"interface_id": {f.binding.ManagementID}},
	} {
		page := f.page(t, query)
		rows := page["items"].([]map[string]any)
		if len(rows) == 0 {
			t.Fatal("supported filter lost matching neighbors", query)
		}
		if query.Get("family") == "ipv6" && (len(rows) != 1 || rows[0]["family"] != "ipv6") {
			t.Fatal("family filter mixed protocols", rows)
		}
		if query.Get("filter") == "02:00:00:00:00:07" && len(rows) != 2 {
			t.Fatal("MAC filter included unresolved entry", rows)
		}
	}
	query := url.Values{"limit": {"1"}}
	first := f.page(t, query)
	token, ok := first["next_cursor"].(string)
	if !ok || first["truncated"] != false {
		t.Fatal("pagination was confused with collection truncation", first)
	}
	query.Set("cursor", token)
	second := f.page(t, query)
	if second["items"].([]map[string]any)[0]["id"] == first["items"].([]map[string]any)[0]["id"] {
		t.Fatal("cursor repeated its boundary row")
	}
	for _, change := range []string{"principal", "credential", "permission", "filter", "family", "limit"} {
		t.Run(change, func(t *testing.T) {
			claims := f.claims
			changed := maps.Clone(query)
			switch change {
			case "principal":
				claims.PrincipalID = repository.NewID()
			case "credential":
				claims.CredentialID = repository.NewID()
			case "permission":
				claims.Revision = repository.NewID()
			case "filter":
				changed.Set("filter", "192.0.2")
			case "family":
				changed.Set("family", "ipv4")
			case "limit":
				changed.Set("limit", "2")
			}
			_, err := f.service.Read(t.Context(), "listNeighbors", nil, changed, claims)
			neighborProblem(t, err, 410, "CURSOR_EXPIRED")
		})
	}
	f.now = f.now.Add(time.Second)
	f.sample.ObservedAt = f.now
	f.service.SampleNeighbors(t.Context())
	if f.page(t, query)["snapshot_id"] != first["snapshot_id"] {
		t.Fatal("unchanged sample expired an otherwise valid cursor")
	}
	f.sample.Neighbors[0].State = "stale"
	f.service.SampleNeighbors(t.Context())
	_, err := f.service.Read(t.Context(), "listNeighbors", nil, query, f.claims)
	neighborProblem(t, err, 410, "CURSOR_EXPIRED")
}

func TestNeighborsStaleFailureAndExpiryWithholdRows(t *testing.T) {
	for _, change := range []string{"outage", "provider", "expired"} {
		t.Run(change, func(t *testing.T) {
			f := newNeighborTest(t)
			f.service.SampleNeighbors(t.Context())
			id := f.page(t, nil)["items"].([]map[string]any)[0]["id"].(string)
			reason := "OVS_ASSOCIATION_STALE"
			switch change {
			case "outage":
				f.service.Unavailable("OVSDB_UNAVAILABLE")
			case "provider":
				reason = "NEIGHBORS_DUMP_UNAVAILABLE"
				f.failure = errors.New("private collector diagnostic")
				f.service.SampleNeighbors(t.Context())
			case "expired":
				reason = "NEIGHBORS_EXPIRED"
				f.now = f.now.Add(neighborFreshFor + time.Second)
				f.observation.Evidence.ObservedAt = f.now
				f.service.install(f.observation, f.decision)
			}
			page := f.page(t, nil)
			if page["availability"] != "unavailable" || page["reason"] != reason || len(page["items"].([]map[string]any)) != 0 {
				t.Fatal("unavailable observation retained neighbor rows", page)
			}
			body, _ := json.Marshal(page)
			if strings.Contains(string(body), "private collector") {
				t.Fatal("private provider error escaped")
			}
			_, err := f.service.Read(t.Context(), "readNeighbor", map[string]string{"neighbor_id": id}, nil, f.claims)
			neighborProblem(t, err, 503, reason)
		})
	}
}

func TestNeighborsSamplingRejectsObservedRebindingAndRecovery(t *testing.T) {
	for _, change := range []string{"index-and-revert", "outage-and-recover"} {
		t.Run(change, func(t *testing.T) {
			f := newNeighborTest(t)
			f.service.SetNeighborObserver(neighborObserverFunc(func(context.Context, []DeviceRequest) (NeighborObservation, error) {
				if change == "index-and-revert" {
					f.service.install(f.withDeviceValues(map[string]any{"ifindex": []any{"8"}}), f.decision)
				} else {
					f.service.Unavailable("OVSDB_UNAVAILABLE")
				}
				f.service.install(f.observation, f.decision)
				return f.sample, nil
			}))
			f.service.SampleNeighbors(t.Context())
			page := f.page(t, nil)
			if page["availability"] != "unavailable" || len(page["items"].([]map[string]any)) != 0 {
				t.Fatal("A-to-B-to-A association revived in-flight evidence", page)
			}
		})
	}
}

func TestNeighborsPartialEmptyAndTruncatedCoverage(t *testing.T) {
	f := newNeighborTest(t)
	second := f.observation
	second.Rows = maps.Clone(second.Rows)
	second.Rows["Interface"] = maps.Clone(second.Rows["Interface"])
	var other Binding
	for _, binding := range f.decision.Bindings {
		if binding.Table == "Interface" && binding.UUID != f.binding.UUID {
			other = binding
		}
	}
	row := second.Rows["Interface"][other.UUID]
	row.Values = maps.Clone(row.Values)
	row.Values["type"], row.Values["ifindex"] = "system", []any{"8"}
	second.Rows["Interface"][other.UUID] = row
	f.service.install(second, f.decision)
	f.service.SampleNeighbors(t.Context())
	page := f.page(t, nil)
	coverage := page["coverage"].(map[string]any)
	if page["availability"] != "degraded" || page["reason"] != "NEIGHBORS_PARTIAL_COVERAGE" ||
		coverage["eligible_interfaces"] != 2 || coverage["observed_interfaces"] != 1 {
		t.Fatal("partial coverage appeared complete", page)
	}
	known := f.page(t, url.Values{"interface_id": {f.binding.ManagementID}, "filter": {"no-match"}})
	if known["availability"] != "complete" || len(known["items"].([]map[string]any)) != 0 {
		t.Fatal("proven filtered empty scope became unavailable", known)
	}
	unproven := f.page(t, url.Values{"interface_id": {other.ManagementID}})
	if unproven["availability"] != "unavailable" || unproven["reason"] != "NEIGHBORS_NO_PROVEN_SCOPE" {
		t.Fatal("unobserved scope appeared empty", unproven)
	}
	f.sample.Covered = append(f.sample.Covered, DeviceRequest{Name: row.Values["name"].(string), IfIndex: 8})
	f.sample.Neighbors = nil
	f.service.SampleNeighbors(t.Context())
	empty := f.page(t, nil)
	if empty["availability"] != "complete" || len(empty["items"].([]map[string]any)) != 0 {
		t.Fatal("proven empty table was not represented as empty", empty)
	}
	f.sample.Truncated = true
	f.service.SampleNeighbors(t.Context())
	truncated := f.page(t, nil)
	if truncated["availability"] != "degraded" || truncated["reason"] != "NEIGHBORS_TRUNCATED" ||
		truncated["truncated"] != true || truncated["next_cursor"] != nil {
		t.Fatal("collection truncation was confused with cursor pagination", truncated)
	}
}
