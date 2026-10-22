//go:build linux

package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

// SpanningTreePreflight is private, short-lived read evidence. It is deliberately
// not an execution.Plan, reservation, seal, Applied proof or public response.
// The runtime protocol identifies a name, not an immutable installed UUID.
type SpanningTreePreflight struct {
	Binding    candidate.Binding
	Sample     SpanningTreeRuntimeSample
	Transition spanningtree.Transition
	Witness    *SpanningTreeWitnessPair
}

var treeFenceTables = []string{"Open_vSwitch", "Bridge", "Port", "Interface"}
var treeFenceChanged = errors.New("SPANNING_TREE_NATIVE_FENCE_CHANGED")

// ReadSpanningTreePreflight composes the independent execution grant, current
// inventory scope, reviewed original, native database fences and qualified
// runtime source. No caller may use this result to execute a prepare intent.
// A future write path still needs an authenticated sealed execution scope,
// reservations, installed UUID/exclusive-protocol proof and Safe Apply.
func (e *Executor) ReadSpanningTreePreflight(ctx context.Context, draft candidate.StoredIntent, control SpanningTreeControl, executable string, authorize func(context.Context) error) (SpanningTreePreflight, error) {
	return e.readSpanningTreePreflight(ctx, draft, control, executable, nil, authorize)
}

// ReadSpanningTreeWitnessPreflight is the opt-in private P2a composition. No
// production caller or write path is registered. A build pin alone does not
// replace the independent root policies, current credential or DB fences.
func (e *Executor) ReadSpanningTreeWitnessPreflight(ctx context.Context, draft candidate.StoredIntent, control SpanningTreeControl, build SpanningTreeWitnessBuild, authorize func(context.Context) error) (SpanningTreePreflight, error) {
	return e.readSpanningTreePreflight(ctx, draft, control, build.Executable, &build, authorize)
}

