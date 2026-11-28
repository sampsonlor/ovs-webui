package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
)

type unexpectedCounterCollector struct{ calls atomic.Int32 }

type publishedCounterObserver func(context.Context, StatRequest) (StatObservation, error)

func (f publishedCounterObserver) ObserveStatistics(ctx context.Context, req StatRequest) (StatObservation, error) {
	return f(ctx, req)
}

func (p *unexpectedCounterCollector) ObserveStatistics(context.Context, StatRequest) (StatObservation, error) {
	p.calls.Add(1)
	return StatObservation{}, errors.New("diagnostic started an extra native sample")
}

type counterWindowFixture struct {
	service *Service
	query   diagnostics.InterfaceCounterRequest
	claims  authn.Claims
	port    Binding
	members []Binding
	source  *unexpectedCounterCollector
	now     time.Time
}

func newCounterWindow(t *testing.T, count int) counterWindowFixture {
	t.Helper()
	f := counterWindowFixture{service: New(nil), source: &unexpectedCounterCollector{}, now: time.Now().UTC()}
	f.service.now = func() time.Time { return f.now }
	f.service.failure = ""
	v := &view{
		observation: Observation{
			Rows: Rows{}, Schema: Schema{Digest: "synthetic-schema"},
			Evidence: Evidence{ObservedAt: f.now, Peer: "synthetic-ovsdb-peer"},
		},
		decision: Decision{State: "confirmed", Generation: "ad316dba-c6dd-468a-b12a-00000000feed", Bindings: map[string]Binding{}},
	}
	object := func(table string, n int, values map[string]any) Binding {
		binding := Binding{
			UUID: fmt.Sprintf("34a93135-0617-4768-a6b3-%012d", n), ManagementID: fmt.Sprintf("7c5789d5-c74e-4cf1-b22e-%012d", n),
			Table: table, State: "active",
		}
		if v.observation.Rows[table] == nil {
			v.observation.Rows[table] = map[string]Row{}
		}
		v.observation.Rows[table][binding.UUID] = Row{UUID: binding.UUID, Values: values}
		v.decision.Bindings[Key(table, binding.UUID)] = binding
		return binding
	}
	native := make([]any, 0, count)
	for i := range count {
		member := object("Interface", i+1, map[string]any{"name": "private-interface", "ifindex": i + 1})
		f.members = append(f.members, member)
		native = append(native, member.UUID)
	}
	f.port = object("Port", 101, map[string]any{"name": "private-port", "interfaces": native})
	bridge := object("Bridge", 201, map[string]any{"ports": []any{f.port.UUID}})
	root := object("Open_vSwitch", 301, map[string]any{"bridges": []any{bridge.UUID}})
	v.observation.Evidence.Root = root.UUID
	f.service.current = v
	f.query = diagnostics.InterfaceCounterRequest{Target: apitypes.Ref{Kind: "port", ID: f.port.ManagementID}, SampleSeconds: 5}
	f.claims = authn.Claims{Capabilities: []string{"diagnostics.active", "inventory.read"}}
	cache := statisticsCache{observer: f.source, entries: map[string]statisticEntry{}, observedAt: f.now.Add(-5 * time.Second)}
	for _, binding := range f.members {
		first := statisticPoint{at: cache.observedAt, valid: 255, values: [8]uint64{10, 25}}
		cache.entries[binding.ManagementID] = statisticEntry{
			binding: binding, association: statisticAssociation(v, binding), generation: v.decision.Generation,
			schema: v.observation.Schema.Digest, samples: []statisticPoint{first},
		}
	}
	f.service.statistics = cache
	return f
}

func (f counterWindowFixture) followUp(edit func(int, *statisticPoint)) {
	cache := &f.service.statistics
	cache.observedAt = f.now.Add(-time.Millisecond)
	for i, binding := range f.members {
		entry := cache.entries[binding.ManagementID]
		point := statisticPoint{at: cache.observedAt, valid: 255, values: [8]uint64{15, 25}}
		if edit != nil {
			edit(i, &point)
		}
		entry.samples = append(entry.samples, point)
		cache.entries[binding.ManagementID] = entry
	}
}

