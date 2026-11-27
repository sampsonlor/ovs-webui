package inventory

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func addNeighborEventDevice(f *neighborTest) Binding {
	binding := addEventTestDevice(f.deviceTest, "other-link", 8)
	request := DeviceRequest{Name: "other-link", IfIndex: 8}
	f.sample.Covered = append(f.sample.Covered, request)
	f.sample.Neighbors = append(f.sample.Neighbors, Neighbor{Interface: request, IP: "192.0.2.8", State: "incomplete"})
	return binding
}

func requirePendingNeighbors(t *testing.T, page map[string]any) {
	t.Helper()
	source := page["source"].(map[string]any)
	coverage := page["coverage"].(map[string]any)
	if page["availability"] != "unavailable" || page["reason"] != "NEIGHBORS_RESYNCING" ||
		len(page["items"].([]map[string]any)) != 0 || page["next_cursor"] != nil ||
		source["freshness"] != "unavailable" || source["observed_at"] != nil || coverage["observed_interfaces"] != 0 {
		t.Fatal("invalidated scope retained neighbor observations", page)
	}
}

func TestNeighborEventsInvalidateOnlyAffectedEvidence(t *testing.T) {
	f := newNeighborTest(t)
	other := addNeighborEventDevice(f)
	f.service.SampleNeighbors(t.Context())
	query := url.Values{"limit": {"1"}}
	first := f.page(t, query)
	token, ok := first["next_cursor"].(string)
	if !ok {
		t.Fatal("expected a paginated pre-event snapshot", first)
	}
	previous := f.service.neighbors
	f.service.HandleNeighborEvents(InterfaceEvents{Indices: []int{999}})
	if f.service.neighbors.id != previous.id || len(f.service.neighbors.pending) != 0 {
		t.Fatal("an unmanaged index changed the shared snapshot")
	}

	f.service.HandleNeighborEvents(InterfaceEvents{Indices: []int{7, 7}})
	filtered := f.service.neighbors
	for range 10 {
		f.service.HandleNeighborEvents(InterfaceEvents{Indices: []int{7}})
	}
	current := f.service.neighbors
	if current.id == previous.id || current.id != filtered.id || current.sequence != previous.sequence+1 ||
		current.eventRevision != filtered.eventRevision || len(current.pending) != 1 ||
		len(previous.entries) != 4 || previous.entries[0].ID == "" || len(previous.covered) != 2 || len(previous.pending) != 0 {
		t.Fatal("event invalidation mutated an earlier snapshot or repeated identical projection work")
	}
	query.Set("cursor", token)
	_, err := f.service.Read(t.Context(), "listNeighbors", nil, query, f.claims)
	neighborProblem(t, err, 410, "CURSOR_EXPIRED")

	affected := url.Values{"interface_id": {f.binding.ManagementID}}
	requirePendingNeighbors(t, f.page(t, affected))
	unaffected := url.Values{"interface_id": {other.ManagementID}}
	page := f.page(t, unaffected)
	if page["availability"] != "complete" || len(page["items"].([]map[string]any)) != 1 ||
		page["source"].(map[string]any)["observed_at"] != f.now {
		t.Fatal("an unaffected interface lost its observed evidence", page)
	}
	page = f.page(t, nil)
	if page["availability"] != "degraded" || page["reason"] != "NEIGHBORS_PARTIAL_COVERAGE" || len(page["items"].([]map[string]any)) != 1 {
		t.Fatal("the aggregate did not disclose scoped invalidation", page)
	}
	endpoints, err := f.service.Read(t.Context(), "listEndpoints", nil, nil, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	endpointPage := endpoints.(map[string]any)
	neighbor := endpointPage["sources"].([]endpointSourceCoverage)[1]
	if endpointPage["matched"] != 1 || neighbor.Availability != "degraded" || neighbor.Observed != 1 || f.reads != 1 {
		t.Fatal("Endpoints retained invalid neighbors or a hint/read initiated a dump", endpointPage, f.reads)
	}

	// The next scheduled collector can restore coverage without another hint.
	f.now = f.now.Add(time.Second)
	f.sample.ObservedAt = f.now
	f.service.SampleNeighbors(t.Context())
	if page := f.page(t, affected); page["availability"] != "complete" || len(page["items"].([]map[string]any)) != 3 ||
		page["source"].(map[string]any)["observed_at"] != f.now || f.reads != 2 || len(f.service.neighbors.pending) != 0 {
		t.Fatal("bounded fallback did not restore the affected scope", page)
	}
}

func TestNeighborEventsIgnoreAmbiguousInterfaceIndices(t *testing.T) {
	f := newNeighborTest(t)
	addEventTestDevice(f.deviceTest, "ambiguous-1", 8)
	addEventTestDevice(f.deviceTest, "ambiguous-2", 8)
	f.service.SampleNeighbors(t.Context())
	before := f.service.neighbors
	f.service.HandleNeighborEvents(InterfaceEvents{Indices: []int{8}})
	after := f.service.neighbors
	if after.id != before.id || after.eventRevision != before.eventRevision || len(after.pending) != 0 || len(after.entries) != 3 {
		t.Fatal("an ambiguous index invalidated proven neighbor rows")
	}
}

func TestNeighborEventsFenceInFlightDump(t *testing.T) {
	for _, tc := range []struct {
		name        string
		event       InterfaceEvents
		otherUsable bool
	}{
		{"targeted", InterfaceEvents{Indices: []int{7}}, true},
		{"lost", InterfaceEvents{Lost: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNeighborTest(t)
			other := addNeighborEventDevice(f)
			started := make(chan struct{})
			release := make(chan struct{})
			block := false
			f.service.SetNeighborObserver(neighborObserverFunc(func(_ context.Context, _ []DeviceRequest) (NeighborObservation, error) {
				if block {
					close(started)
					<-release
				}
				return f.sample, nil
			}))
			f.service.SampleNeighbors(t.Context())
			// Keep the first targeted invalidation pending when the dump starts.
			// Another event on that same index must still fence this newer dump.
			f.service.HandleNeighborEvents(InterfaceEvents{Indices: []int{7}})
			block = true
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				f.service.SampleNeighbors(t.Context())
			}()
			<-started
			f.service.HandleNeighborEvents(tc.event)
			close(release)
			<-finished
			block = false
			requirePendingNeighbors(t, f.page(t, url.Values{"interface_id": {f.binding.ManagementID}}))
			otherQuery := url.Values{"interface_id": {other.ManagementID}}
			page := f.page(t, otherQuery)
			if tc.otherUsable {
				if page["availability"] != "complete" || len(page["items"].([]map[string]any)) != 1 {
					t.Fatal("a targeted event fenced independent evidence", page)
				}
			} else {
				requirePendingNeighbors(t, page)
			}
			f.service.SampleNeighbors(t.Context())
			if page := f.page(t, nil); page["availability"] != "complete" || len(page["items"].([]map[string]any)) != 4 {
				t.Fatal("the first safe resync failed to recover both bindings", page)
			}
		})
	}
}
