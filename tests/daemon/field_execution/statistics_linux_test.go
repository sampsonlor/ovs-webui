//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

type nativeStatisticsSample struct {
	Counters      map[string]*string  `json:"counters"`
	Rates         map[string]*float64 `json:"rates"`
	SampledAt     *time.Time          `json:"sampled_at"`
	Reset         bool                `json:"reset"`
	Discontinuity string              `json:"discontinuity"`
	Coalesced     bool                `json:"coalesced"`
}

type nativeStatistics struct {
	nativeStatisticsSample
	State            string                     `json:"state"`
	Reason           string                     `json:"reason"`
	SampleCount      int                        `json:"sample_count"`
	IntervalMS       int                        `json:"interval_ms"`
	RetentionSeconds int                        `json:"retention_seconds"`
	Source           struct{ Freshness string } `json:"source"`
	History          []nativeStatisticsSample   `json:"history"`
}

type nativeStatisticsResource struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Statistics nativeStatistics `json:"statistics"`
}

type statisticsFixture struct {
	*fixture
	port, intf candidate.Binding
	lastSample time.Time
}

func newStatisticsFixture(t *testing.T) *statisticsFixture {
	t.Helper()
	f := &statisticsFixture{fixture: newFixture(t)}
	f.port = f.binding("field-p1")
	f.intf = f.interfaceBinding("field-p1")
	must(t, f.inventory.ConfigureStatistics(f.provider))
	f.waitStatisticsSource()
	return f
}

func (f *statisticsFixture) waitStatisticsSource() {
	f.t.Helper()
	waitFor(f.t, func() bool {
		view, err := f.inventory.ExecutionView(f.ctx, []candidate.Binding{f.port})
		if err != nil {
			return false
		}
		root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
		progress := strings.Fields(f.vs("get", "Open_vSwitch", ".", "cur_cfg", "next_cfg"))
		if len(progress) != 2 || progress[0] != progress[1] || root.Values["cur_cfg"] != progress[0] || root.Values["next_cfg"] != progress[1] {
			return false
		}
		return f.nativeCounters("field-p1")["rx_packets"] != ""
	})
}

func (f *statisticsFixture) interfaceBinding(name string) candidate.Binding {
	f.t.Helper()
	uuid := f.vs("get", "Interface", name, "_uuid")
	var b candidate.Binding
	waitFor(f.t, func() bool {
		return f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, "SELECT management_id,ovs_uuid,table_name,generation FROM identities WHERE table_name='Interface' AND state='active' AND ovs_uuid=?", uuid).Scan(&b.ManagementID, &b.OVSUUID, &b.Table, &b.Generation)
		}) == nil
	})
	return b
}

func (f *statisticsFixture) sample() error {
	f.t.Helper()
	if remaining := time.Until(f.lastSample.Add(5100 * time.Millisecond)); !f.lastSample.IsZero() && remaining > 0 {
		time.Sleep(remaining)
	}
	err := f.inventory.SampleStatistics(f.ctx)
	f.lastSample = time.Now()
	return err
}

func (f *statisticsFixture) readStatistics(path string, query url.Values, claims authn.Claims) ([]byte, error) {
	f.t.Helper()
	contract, err := apicontract.New()
	must(f.t, err)
	op, params, _ := contract.Match("GET", "/api/v1"+path)
	if op == nil {
		f.t.Fatal("Statistics API operation is absent", path)
	}
	result, err := f.inventory.Read(f.ctx, op.ID, params, query, claims)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	must(f.t, err)
	must(f.t, op.ValidateResponse(200, data))
	return data, nil
}

func (f *statisticsFixture) detail(id string) nativeStatisticsResource {
	f.t.Helper()
	data, err := f.readStatistics("/statistics/"+id, nil, f.login.Claims)
	must(f.t, err)
	var resource nativeStatisticsResource
	must(f.t, json.Unmarshal(data, &resource))
	return resource
}

func (f *statisticsFixture) nativeCounters(name string) map[string]string {
	f.t.Helper()
	raw := f.vs("--format=json", "--columns=statistics", "list", "Interface", name)
	var result struct{ Data [][]json.RawMessage }
	must(f.t, json.Unmarshal([]byte(raw), &result))
	if len(result.Data) != 1 || len(result.Data[0]) != 1 {
		f.t.Fatal("native Interface statistics row is missing", raw)
	}
	var datum []json.RawMessage
	must(f.t, json.Unmarshal(result.Data[0][0], &datum))
	if len(datum) != 2 {
		f.t.Fatal("native Interface statistics map is invalid", raw)
	}
	var pairs [][]json.RawMessage
	must(f.t, json.Unmarshal(datum[1], &pairs))
	counters := map[string]string{}
	for _, pair := range pairs {
		if len(pair) != 2 {
			f.t.Fatal("native Interface statistics pair is invalid")
		}
		var key string
		var value json.Number
		must(f.t, json.Unmarshal(pair[0], &key))
		must(f.t, json.Unmarshal(pair[1], &value))
		counters[key] = value.String()
	}
	return counters
}