func TestInterfaceCounterDiagnosticUsesOnlyAdjacentScopedPublishedSamples(t *testing.T) {
	f := newCounterWindow(t, 5)
	if err := f.service.CheckInterfaceCounterTarget(t.Context(), f.query, f.claims); err != nil {
		t.Fatal(err)
	}
	out := InterfaceCounterDiagnostic{Target: f.query.Target}
	var window interfaceCounterWindow
	for range 2 {
		if done, err := f.service.interfaceCounterPoll(t.Context(), &window, &out); done || err != nil {
			t.Fatalf("returned counter deltas without a second publication: %v, %v", done, err)
		}
	}
	f.followUp(nil)
	if done, err := f.service.interfaceCounterPoll(t.Context(), &window, &out); !done || err != nil {
		t.Fatalf("eligible published samples did not complete: %v, %v", done, err)
	}
	if out.State != "truncated" || out.Freshness != "fresh" || !out.Truncated || len(out.Members) != 4 ||
		out.FirstSampledAt == nil || out.SecondSampledAt == nil || !out.SecondSampledAt.After(*out.FirstSampledAt) {
		t.Fatalf("scope, times, or truncation lost: %+v", out)
	}
	for i, row := range out.Members {
		if row.Interface.ID != f.members[i].ManagementID || row.State != "observed" ||
			row.Deltas["rx_bytes"] == nil || *row.Deltas["rx_bytes"] != "5" ||
			row.Deltas["tx_bytes"] == nil || *row.Deltas["tx_bytes"] != "0" {
			t.Fatalf("unexpected scoped counter row: %+v", row)
		}
	}
	raw, err := json.Marshal(out)
	if err != nil || strings.Contains(string(raw), "private-interface") || strings.Contains(string(raw), f.port.UUID) ||
		f.source.calls.Load() != 0 {
		t.Fatalf("diagnosis sampled new native data or leaked an unscoped identity: %s, %v", raw, err)
	}
}

func TestInterfaceCounterDiagnosticConsumesActualSchedulerPublications(t *testing.T) {
	f := newCounterWindow(t, 1)
	now, reads, amount := f.now, 0, "100"
	f.service.now = func() time.Time { return now }
	observer := publishedCounterObserver(func(_ context.Context, req StatRequest) (StatObservation, error) {
		reads++
		counters := make(map[string]map[string]*string, len(req.Interfaces))
		for _, id := range req.Interfaces {
			value := amount
			counters[id] = map[string]*string{"rx_bytes": &value}
		}
		return StatObservation{ObservedAt: now, Root: req.Root, Schema: req.Schema, Counters: counters}, nil
	})
	if err := f.service.ConfigureStatistics(observer); err != nil {
		t.Fatal(err)
	}
	// The test advances the existing sampler explicitly. The diagnostic only
	// consumes those publications and cannot wake or call the observer.
	if err := f.service.SampleStatistics(t.Context()); err != nil {
		t.Fatal(err)
	}
	out := InterfaceCounterDiagnostic{Target: f.query.Target}
	var run interfaceCounterWindow
	if done, err := f.service.interfaceCounterPoll(t.Context(), &run, &out); done || err != nil || reads != 1 {
		t.Fatalf("baseline state or provider invocation incorrect: %v, %v, %d", done, err, reads)
	}
	now, amount = now.Add(StatisticsInterval), "133"
	if err := f.service.SampleStatistics(t.Context()); err != nil {
		t.Fatal(err)
	}
	done, err := f.service.interfaceCounterPoll(t.Context(), &run, &out)
	if !done || err != nil || reads != 2 || out.State != "partial" || len(out.Members) != 1 ||
		out.Members[0].Deltas["rx_bytes"] == nil || *out.Members[0].Deltas["rx_bytes"] != "33" {
		t.Fatalf("actual scheduler points were not consumed exactly once: %+v, %v, %d", out, err, reads)
	}
}

func TestStatisticsSchedulerPublishesAdjacentDiagnosticSamples(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const startDelay = 100 * time.Millisecond
		const collectionTime = 200 * time.Millisecond

		f := newCounterWindow(t, 1)
		start, reads := true, 0
		f.service.now = func() time.Time {
			if start {
				start = false
				// Model startup scheduling delay after the scheduler begins but
				// before SampleStatistics records its first admission clock.
				time.Sleep(startDelay)
			}
			return time.Now()
		}
		observer := publishedCounterObserver(func(_ context.Context, req StatRequest) (StatObservation, error) {
			reads++
			time.Sleep(collectionTime)
			value := fmt.Sprint(100 + 33*(reads-1))
			return StatObservation{
				ObservedAt: time.Now(), Root: req.Root, Schema: req.Schema,
				Counters: map[string]map[string]*string{f.members[0].UUID: {"rx_bytes": &value}},
			}, nil
		})
		if err := f.service.ConfigureStatistics(observer); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go f.service.RunStatistics(ctx)
		time.Sleep(startDelay + collectionTime)
		synctest.Wait()

		out := InterfaceCounterDiagnostic{Target: f.query.Target}
		var run interfaceCounterWindow
		if done, err := f.service.interfaceCounterPoll(ctx, &run, &out); done || err != nil || run.first.IsZero() || reads != 1 {
			t.Fatalf("first scheduled publication was not available as a baseline: %v, %v, %d", done, err, reads)
		}
		time.Sleep(StatisticsInterval - time.Millisecond)
		synctest.Wait()
		if reads != 1 {
			t.Fatalf("scheduler attempted another collection before the full interval: %d", reads)
		}
		time.Sleep(time.Millisecond + collectionTime)
		synctest.Wait()
		done, err := f.service.interfaceCounterPoll(ctx, &run, &out)
		if !done || err != nil || reads != 2 || out.Freshness != "fresh" || out.State != "partial" || len(out.Members) != 1 ||
			out.SecondSampledAt == nil || out.SecondSampledAt.Sub(run.first) != StatisticsInterval+collectionTime ||
			out.Members[0].Deltas["rx_bytes"] == nil || *out.Members[0].Deltas["rx_bytes"] != "33" {
			t.Fatalf("second scheduled publication could not be compared to its adjacent baseline: %+v, %v, %d", out, err, reads)
		}
	})
}

