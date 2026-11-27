package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type statisticsObserverFunc func(context.Context, StatRequest) (StatObservation, error)

func (f statisticsObserverFunc) ObserveStatistics(ctx context.Context, request StatRequest) (StatObservation, error) {
	return f(ctx, request)
}

type statisticsTest struct {
	service     *Service
	observation Observation
	decision    Decision
	claims      authn.Claims
	binding     Binding
	now         time.Time
	values      map[string]*string
	reads       int
}

func newStatisticsTest(t *testing.T) *statisticsTest {
	t.Helper()
	s, o, d, c := fixture(t)
	f := &statisticsTest{service: s, observation: o, decision: d, claims: c, now: o.Evidence.ObservedAt, values: map[string]*string{}}
	f.observation.Evidence.Root = repository.NewID()
	for _, b := range d.Bindings {
		if b.Table == "Interface" {
			f.binding = b
			break
		}
	}
	s.now = func() time.Time { return f.now }
	observer := statisticsObserverFunc(func(_ context.Context, request StatRequest) (StatObservation, error) {
		f.reads++
		out := StatObservation{ObservedAt: f.now, Root: request.Root, Schema: request.Schema, Counters: map[string]map[string]*string{}}
		for _, id := range request.Interfaces {
			out.Counters[id] = f.values
		}
		return out, nil
	})
	if err := s.ConfigureStatistics(observer); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *statisticsTest) set(key, value string) { f.values[key] = &value }