func (f *statisticsFixture) receivePackets(count int) {
	f.t.Helper()
	packet := "02000000000202000000000108060001080006040001020000000001c6126d01000000000000c6126d02"
	args := []string{"ovs-appctl", "-t", filepath.Join(f.root, "switch.ctl"), "netdev-dummy/receive", "field-p1"}
	for range count {
		args = append(args, packet)
	}
	f.run(args...)
}

func (f *statisticsFixture) evidenceCount() int {
	f.t.Helper()
	var count int
	must(f.t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT count(*) FROM evidence_records").Scan(&count)
	}))
	return count
}

func (f *statisticsFixture) evidenceSince(count int) []string {
	f.t.Helper()
	records := []string{}
	must(f.t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		rows, err := q.QueryContext(ctx, "SELECT operation,source,document FROM evidence_records ORDER BY created_at_ms,id LIMIT -1 OFFSET ?", count)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var operation, source string
			var document []byte
			if err := rows.Scan(&operation, &source, &document); err != nil {
				return err
			}
			var record struct {
				Reason any `json:"reason"`
			}
			if err := json.Unmarshal(document, &record); err != nil {
				return err
			}
			records = append(records, fmt.Sprintf("%s source=%s reason=%v", operation, source, record.Reason))
		}
		return rows.Err()
	}))
	return records
}

