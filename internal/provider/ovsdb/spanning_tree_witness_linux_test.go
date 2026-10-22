//go:build linux

package ovsdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
)

func witnessExample(t *testing.T) ovswitness.Sample {
	t.Helper()
	data, err := os.ReadFile("../../ovswitness/testdata/known.json")
	if err != nil {
		t.Fatal(err)
	}
	var s ovswitness.Sample
	if json.Unmarshal(data, &s) != nil {
		t.Fatal("fixture")
	}
	return s
}

func TestSpanningTreeWitnessWireUsesFreshNonceAndClosedMethod(t *testing.T) {
	s := witnessExample(t)
	seen := map[string]bool{}
	for _, fault := range []string{"", "", "replay", "wrong-uuid", "whitespace", "truncated", "duplicate"} {
		a, b := net.Pipe()
		_ = a.SetDeadline(time.Now().Add(time.Second))
		done := make(chan map[string]json.RawMessage, 1)
		go func() {
			defer b.Close()
			var q map[string]json.RawMessage
			if json.NewDecoder(b).Decode(&q) != nil {
				done <- nil
				return
			}
			var args []string
			_ = json.Unmarshal(q["params"], &args)
			if len(args) != 2 {
				done <- q
				return
			}
			copy := s
			copy.Nonce = args[1]
			if fault == "replay" {
				copy.Nonce = strings.Repeat("0", 64)
			}
			if fault == "wrong-uuid" {
				copy.Bridge.UUID = copy.Database.RootUUID
			}
			inner, _ := json.Marshal(copy)
			wire, _ := json.Marshal(map[string]any{"id": 1, "result": string(inner), "error": nil})
			switch fault {
			case "whitespace":
				wire = append([]byte(strings.Repeat(" ", ovswitness.MaxWireBytes)), wire...)
			case "truncated":
				wire = wire[:len(wire)/2]
			case "duplicate":
				wire = []byte(`{"id":1,"id":1,"error":null,"result":""}`)
			}
			done <- q
			_, _ = b.Write(wire)
		}()
		out, at, err := requestSpanningTreeWitness(a, s.Bridge.UUID)
		_ = a.Close()
		q := <-done
		var method string
		var args []string
		_ = json.Unmarshal(q["method"], &method)
		_ = json.Unmarshal(q["params"], &args)
		if len(q) != 3 || string(q["id"]) != "1" || method != "ovs-webui/stp-witness-v1" || len(args) != 2 || args[0] != s.Bridge.UUID || !witnessHex(args[1]) || seen[args[1]] {
			t.Fatal("generic or replayable request", q)
		}
		seen[args[1]] = true
		if fault == "" {
			if err != nil || at.IsZero() || out.Nonce != args[1] {
				t.Fatal(out, at, err)
			}
		} else if err == nil || !reflect.DeepEqual(out, ovswitness.Sample{}) || !at.IsZero() {
			t.Fatal("partial failure", fault, out, at, err)
		}
	}
}

func TestSpanningTreeWitnessPairRejectsEveryIdentityAndEpochChange(t *testing.T) {
	s := witnessExample(t)
	a := SpanningTreeWitnessSample{Daemon: "boot:pid:start", ObservedAt: time.Now(), Witness: s, buildIdentity: "exact-fds"}
	b := a
	b.Witness.Nonce = strings.Repeat("f", 64)
	if !sameWitnessPair(a, b) {
		t.Fatal("identical fresh pair denied")
	}
	changes := map[string]func(*SpanningTreeWitnessSample){
		"daemon":        func(s *SpanningTreeWitnessSample) { s.Daemon = "other" },
		"build":         func(s *SpanningTreeWitnessSample) { s.buildIdentity = "other" },
		"replay":        func(s *SpanningTreeWitnessSample) { s.Witness.Nonce = a.Witness.Nonce },
		"time":          func(s *SpanningTreeWitnessSample) { s.ObservedAt = a.ObservedAt.Add(-time.Second) },
		"process":       func(s *SpanningTreeWitnessSample) { s.Witness.Daemon = "other" },
		"root":          func(s *SpanningTreeWitnessSample) { s.Witness.Database.RootUUID = "other" },
		"authority":     func(s *SpanningTreeWitnessSample) { s.Witness.Database.Authority = "2" },
		"seqno":         func(s *SpanningTreeWitnessSample) { s.Witness.Database.IDLSeqno = "999" },
		"uuid":          func(s *SpanningTreeWitnessSample) { s.Witness.Bridge.UUID = "other" },
		"runtime":       func(s *SpanningTreeWitnessSample) { s.Witness.Bridge.Instance = "other" },
		"binding":       func(s *SpanningTreeWitnessSample) { s.Witness.Bridge.Epoch = "999" },
		"configuration": func(s *SpanningTreeWitnessSample) { s.Witness.Configuration.Epoch = "999" },
		"input":         func(s *SpanningTreeWitnessSample) { s.Witness.Configuration.InputDigest = strings.Repeat("0", 64) },
		"source": func(s *SpanningTreeWitnessSample) {
			s.Witness.Configuration.Source.STP = !s.Witness.Configuration.Source.STP
		},
		"engine": func(s *SpanningTreeWitnessSample) { s.Witness.Native.RSTP.Enabled = true },
	}
	for name, change := range changes {
		copy := b
		change(&copy)
		if sameWitnessPair(a, copy) {
			t.Fatal("changed pair trusted", name)
		}
	}
}