func (f *statisticsTest) sample(t *testing.T, elapsed time.Duration) {
	t.Helper()
	f.now = f.now.Add(elapsed)
	f.observation.Evidence.ObservedAt = f.now
	f.service.install(f.observation, f.decision)
	if err := f.service.SampleStatistics(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *statisticsTest) read(t *testing.T) map[string]any {
	t.Helper()
	out, err := f.service.Read(context.Background(), "readStatistic", map[string]string{"statistic_id": f.binding.ManagementID}, nil, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)["statistics"].(map[string]any)
}

func TestStatisticVersionsMatchAuthorizedReadsWithoutSampling(t *testing.T) {
	f := newStatisticsTest(t)
	f.claims.Capabilities = []string{"inventory.read", "state.read"}
	f.set("rx_bytes", "10")
	f.sample(t, 0)
	id, absent := f.binding.ManagementID, repository.NewID()
	assertVersion := func() string {
		t.Helper()
		version := f.service.StatisticVersions([]string{id, absent})
		value, err := f.service.Read(t.Context(), "readStatistic", map[string]string{"statistic_id": id}, nil, f.claims)
		if err != nil {
			t.Fatal(err)
		}
		if len(version) != 1 || version[id] == "" || version[id] != value.(map[string]any)["resource_version"] {
			t.Fatalf("private hint must match the REST version of just the known scope: %v", version)
		}
		return version[id]
	}
	first := assertVersion()
	if repeat := assertVersion(); repeat != first || f.reads != 1 {
		t.Fatal("reading versions changed the cache or launched another round")
	}
	f.set("rx_bytes", "11")
	f.sample(t, StatisticsInterval)
	updated := assertVersion()
	if updated == first || f.reads != 2 {
		t.Fatal("a published round did not advance its scoped version")
	}
	f.now = f.now.Add(statisticsFreshFor + time.Second)
	if stale := assertVersion(); stale == updated || f.reads != 2 {
		t.Fatal("expired evidence did not advance the version without sampling")
	}
}

func TestStatisticsExactDeltaResetMissingAndSharedReads(t *testing.T) {
	f := newStatisticsTest(t)
	f.claims.Capabilities = []string{"inventory.read", "state.read"}
	f.set("rx_bytes", "9007199254740993")
	f.set("tx_bytes", "0")
	f.sample(t, 0)
	first := f.read(t)
	if *first["counters"].(map[string]*string)["rx_bytes"] != "9007199254740993" || first["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("first sample lost exactness or invented a rate", first)
	}
	for range 20 {
		_ = f.read(t)
	}
	if f.reads != 1 {
		t.Fatal("HTTP reader created samples", f.reads)
	}
	f.set("rx_bytes", "9007199254740994")
	f.sample(t, StatisticsInterval)
	rate := f.read(t)["rates"].(map[string]*float64)["rx_bytes"]
	if rate == nil || *rate != 0.2 {
		t.Fatal("delta used an imprecise cumulative float", rate)
	}
	f.set("rx_bytes", "1")
	f.sample(t, StatisticsInterval)
	reset := f.read(t)
	if reset["reset"] != true || reset["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("reset joined counter epochs", reset)
	}
	delete(f.values, "rx_bytes")
	f.sample(t, StatisticsInterval)
	if f.read(t)["counters"].(map[string]*string)["rx_bytes"] != nil {
		t.Fatal("missing became zero")
	}
	f.set("rx_bytes", "4")
	f.sample(t, StatisticsInterval)
	if f.read(t)["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("rate crossed a missing counter")
	}
}

func TestStatisticsBreaksBriefOutageAndNativeAssociationChanges(t *testing.T) {
	f := newStatisticsTest(t)
	f.set("rx_bytes", "10")
	f.sample(t, 0)
	f.service.Unavailable("OVSDB_UNAVAILABLE")
	stale := f.read(t)
	if stale["source"].(map[string]any)["freshness"] != "stale" || stale["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("outage retained fresh rate")
	}
	f.service.install(f.observation, f.decision)
	if resumed := f.read(t); resumed["source"].(map[string]any)["freshness"] == "fresh" || resumed["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("monitor reconnect revived an old statistics sample")
	}
	f.set("rx_bytes", "20")
	f.sample(t, StatisticsInterval)
	after := f.read(t)
	if *after["discontinuity"].(*string) != "source-changed" || after["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("brief outage connected the trend", after)
	}
	row := f.observation.Rows["Interface"][f.binding.UUID]
	row.Values["type"] = "internal"
	f.observation.Rows["Interface"][f.binding.UUID] = row
	f.set("rx_bytes", "30")
	f.sample(t, StatisticsInterval)
	if f.read(t)["sample_count"] != 1 {
		t.Fatal("history crossed a changed native device source")
	}
	f.sample(t, 2*StatisticsInterval)
	gap := f.read(t)
	if gap["coalesced"] != true || gap["rates"].(map[string]*float64)["rx_bytes"] != nil {
		t.Fatal("skipped slot became a continuous rate")
	}
}

func TestStatisticsRetentionResponseBudgetAndScopedCursor(t *testing.T) {
	f := newStatisticsTest(t)
	for index := range 65 {
		f.set("rx_bytes", strconv.Itoa(index))
		f.sample(t, StatisticsInterval)
	}
	stats := f.read(t)
	if stats["sample_count"] != 60 {
		t.Fatal("unbounded or incorrect history", stats["sample_count"])
	}
	blob, err := json.Marshal(stats)
	if err != nil || len(blob) > 40<<10 {
		t.Fatal("detail exceeded IPC budget", len(blob), err)
	}
	// Fixed arrays bound the retained data without accounting for arbitrary
	// vendor maps. Projection maps only exist while an authorized read runs.
	if unsafe.Sizeof(statisticPoint{})*statisticsSamples*statisticsObjects > 32<<20 {
		t.Fatal("retained samples exceeded fixed memory budget")
	}
	q := url.Values{"limit": {"1"}}
	page, err := f.service.Read(context.Background(), "listStatistics", nil, q, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	q.Set("cursor", page.(map[string]any)["next_cursor"].(string))
	f.sample(t, StatisticsInterval)
	if _, err = f.service.Read(context.Background(), "listStatistics", nil, q, f.claims); err != nil {
		t.Fatal("counter sampling invalidated inventory selection", err)
	}
	changed := f.claims
	changed.Revision = repository.NewID()
	if _, err = f.service.Read(context.Background(), "listStatistics", nil, q, changed); err == nil {
		t.Fatal("permission cursor survived revocation")
	}
	f.now = f.now.Add(StatisticsRetention + time.Second)
	if f.read(t)["sample_count"] != 0 {
		t.Fatal("expired history remained visible")
	}
	f.service.Unavailable("OVSDB_UNAVAILABLE")
	if err := f.service.SampleStatistics(context.Background()); err == nil {
		t.Fatal("sampling unavailable inventory succeeded")
	}
	if len(f.service.statistics.entries) != 0 {
		t.Fatal("expired private history remained allocated after failed collection")
	}
}
