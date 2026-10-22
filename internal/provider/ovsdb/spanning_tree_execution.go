package ovsdb

import (
	"encoding/json"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Root-owned process configuration only. There is no default discovery and no
// client-controlled control socket, executable, profile, transaction or command.
type SpanningTreeExecutionOptions struct {
	ControlSocket, Executable, Manifest, ManifestDigest string
	DatabaseExecutable, DatabaseSHA256                  string
	PeerUID                                             uint32
}

func (e *Executor) ConfigureSpanningTreeExecution(o SpanningTreeExecutionOptions) error {
	if e == nil || e.spanningTree != nil || !filepath.IsAbs(o.ControlSocket) || len(o.ControlSocket) > 100 || !filepath.IsAbs(o.Executable) || !filepath.IsAbs(o.Manifest) || !filepath.IsAbs(o.DatabaseExecutable) || strings.ContainsAny(o.ControlSocket+o.Executable+o.Manifest+o.DatabaseExecutable, "\x00\r\n") || len(o.DatabaseSHA256) != 64 || strings.Trim(o.DatabaseSHA256, "0123456789abcdef") != "" || len(o.ManifestDigest) != 64 || strings.Trim(o.ManifestDigest, "0123456789abcdef") != "" || o.PeerUID == ^uint32(0) {
		return apitypes.Fail(422, "INVALID_SPANNING_TREE_WITNESS_CONFIGURATION")
	}
	e.spanningTree = &o
	return nil
}

func spanningTreeIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) != 1 || c.Intents[0].Operation != candidate.SpanningTreeConfigure || c.Intents[0].SpanningTree == nil {
		return candidate.StoredIntent{}, false
	}
	return c.Intents[0], true
}

func compileSpanningTreeExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	i, ok := spanningTreeIntent(envelope.Candidate)
	if !ok {
		return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
	}
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return out, apitypes.Fail(409, "SPANNING_TREE_EXECUTION_CONFLICT")
	}
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	if e1 != nil || e2 != nil || next == math.MaxInt64 || next != cur {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != native.TypeInteger || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
		}
	}
	bridge := view.Observation.Rows["Bridge"][i.Object.OVSUUID]
	labels, ok := bridge.Values["external_ids"].(map[string]any)
	if !ok || len(labels) >= 128 && labels[candidate.SpanningTreeMarker] == nil {
		return out, apitypes.Fail(429, "COMMIT_EVIDENCE_CAPACITY")
	}
	// Full persistent four-table waits include references, unrelated map keys,
	// both flags, raw originals, marker and settled counters in this ONE write.
	ops, err := treeFenceOperations(view, d)
	if err != nil {
		return out, err
	}
	n := nativePlan{Operations: ops[:4], Evidence: view.Observation.Evidence, TreePolicy: view.Candidate.Policy}
	where := []any{uuidCondition(i.Object.OVSUUID)}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "update", "table": "Bridge", "where": where, "row": map[string]any{"stp_enable": i.SpanningTree.After.STP, "rstp_enable": i.SpanningTree.After.RSTP}})
	remove, insert := []any{}, []any{}
	for _, key := range candidate.SpanningTreeBasicKeys {
		remove = append(remove, key)
		if value := i.SpanningTree.After.Parameters[key]; value != nil {
			insert = append(insert, []any{key, *value})
		}
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Bridge", "where": where, "mutations": []any{
		[]any{"other_config", "delete", []any{"set", remove}}, []any{"other_config", "insert", []any{"map", insert}},
		[]any{"external_ids", "delete", []any{"set", []any{candidate.SpanningTreeMarker}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{candidate.SpanningTreeMarker, marker}}}},
	}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	bindings := append([]candidate.Binding{}, view.Candidate.SpanningTrees[i.Object.ManagementID].Bindings...)
	slices.SortFunc(bindings, func(a, b candidate.Binding) int { return strings.Compare(a.ManagementID, b.ManagementID) })
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data, GraphBindings: bindings}, nil
}
