//go:build linux

package ovsdb

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func TestStatisticsObserverChecksUnixPeerAndOnlyReadsBoundCounters(t *testing.T) {
	schemaBody, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := discover(schemaBody)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"read-only", "schema-replaced", "peer-denied", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			control := controlServer(t, func(conn net.Conn) {
				decoder := json.NewDecoder(conn)
				var request map[string]any
				if decoder.Decode(&request) != nil {
					return
				}
				requests <- request
				if scenario == "timeout" {
					var discarded any
					_ = decoder.Decode(&discarded)
					return
				}
				if json.NewEncoder(conn).Encode(map[string]any{"id": 1, "result": json.RawMessage(schemaBody), "error": nil}) != nil {
					return
				}
				if decoder.Decode(&request) != nil {
					return
				}
				requests <- request
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": 2, "result": statisticsTestResult(), "error": nil})
			})
			provider := &Provider{options: Options{Socket: control.Socket, PeerUID: control.PeerUID}}
			req := inventory.StatRequest{Root: statisticsRootID, Schema: discovered.public.Digest, Interfaces: []string{statisticsInterfaceID}}
			if scenario == "schema-replaced" {
				req.Schema = strings.Repeat("a", 64)
			}
			if scenario == "peer-denied" {
				provider.options.PeerUID++
			}
			budget := statisticsReadBudget
			if scenario == "timeout" {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			start := time.Now()
			sample, err := provider.ObserveStatistics(ctx, req)
			if scenario != "read-only" {
				if err == nil || sample.Counters != nil || !sample.ObservedAt.IsZero() || time.Since(start) > time.Second {
					t.Fatal("failed observation returned partial data or exceeded its deadline", sample, err)
				}
				expected := "STATISTICS_UNAVAILABLE"
				if scenario == "schema-replaced" {
					expected = "STATISTICS_SOURCE_CHANGED"
				}
				if err.Error() != expected {
					t.Fatal("raw failure detail escaped the closed statistics error", err)
				}
				return
			}
			if err != nil || sample.Root != req.Root || sample.Schema != req.Schema || sample.ObservedAt.Before(start) ||
				sample.Coalesced || sample.Counters[statisticsInterfaceID]["rx_bytes"] == nil || *sample.Counters[statisticsInterfaceID]["rx_bytes"] != "77" {
				t.Fatal("requested native counter sample unavailable", sample, err)
			}
			discovery := <-requests
			if discovery["method"] != "get_schema" {
				t.Fatal("statistics observer did not independently validate its schema")
			}
			read := <-requests
			actual, _ := json.Marshal(read["params"])
			expected, _ := json.Marshal(statisticsOperations())
			if read["method"] != "transact" || string(actual) != string(expected) {
				t.Fatal("statistics observer broadened its native read transaction", read)
			}
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			if err := request(client, 3, "transact", nil); err == nil || err.Error() != "OVSDB_METHOD_DENIED" {
				t.Fatal("statistics observer broadened the inventory RPC allowlist")
			}
		})
	}
}