func TestSpanningTreeWitnessFenceKeepsAtomicGuardsAndClosedRegistry(t *testing.T) {
	d, v := treeFenceFixture(t, "3.3.9")
	if candidate.Digest(d.native) != ovswitness.BuildSchemaDigest() {
		t.Fatal("schema pin mismatch", d.public.Digest)
	}
	ops, err := treeFenceOperations(v, d)
	if err != nil {
		t.Fatal(err)
	}
	w, err := treeWitnessOperations(ops, d)
	if err != nil || len(w) != 12 || !reflect.DeepEqual(w[:8], ops) {
		t.Fatal("old guards replaced", err)
	}
	for _, op := range w[8:] {
		if op["op"] != "select" || len(op["where"].([]any)) != 0 {
			t.Fatal("write or query expansion", op)
		}
	}
	delete(d.native.Tables["Interface"].Columns, "mtu_request")
	if _, err := treeWitnessOperations(ops, d); err == nil {
		t.Fatal("partial schema accepted")
	}
	// Construct one real canonical graph, including native integer atoms and
	// actual ofport, with same-transaction versions around the entire image.
	data, err := os.ReadFile("../../ovswitness/testdata/input.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows map[string][]map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if dec.Decode(&rows) != nil {
		t.Fatal("input fixture")
	}
	v.Observation.Rows = inventory.Rows{}
	result := []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}}
	var binding candidate.Binding
	for _, table := range treeFenceTables {
		v.Observation.Rows[table] = map[string]inventory.Row{}
		versions := []any{}
		for _, row := range rows[table] {
			id := row["_uuid"].([]any)[1].(string)
			values := map[string]any{}
			for _, field := range []string{"ports", "interfaces"} {
				if raw, ok := row[field]; ok {
					normalized, e := normalize(raw, schema(t, "3.3.9").native.Tables[table].Columns[field])
					if e != nil {
						t.Fatal(e)
					}
					values[field] = normalized
				}
			}
			if table == "Open_vSwitch" {
				v.Observation.Evidence.Root = id
				values["next_cfg"] = "5" // separate whole-table wait, not a digest column
			}
			if table == "Bridge" {
				binding.OVSUUID = id
			}
			v.Observation.Rows[table][id] = inventory.Row{UUID: id, Values: values}
			versions = append(versions, map[string]any{"_uuid": row["_uuid"], "_version": []any{"uuid", "99999999-9999-4999-8999-999999999999"}})
		}
		result = append(result, map[string]any{"rows": versions})
	}
	for _, table := range treeFenceTables {
		result = append(result, map[string]any{"rows": rows[table]})
	}
	encode := func() []byte { b, _ := json.Marshal(result); return b }
	out, err := decodeTreeWitnessFence(encode(), v, binding)
	want, _ := ovswitness.Digest(rows)
	if err != nil || out.InputDigest != want {
		t.Fatal(out, err)
	}
	for _, fault := range []string{"version", "unknown", "ofport", "missing", "extra-result"} {
		backup := encode()
		switch fault {
		case "version":
			result[4] = map[string]any{"rows": []any{}}
		case "unknown":
			result[8] = map[string]any{"rows": rows["Open_vSwitch"], "extra": true}
		case "ofport":
			result[11] = map[string]any{"rows": rows["Interface"]}
			delete(rows["Interface"][0], "ofport")
		case "missing":
			result[11] = map[string]any{"rows": []any{}}
		case "extra-result":
			result = append(result, map[string]any{})
		}
		got, e := decodeTreeWitnessFence(encode(), v, binding)
		if e == nil || !reflect.DeepEqual(got, treeWitnessFence{}) {
			t.Fatal("partial fence", fault, got, e)
		}
		dec := json.NewDecoder(strings.NewReader(string(backup)))
		dec.UseNumber()
		_ = dec.Decode(&result)
		// Restore the separately referenced canonical fixture after mutation.
		dec = json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		_ = dec.Decode(&rows)
	}
}

func TestSpanningTreeWitnessAdmissionAndFileBoundsFailClosed(t *testing.T) {
	for _, authorize := range []func(context.Context) error{nil, func(context.Context) error { return io.EOF }} {
		out, err := ReadBoundSpanningTreeWitness(context.Background(), SpanningTreeControl{}, Options{}, inventory.Evidence{}, SpanningTreeWitnessBuild{}, "", authorize)
		if err == nil || !reflect.DeepEqual(out, SpanningTreeWitnessSample{}) {
			t.Fatal(out, err)
		}
		var e *Executor
		p, err := e.ReadSpanningTreeWitnessPreflight(context.Background(), candidate.StoredIntent{}, SpanningTreeControl{}, SpanningTreeWitnessBuild{}, authorize)
		if err == nil || !reflect.DeepEqual(p, SpanningTreePreflight{}) {
			t.Fatal(p, err)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if os.WriteFile(file, []byte("executable"), 0700) != nil {
		t.Fatal("fixture")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		ctx context.Context
		max int64
	}{{ctx, 4096}, {context.Background(), 2}} {
		if hash, _, err := witnessFileHash(c.ctx, file, c.max, true); err == nil || hash != "" {
			t.Fatal("unbounded or cancelled hash")
		}
	}
	if os.Geteuid() == 0 {
		hash, _, err := witnessFileHash(context.Background(), file, 4096, true)
		expected := sha256.Sum256([]byte("executable"))
		if err != nil || hash != hex.EncodeToString(expected[:]) {
			t.Fatal("root FD hash", err)
		}
	}
	_ = os.Chmod(file, 0777)
	if hash, _, err := witnessFileHash(context.Background(), file, 4096, true); err == nil || hash != "" {
		t.Fatal("writable executable trusted")
	}
}