func (e *Executor) readSpanningTreePreflight(ctx context.Context, draft candidate.StoredIntent, control SpanningTreeControl, executable string, build *SpanningTreeWitnessBuild, authorize func(context.Context) error) (SpanningTreePreflight, error) {
	empty := SpanningTreePreflight{}
	if authorize == nil {
		return empty, errors.New("SPANNING_TREE_EXECUTION_AUTHORIZATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := authorize(ctx); err != nil {
		return empty, err
	}
	if draft.Operation != candidate.SpanningTreePrepare || draft.SpanningTree == nil || e == nil || e.inventory == nil || e.provider == nil {
		return empty, errors.New("INVALID_SPANNING_TREE_PREFLIGHT")
	}
	for _, image := range []candidate.SpanningTreeConfig{draft.SpanningTree.Before, draft.SpanningTree.After} {
		if len(image.Parameters) != len(candidate.SpanningTreeBasicKeys) {
			return empty, errors.New("INVALID_SPANNING_TREE_PREFLIGHT")
		}
		for _, key := range candidate.SpanningTreeBasicKeys {
			if value, exists := image.Parameters[key]; !exists || value != nil && len(*value) > 128 {
				return empty, errors.New("INVALID_SPANNING_TREE_PREFLIGHT")
			}
		}
	}
	view, err := e.inventory.SpanningTreeExecutionView(ctx, draft.Object)
	if err != nil {
		return empty, err
	}
	tree := view.Candidate.SpanningTrees[draft.Object.ManagementID]
	if draft.Schema != view.Candidate.Schema || draft.Dependency != tree.Dependency || candidate.Digest(draft.SpanningTree.Before) != candidate.Digest(tree.Configuration) {
		return empty, errors.New("SPANNING_TREE_REVIEWED_ORIGINAL_CHANGED")
	}
	configuration := func(c candidate.SpanningTreeConfig) spanningtree.Configuration {
		other := map[string]any{}
		for k, v := range c.Parameters {
			if v != nil {
				other[k] = *v
			}
		}
		return spanningtree.Configuration{STP: &c.STP, RSTP: &c.RSTP, Other: other}
	}
	before, after := configuration(draft.SpanningTree.Before), configuration(draft.SpanningTree.After)
	protocol := "stp"
	if !*before.STP && *before.RSTP {
		protocol = "rstp"
	}
	if *before.STP == *before.RSTP || spanningtree.Validate(before.STP, before.RSTP, before.Other).State != "valid" || spanningtree.Validate(after.STP, after.RSTP, after.Other).State != "valid" {
		return empty, errors.New("SPANNING_TREE_PREFLIGHT_CONFIGURATION_UNPROVEN")
	}
	bridge, ok := view.Observation.Rows["Bridge"][draft.Object.OVSUUID].Configuration["name"].(string)
	if !ok || !spanningtree.RuntimeName(bridge) {
		return empty, errors.New("SPANNING_TREE_PREFLIGHT_NAME_UNPROVEN")
	}
	// Every source callback, including the final post-read callback, checks the
	// exact private scope again. Root grant revoke/regrant changes its revision.
	check := func(ctx context.Context) error {
		if err := authorize(ctx); err != nil {
			return err
		}
		current, err := e.inventory.SpanningTreeExecutionView(ctx, draft.Object)
		if err != nil {
			return err
		}
		if treeScopeFingerprint(current, draft.Object) != treeScopeFingerprint(view, draft.Object) {
			return errors.New("SPANNING_TREE_PREFLIGHT_SCOPE_CHANGED")
		}
		return ctx.Err()
	}
	if err = check(ctx); err != nil {
		return empty, err
	}
	// Set cancellation/deadline BEFORE get_schema. The held connection is used
	// for both atomic fences; no reconnect, retry or polling can refresh evidence.
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", e.provider.options.Socket)
	if err != nil {
		return empty, treeFenceChanged
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return empty, treeFenceChanged
	}
	peerID, pid, err := peer(conn, e.provider.options.PeerUID)
	if err != nil || peerID != view.Observation.Evidence.Peer {
		return empty, treeFenceChanged
	}
	r := bufio.NewReader(conn)
	raw, err := call(conn, r, 1, "get_schema", []any{"Open_vSwitch"})
	if err != nil {
		return empty, treeFenceChanged
	}
	d, err := discover(raw)
	if err != nil || d.public.Digest != draft.Schema || !sameExecutionFile(e.provider.options, pid, view.Observation.Evidence) {
		return empty, treeFenceChanged
	}
	ops, err := treeFenceOperations(view, d)
	if err != nil {
		return empty, err
	}
	var versions map[string]string
	var sample SpanningTreeRuntimeSample
	var pair *SpanningTreeWitnessPair
	if build != nil {
		if candidate.Digest(d.native) != ovswitness.BuildSchemaDigest() {
			return empty, witnessUnproven
		}
		// Both native input images are read in the same transactions as the
		// original whole-table guards and row versions, around both witnesses.
		ops, err = treeWitnessOperations(ops, d)
		if err != nil {
			return empty, err
		}
		if err = check(ctx); err != nil {
			return empty, err
		}
		first, err := readTreeWitnessFence(conn, r, ops, view, draft.Object)
		if err != nil {
			return empty, err
		}
		a, err := ReadBoundSpanningTreeWitness(ctx, control, e.provider.options, view.Observation.Evidence, *build, draft.Object.OVSUUID, check)
		if err != nil {
			return empty, err
		}
		b, err := ReadBoundSpanningTreeWitness(ctx, control, e.provider.options, view.Observation.Evidence, *build, draft.Object.OVSUUID, check)
		if err != nil {
			return empty, err
		}
		if err = check(ctx); err != nil {
			return empty, err
		}
		last, err := readTreeWitnessFence(conn, r, ops, view, draft.Object)
		if err != nil || candidate.Digest(first) != candidate.Digest(last) || !sameWitnessPair(a, b) || ovswitness.Exclusive(a.Witness) != nil || a.Witness.Bridge.Name != bridge || a.Witness.Configuration.InputDigest != first.InputDigest || a.Witness.Configuration.NextCfg != first.NextCfg || a.Witness.Database.RootUUID != view.Observation.Evidence.Root || a.Witness.Configuration.Source.STP != draft.SpanningTree.Before.STP || a.Witness.Configuration.Source.RSTP != draft.SpanningTree.Before.RSTP || candidate.Digest(a.Witness.Configuration.Source.Parameters) != candidate.Digest(draft.SpanningTree.Before.Parameters) {
			return empty, witnessUnproven
		}
		local := a.Witness.Native.STP.Local
		if protocol == "rstp" {
			local = a.Witness.Native.RSTP.Local
		}
		if local == nil {
			return empty, witnessUnproven
		}
		sample = SpanningTreeRuntimeSample{Daemon: a.Daemon, ObservedAt: a.ObservedAt, Runtime: spanningtree.Runtime{Protocol: protocol, BridgeName: bridge, Local: spanningtree.LocalParameters{Priority: int(local.Priority), SystemID: local.SystemID, Timers: spanningtree.Timers{HelloTime: int(local.HelloMS / 1000), MaxAge: int(local.MaxAgeMS / 1000), ForwardDelay: int(local.ForwardMS / 1000)}}}}
		pair = &SpanningTreeWitnessPair{First: a, Second: b}
	} else {
		versions, err = readTreeFence(conn, r, ops)
		if err != nil {
			return empty, err
		}
		sample, err = ReadBoundSpanningTreeRuntime(ctx, control, e.provider.options, view.Observation.Evidence, executable, protocol, bridge, check)
		if err != nil {
			return empty, err
		}
		currentVersions, err := readTreeFence(conn, r, ops)
		if err != nil || candidate.Digest(versions) != candidate.Digest(currentVersions) {
			return empty, treeFenceChanged
		}
	}
	currentPeer, _, err := peer(conn, e.provider.options.PeerUID)
	if err != nil || currentPeer != peerID || !sameExecutionFile(e.provider.options, pid, view.Observation.Evidence) {
		return empty, treeFenceChanged
	}
	if err = check(ctx); err != nil {
		return empty, err
	}
	if time.Since(sample.ObservedAt) < 0 || time.Since(sample.ObservedAt) > 2*time.Second {
		return empty, errors.New("SPANNING_TREE_RUNTIME_SAMPLE_STALE")
	}
	assessment := spanningtree.AssessTransition(before, after, sample.Runtime)
	if assessment.State != "compatible" {
		return empty, errors.New("SPANNING_TREE_RUNTIME_TRANSITION_UNPROVEN")
	}
	return SpanningTreePreflight{Binding: draft.Object, Sample: sample, Transition: assessment, Witness: pair}, nil
}

func treeScopeFingerprint(v inventory.ExecutionView, b candidate.Binding) string {
	e := v.Observation.Evidence
	return candidate.Digest([]any{v.Candidate.Schema, v.Candidate.Generation, v.Candidate.Policy, v.Candidate.SpanningTrees[b.ManagementID], e.Endpoint, e.Database, e.Schema, e.Root, e.Peer, e.Boot, e.File.Device, e.File.Inode})
}

// Four whole-table zero-timeout guards cover complete persistent images and
// incoming topology references, including all Bridge mirror/controller sets.
// Matching _uuid sets prevents unnoticed additions/removals at either fence.
// Four same-transaction version selects detect changes-and-restores of rows
// present at the first fence. Runtime/status changes also reject conservatively.
func treeFenceOperations(v inventory.ExecutionView, d discovered) ([]map[string]any, error) {
	ops := []map[string]any{}
	count := 0
	for _, table := range treeFenceTables {
		rows, exists := v.Observation.Rows[table]
		if !exists {
			return nil, treeFenceChanged
		}
		columns := []string{"_uuid"}
		for name, c := range d.native.Tables[table].Columns {
			if graphConfigurationColumn(table, name, c) {
				columns = append(columns, name)
			}
		}
		if table == "Open_vSwitch" {
			if len(rows) != 1 {
				return nil, treeFenceChanged
			}
			columns = append(columns, "next_cfg", "cur_cfg")
		}
		slices.Sort(columns)
		ids := []string{}
		for id := range rows {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		expected := []any{}
		for _, id := range ids {
			row := rows[id]
			count++
			if count > inventory.MaxRows || row.UUID != id || !apitypes.UUID(id) {
				return nil, treeFenceChanged
			}
			values := map[string]any{"_uuid": uuidValue(id)}
			for _, name := range columns {
				if name == "_uuid" {
					continue
				}
				value, known := row.Configuration[name]
				if table == "Open_vSwitch" && (name == "next_cfg" || name == "cur_cfg") {
					next, e1 := number(row.Values["next_cfg"])
					current, e2 := number(row.Values["cur_cfg"])
					if id != v.Observation.Evidence.Root || e1 != nil || e2 != nil || next != current {
						return nil, errors.New("SPANNING_TREE_CONFIGURATION_NOT_SETTLED")
					}
					value, known = row.Values[name]
				}
				if !known {
					return nil, treeFenceChanged
				}
				native, err := nativeValue(value, d.native.Tables[table].Columns[name])
				if err != nil {
					return nil, treeFenceChanged
				}
				values[name] = native
			}
			expected = append(expected, values)
		}
		ops = append(ops, waitRows(table, []any{}, columns, expected))
	}
	for _, table := range treeFenceTables {
		ops = append(ops, map[string]any{"op": "select", "table": table, "where": []any{}, "columns": []string{"_uuid", "_version"}})
	}
	data, err := json.Marshal(ops)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return nil, errors.New("SPANNING_TREE_FENCE_BUDGET")
	}
	return ops, nil
}

func readTreeFence(conn net.Conn, r *bufio.Reader, ops []map[string]any) (map[string]string, error) {
	params := []any{"Open_vSwitch"}
	for _, op := range ops {
		params = append(params, op)
	}
	if err := send(conn, map[string]any{"method": "transact", "params": params, "id": 4}); err != nil {
		return nil, treeFenceChanged
	}
	m, err := readMessage(r)
	if err != nil || m.Method != "" || string(m.ID) != "4" || len(m.Error) != 0 && string(m.Error) != "null" {
		return nil, treeFenceChanged
	}
	versions, err := treeFenceVersions(m.Result)
	if err != nil {
		return nil, err
	}
	count := 0
	for i, table := range treeFenceTables {
		for _, value := range ops[i]["rows"].([]any) {
			id := value.(map[string]any)["_uuid"].([]any)[1].(string)
			if versions[table+":"+id] == "" {
				return nil, treeFenceChanged
			}
			count++
		}
	}
	if len(versions) != count {
		return nil, treeFenceChanged
	}
	return versions, nil
}

func treeFenceVersions(data []byte) (map[string]string, error) {
	var result []map[string]json.RawMessage
	if len(data) > 512<<10 || json.Unmarshal(data, &result) != nil || len(result) != 8 {
		return nil, treeFenceChanged
	}
	versions := map[string]string{}
	for i, item := range result {
		if item == nil || i < 4 && len(item) != 0 {
			return nil, treeFenceChanged
		}
		if i < 4 {
			continue
		}
		var rows []map[string][]string
		if len(item) != 1 || item["rows"] == nil || string(item["rows"]) == "null" || json.Unmarshal(item["rows"], &rows) != nil || i == 4 && len(rows) != 1 {
			return nil, treeFenceChanged
		}
		for _, row := range rows {
			id, version := row["_uuid"], row["_version"]
			if len(row) != 2 || len(id) != 2 || len(version) != 2 || id[0] != "uuid" || version[0] != "uuid" || !apitypes.UUID(id[1]) || !apitypes.UUID(version[1]) {
				return nil, treeFenceChanged
			}
			key := treeFenceTables[i-4] + ":" + id[1]
			if versions[key] != "" || len(versions) >= inventory.MaxRows {
				return nil, treeFenceChanged
			}
			versions[key] = version[1]
		}
	}
	return versions, nil
}