func TestInterfaceCounterDiagnosticRejectsResetGapAndChangedSource(t *testing.T) {
	f := newCounterWindow(t, 4)
	out := InterfaceCounterDiagnostic{Target: f.query.Target}
	var run interfaceCounterWindow
	_, _ = f.service.interfaceCounterPoll(t.Context(), &run, &out)
	f.followUp(func(i int, point *statisticPoint) {
		switch i {
		case 0:
			point.reset, point.discontinuity = true, "counter-reset"
		case 1:
			point.coalesced, point.discontinuity = true, "collection-gap"
		case 2:
			point.valid = 1
		case 3:
			point.values[0] = 1 // independently reject negative delta without relying on a marker.
		}
	})
	done, err := f.service.interfaceCounterPoll(t.Context(), &run, &out)
	if !done || err != nil || out.State != "partial" || len(out.Members) != 4 {
		t.Fatalf("missing explicit partial evidence: %+v, %v", out, err)
	}
	for i, want := range []string{"DIAGNOSTIC_COUNTER_RESET", "DIAGNOSTIC_COUNTER_SAMPLE_GAP", "DIAGNOSTIC_COUNTER_FIELDS_UNKNOWN", "DIAGNOSTIC_COUNTER_RESET"} {
		if out.Members[i].Reason != want || out.Members[i].Deltas["tx_bytes"] != nil {
			t.Fatalf("unknown/negative value was not fenced: %+v", out.Members[i])
		}
	}
	if out.Members[2].Deltas["rx_bytes"] == nil || *out.Members[2].Deltas["rx_bytes"] != "5" {
		t.Fatal("independently present field lost")
	}

	for _, change := range []string{"generation", "membership", "interface-association", "cache-source-epoch"} {
		t.Run(change, func(t *testing.T) {
			f := newCounterWindow(t, 2)
			out := InterfaceCounterDiagnostic{Target: f.query.Target}
			var run interfaceCounterWindow
			_, _ = f.service.interfaceCounterPoll(t.Context(), &run, &out)
			f.followUp(nil)
			switch change {
			case "generation":
				next := *f.service.current
				next.decision.Generation = "ad316dba-c6dd-468a-b12a-00000000face"
				f.service.current = &next
			case "membership":
				f.replaceValue(f.port, "interfaces", []any{f.members[0].UUID})
			case "interface-association":
				f.replaceValue(f.members[0], "ifindex", 500)
			case "cache-source-epoch":
				f.service.statisticsEpoch++
			}
			done, err := f.service.interfaceCounterPoll(t.Context(), &run, &out)
			if !done || err != nil || out.State != "evidence-unavailable" || len(out.Members) != 0 {
				t.Fatalf("scope drift was not fenced: %+v, %v", out, err)
			}
		})
	}
}

func (f counterWindowFixture) replaceValue(binding Binding, key string, value any) {
	next := *f.service.current
	next.observation.Rows = maps.Clone(next.observation.Rows)
	rows := maps.Clone(next.observation.Rows[binding.Table])
	row := rows[binding.UUID]
	row.Values = maps.Clone(row.Values)
	row.Values[key] = value
	rows[binding.UUID] = row
	next.observation.Rows[binding.Table] = rows
	f.service.current = &next
}

func TestInterfaceCounterDiagnosticAuthorizationTimeoutAndCancellation(t *testing.T) {
	f := newCounterWindow(t, 1)
	_, err := f.service.ObserveInterfaceCounterDiagnostic(t.Context(), f.query,
		authn.Claims{Capabilities: []string{"inventory.read", "diagnostics.read"}})
	var p *apitypes.Problem
	if !errors.As(err, &p) || p.Code != "CAPABILITY_DENIED" {
		t.Fatalf("read-only grant accessed Active counter window: %v", err)
	}
	bad := f.query
	bad.SampleSeconds = 6
	if _, err := f.service.ObserveInterfaceCounterDiagnostic(t.Context(), bad, f.claims); err == nil {
		t.Fatal("unexpected duration accepted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	out, err := f.service.ObserveInterfaceCounterDiagnostic(ctx, f.query, f.claims)
	if err != nil || out.State != "timed-out" || len(out.Members) != 0 || out.Source != "ovsdb-statistics" {
		t.Fatalf("deadline returned unproven evidence: %+v, %v", out, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	out, err = f.service.ObserveInterfaceCounterDiagnostic(ctx, f.query, f.claims)
	if err != nil || out.State != "cancelled" || len(out.Members) != 0 || f.source.calls.Load() != 0 {
		t.Fatalf("cancel was not independently recognized: %+v, %v", out, err)
	}
}