func TestNativeStatisticsObservation(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires the explicit isolated Linux OVS statistics fixture")
	}
	t.Run("traffic_updates_shared_counters_and_bounded_RAM_history_without_writes", func(t *testing.T) {
		f := newStatisticsFixture(t)
		// OVS publishes its runtime counters at a native five-second cadence.
		// Establish a real native baseline before comparing the shared read.
		waitFor(t, func() bool { return f.nativeCounters("field-p1")["rx_packets"] != "" })
		must(t, f.sample())
		first := f.detail(f.intf.ManagementID).Statistics
		if first.SampledAt == nil || first.Counters["rx_packets"] == nil || first.IntervalMS != 5000 || first.RetentionSeconds != 300 {
			t.Fatal("Statistics did not expose the bounded native sampling contract", first)
		}
		original, err := strconv.ParseUint(*first.Counters["rx_packets"], 10, 64)
		must(t, err)
		baseline, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{f.port})
		must(t, err)
		evidence := f.evidenceCount()
		f.receivePackets(7)
		waitFor(t, func() bool {
			count, err := strconv.ParseUint(f.nativeCounters("field-p1")["rx_packets"], 10, 64)
			return err == nil && count >= original+7
		})
		must(t, f.sample())
		second := f.detail(f.intf.ManagementID).Statistics
		native := f.nativeCounters("field-p1")
		for _, key := range []string{"rx_bytes", "tx_bytes", "rx_packets", "tx_packets", "rx_errors", "tx_errors", "rx_dropped", "tx_dropped"} {
			actual := second.Counters[key]
			if expected, reported := native[key]; reported {
				if actual == nil || *actual != expected {
					t.Fatalf("shared %s counter differs from the native Interface value %s", key, expected)
				}
			} else if actual != nil {
				t.Fatalf("unreported native %s became a known shared value", key)
			}
		}
		if second.SampledAt == nil || !second.SampledAt.After(*first.SampledAt) || second.Reset || len(second.History) < 2 || len(second.History) > 60 {
			t.Fatal("shared history did not retain successive native samples", second)
		}
		knownRates := 0
		for _, rate := range second.Rates {
			if rate != nil {
				knownRates++
				if *rate < 0 {
					t.Fatal("monotonic counters produced a negative rate")
				}
			}
		}
		if knownRates != 4 {
			t.Fatal("the second complete native sample did not produce four traffic rates", second.Rates)
		}
		claims := f.login.Claims
		claims.Capabilities = []string{"inventory.read", "state.read"}
		for range 20 {
			data, err := f.readStatistics("/statistics/"+f.intf.ManagementID, nil, claims)
			must(t, err)
			var resource nativeStatisticsResource
			must(t, json.Unmarshal(data, &resource))
			if resource.Statistics.SampleCount != second.SampleCount || resource.Statistics.SampledAt == nil || !resource.Statistics.SampledAt.Equal(*second.SampledAt) {
				t.Fatal("client reads triggered private sampling or changed the shared history")
			}
		}
		data, err := f.readStatistics("/statistics", url.Values{"port_id": {f.port.ManagementID}}, claims)
		must(t, err)
		var page struct{ Items []nativeStatisticsResource }
		must(t, json.Unmarshal(data, &page))
		if len(page.Items) != 1 || page.Items[0].ID != f.intf.ManagementID {
			t.Fatal("Port selection did not resolve its exact observed member Interface", string(data))
		}
		for _, capabilities := range [][]string{{"state.read"}, {"inventory.read"}} {
			claims.Capabilities = capabilities
			if _, err := f.readStatistics("/statistics/"+f.intf.ManagementID, nil, claims); err == nil {
				t.Fatal("Statistics read accepted incomplete observation permission")
			}
		}
		if f.proxy.sent.Load() != 0 {
			t.Fatal("Statistics collection or reads changed native OVS state")
		}
		after, err := f.inventory.CandidateSnapshot(f.ctx, []candidate.Binding{f.port})
		must(t, err)
		if after.Revision != baseline.Revision {
			t.Fatalf("counter sampling changed the desired configuration revision from %s to %s; before=%+v after=%+v", baseline.Revision, after.Revision, baseline, after)
		}
		if count := f.evidenceCount(); count != evidence {
			t.Fatalf("evidence records changed from %d to %d during counter sampling: %v", evidence, count, f.evidenceSince(evidence))
		}
	})
	t.Run("native_source_outage_withholds_fresh_values_and_recovery_breaks_trend", func(t *testing.T) {
		f := newStatisticsFixture(t)
		must(t, f.sample())
		first := f.detail(f.intf.ManagementID).Statistics
		f.stop("db")
		_ = f.sample()
		unavailable := f.detail(f.intf.ManagementID).Statistics
		if unavailable.Source.Freshness == "fresh" {
			t.Fatal("stopped OVSDB retained fresh Statistics", unavailable)
		}
		for _, rate := range unavailable.Rates {
			if rate != nil {
				t.Fatal("stopped OVSDB retained a current traffic rate")
			}
		}
		f.startDB()
		waitFor(t, func() bool { _, err := f.inventory.CandidateSnapshot(f.ctx, nil); return err == nil })
		// OVSDB discards ephemeral statistics at restart. The live daemon
		// reconnects and repopulates them on its own statistics cadence.
		f.waitStatisticsSource()
		must(t, f.sample())
		recovered := f.detail(f.intf.ManagementID).Statistics
		if recovered.SampledAt == nil || first.SampledAt != nil && !recovered.SampledAt.After(*first.SampledAt) || recovered.Source.Freshness != "fresh" {
			t.Fatal("Statistics did not recover from actual OVSDB reconnection", recovered)
		}
		if recovered.Discontinuity == "" || len(recovered.History) > 60 {
			t.Fatal("Statistics bridged the disconnected source interval", recovered)
		}
		for _, rate := range recovered.Rates {
			if rate != nil {
				t.Fatal("Statistics derived a rate across a native source outage")
			}
		}
	})
	t.Run("same_name_recreation_uses_fresh_identity_and_history", func(t *testing.T) {
		f := newStatisticsFixture(t)
		must(t, f.sample())
		before := f.detail(f.intf.ManagementID)
		f.vs("del-port", telemetryBridge, "field-p1")
		waitFor(t, func() bool {
			_, err := f.readStatistics("/statistics/"+f.intf.ManagementID, nil, f.login.Claims)
			return err != nil
		})
		f.vs("add-port", telemetryBridge, "field-p1", "--", "set", "Interface", "field-p1", "type=dummy")
		replacement := f.interfaceBinding("field-p1")
		if replacement.OVSUUID == f.intf.OVSUUID || replacement.ManagementID == f.intf.ManagementID {
			t.Fatal("same-name Interface creation reused the retired immutable identity")
		}
		must(t, f.sample())
		after := f.detail(replacement.ManagementID)
		if after.ID == before.ID || len(after.Statistics.History) > 1 || after.Statistics.Reset {
			t.Fatal("new native Interface inherited the retired object's counter history", after)
		}
		for _, rate := range after.Statistics.Rates {
			if rate != nil {
				t.Fatal("new native Interface inherited an old rate")
			}
		}
	})
}
