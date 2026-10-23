//go:build linux

package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestSpanningTreeNativeFenceUsesCompleteReadOnlyImages(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, view := treeFenceFixture(t, version)
			ops, err := treeFenceOperations(view, d)
			if err != nil || len(ops) != 8 {
				t.Fatal(ops, err)
			}
			for i, table := range treeFenceTables {
				g := ops[i]
				if g["op"] != "wait" || g["table"] != table || g["until"] != "==" || g["timeout"] != 0 || !reflect.DeepEqual(g["where"], []any{}) {
					t.Fatal("unguarded native read", g)
				}
				columns := g["columns"].([]string)
				for name, c := range d.native.Tables[table].Columns {
					if graphConfigurationColumn(table, name, c) && !strings.Contains(strings.Join(columns, ","), name) {
						t.Fatal("persistent field missing", table, name)
					}
				}
				if ops[i+4]["op"] != "select" || !reflect.DeepEqual(ops[i+4]["columns"], []string{"_uuid", "_version"}) {
					t.Fatal("not a fixed read-only version select", ops[i+4])
				}
			}
			for _, scenario := range []string{"unknown-configuration", "unsettled", "unknown-counter", "unknown-table", "wrong-root", "budget"} {
				t.Run(scenario, func(t *testing.T) {
					_, v := treeFenceFixture(t, version)
					root := v.Observation.Rows["Open_vSwitch"][v.Observation.Evidence.Root]
					switch scenario {
					case "unknown-configuration":
						delete(root.Configuration, "external_ids")
					case "unsettled":
						root.Values["next_cfg"] = "11"
					case "unknown-counter":
						root.Values["cur_cfg"] = "not-a-counter"
					case "unknown-table":
						delete(v.Observation.Rows, "Interface")
					case "wrong-root":
						v.Observation.Evidence.Root = repository.NewID()
					case "budget":
						root.Configuration["external_ids"] = map[string]any{"large": strings.Repeat("x", 256<<10)}
					}
					if _, err := treeFenceOperations(v, d); err == nil {
						t.Fatal("unproven fence accepted", scenario)
					}
				})
			}
		})
	}
}

func TestSpanningTreeNativeFenceRejectsMalformedRepliesAndRowSets(t *testing.T) {
	d, view := treeFenceFixture(t, "3.3.9")
	ops, err := treeFenceOperations(view, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "guard-error", "null-wait", "extra-result", "null-rows", "missing-version", "invalid-version", "duplicate-row", "missing-row", "foreign-row", "wrong-id"} {
		t.Run(scenario, func(t *testing.T) {
			result := []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}}
			for _, table := range treeFenceTables {
				rows := []any{}
				for id := range view.Observation.Rows[table] {
					rows = append(rows, map[string]any{"_uuid": uuidValue(id), "_version": uuidValue(repository.NewID())})
				}
				result = append(result, map[string]any{"rows": rows})
			}
			id := 4
			selectResult := result[5].(map[string]any)
			rows := selectResult["rows"].([]any)
			row := rows[0].(map[string]any)
			switch scenario {
			case "guard-error":
				result[0] = map[string]any{"error": "timed out", "details": "private-native-error"}
			case "null-wait":
				result[1] = nil
			case "extra-result":
				result = append(result, map[string]any{})
			case "null-rows":
				selectResult["rows"] = nil
			case "missing-version":
				delete(row, "_version")
			case "invalid-version":
				row["_version"] = []any{"uuid", "invalid"}
			case "duplicate-row":
				selectResult["rows"] = append(rows, row)
			case "missing-row":
				selectResult["rows"] = []any{}
			case "foreign-row":
				row["_uuid"] = uuidValue(repository.NewID())
			case "wrong-id":
				id++
			}
			client, server := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				_ = server.SetDeadline(time.Now().Add(time.Second))
				var q map[string]any
				if json.NewDecoder(server).Decode(&q) == nil {
					_ = json.NewEncoder(server).Encode(map[string]any{"id": id, "error": nil, "result": result})
				}
			}()
			versions, err := readTreeFence(client, bufio.NewReader(client), ops)
			<-done
			if scenario == "valid" {
				if err != nil || len(versions) != 4 {
					t.Fatal(versions, err)
				}
			} else if !errors.Is(err, treeFenceChanged) || versions != nil || strings.Contains(err.Error(), "private-native-error") {
				t.Fatal("partial or detailed unproven evidence escaped", versions, err)
			}
		})
	}
}

func TestSpanningTreePreflightDeniesBeforeAnyProviderAccess(t *testing.T) {
	var e *Executor
	denied := errors.New("revoked")
	for _, authorize := range []func(context.Context) error{nil, func(context.Context) error { return denied }, func(context.Context) error { return nil }} {
		out, err := e.ReadSpanningTreePreflight(context.Background(), candidate.StoredIntent{}, SpanningTreeControl{}, "", authorize)
		if err == nil || !reflect.DeepEqual(out, SpanningTreePreflight{}) {
			t.Fatal("invalid or unauthorized preflight reached a provider", out, err)
		}
	}
}

func TestSpanningTreePreflightScopeRetainsIdentityAndExcludesSamplingTime(t *testing.T) {
	_, view := treeFenceFixture(t, "3.3.9")
	b := candidate.Binding{ManagementID: repository.NewID()}
	before := treeScopeFingerprint(view, b)
	view.Observation.Evidence.ObservedAt = time.Now()
	view.Observation.Evidence.File.Size++
	if treeScopeFingerprint(view, b) != before {
		t.Fatal("ordinary read freshness/file growth changed the immutable scope")
	}
	for _, scenario := range []string{"root", "peer", "boot", "file", "policy", "schema", "generation"} {
		v := view
		switch scenario {
		case "root":
			v.Observation.Evidence.Root = repository.NewID()
		case "peer":
			v.Observation.Evidence.Peer = "other-peer"
		case "boot":
			v.Observation.Evidence.Boot = repository.NewID()
		case "file":
			v.Observation.Evidence.File.Inode++
		case "policy":
			v.Candidate.Policy = "other-policy"
		case "schema":
			v.Candidate.Schema = "other-schema"
		case "generation":
			v.Candidate.Generation = repository.NewID()
		}
		if treeScopeFingerprint(v, b) == before {
			t.Fatal("identity fence omitted", scenario)
		}
	}
}
