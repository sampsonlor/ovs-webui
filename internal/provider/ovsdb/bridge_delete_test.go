package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestDeletionPreflightUsesPrivateReadOnlyTransport(t *testing.T) {
	for _, scenario := range []string{"success", "guard-conflict", "wrong-reply", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			d, v, _ := executionFixture(t, "3.3.9")
			bind := func(table string) candidate.Binding {
				return candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: v.Candidate.Generation}
			}
			graph := candidate.BridgeGraph{Name: "br-del", Root: v.Observation.Evidence.Root, Bridge: bind("Bridge"), Port: bind("Port"), Interface: bind("Interface")}
			c := candidate.Candidate{Intents: []candidate.StoredIntent{{Operation: candidate.BridgeDelete, Object: graph.Bridge, Deletion: &candidate.BridgeDeletion{Source: graph, SourceMarker: strings.Repeat("a", 64)}}}}
			client, server := net.Pipe()
			defer client.Close()
			done := make(chan error, 1)
			go func() {
				defer server.Close()
				var request struct {
					Method string
					ID     int
					Params []json.RawMessage
				}
				decoder := json.NewDecoder(server)
				if err := decoder.Decode(&request); err != nil {
					done <- err
					return
				}
				if request.Method != "transact" || request.ID != 4 || len(request.Params) < 2 {
					done <- errors.New("missing native proof")
					return
				}
				results := []any{}
				for _, raw := range request.Params[1:] {
					var op map[string]any
					if json.Unmarshal(raw, &op) != nil || op["op"] != "wait" || op["timeout"] != float64(0) {
						done <- errors.New("preflight contained a mutation or blocking wait")
						return
					}
					results = append(results, map[string]any{})
				}
				if scenario == "disconnect" {
					done <- nil
					return
				}
				if err := send(server, map[string]any{"method": "echo", "params": []any{}, "id": 9}); err != nil {
					done <- err
					return
				}
				var echo map[string]any
				if err := decoder.Decode(&echo); err != nil {
					done <- err
					return
				}
				if echo["id"] != float64(9) {
					done <- errors.New("echo not answered")
					return
				}
				if scenario == "guard-conflict" {
					results[0] = map[string]any{"error": "timed out"}
				}
				id := 4
				if scenario == "wrong-reply" {
					id = 99
				}
				done <- send(server, map[string]any{"id": id, "result": results, "error": nil})
			}()
			err := verifyDeletionBefore(context.Background(), client, bufio.NewReader(client), d, c, strings.Repeat("a", 64))
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				want := "BRIDGE_PREFLIGHT_UNAVAILABLE"
				if scenario == "guard-conflict" {
					want = "ISOLATED_BRIDGE_GRAPH_CHANGED"
				}
				var problem *apitypes.Problem
				if !errors.As(err, &problem) || problem.Code != want {
					t.Fatal(scenario, err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			// The general discovery helper still refuses arbitrary transactions.
			if request(client, 4, "transact", nil) == nil {
				t.Fatal("discovery write boundary widened")
			}
		})
	}
}

func TestDeletionNativeGraphsUseDistinctMarkersAndFreshUUIDs(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			bind := func(table string) candidate.Binding {
				return candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: v.Candidate.Generation}
			}
			graph := func() candidate.BridgeGraph {
				return candidate.BridgeGraph{Name: "br-del", Root: v.Observation.Evidence.Root, Bridge: bind("Bridge"), Port: bind("Port"), Interface: bind("Interface")}
			}
			original, replacement := graph(), graph()
			i := candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.BridgeDelete, Object: original.Bridge,
				Deletion: &candidate.BridgeDeletion{Source: original, Replacement: replacement, SourceMarker: strings.Repeat("a", 64)}}
			e.Candidate.Intents = []candidate.StoredIntent{i}
			p, err := compileExecution(repository.NewID(), strings.Repeat("b", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if err = json.Unmarshal(p.Native, &n); err != nil {
				t.Fatal(err)
			}
			if len(n.InsertUUIDs) != 0 || n.CreationMarker != i.Deletion.SourceMarker || strings.Contains(string(p.Native), `"op":"delete"`) {
				t.Fatal("deletion is not guarded root detach")
			}
			for _, needle := range []string{`"Mirror"`, `"qos"`, `"ingress_policing_rate"`} {
				if !strings.Contains(string(p.Native), needle) {
					t.Fatal("missing dependency guard", needle)
				}
			}
			candidate.Reverse(&e.Candidate.Intents[0])
			p, err = compileBridgeExecution(p.ID, strings.Repeat("c", 64), strings.Repeat("c", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(p.Native, &n); err != nil {
				t.Fatal(err)
			}
			if len(n.InsertUUIDs) != 3 {
				t.Fatal("missing replacement rows")
			}
			for index, uuid := range n.InsertUUIDs {
				for _, b := range original.Bindings() {
					if uuid == b.OVSUUID {
						t.Fatal("old identity resurrected")
					}
				}
				if n.Operations[index]["op"] != "insert" {
					t.Fatal(index)
				}
			}
			for _, b := range original.Bindings() {
				if !strings.Contains(string(p.Native), b.OVSUUID) {
					t.Fatal("old UUID absence unguarded")
				}
			}
		})
	}
}
