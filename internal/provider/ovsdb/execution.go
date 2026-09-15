package ovsdb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"slices"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

type Executor struct {
	provider  *Provider
	inventory *inventory.Service
}

func (p *Provider) Executor(s *inventory.Service) *Executor { return &Executor{p, s} }

type nativePlan struct {
	Operations   []map[string]any   `json:"operations"`
	CountIndexes []int              `json:"count_indexes"`
	TargetIndex  int                `json:"target_index"`
	Evidence     inventory.Evidence `json:"evidence"`
}

func uuidValue(id string) []any     { return []any{"uuid", id} }
func uuidCondition(id string) []any { return []any{"_uuid", "==", uuidValue(id)} }
func uuidSet(ids ...string) []any {
	values := []any{}
	for _, id := range ids {
		values = append(values, uuidValue(id))
	}
	return []any{"set", values}
}
func waitRows(table string, where []any, columns []string, rows []any) map[string]any {
	return map[string]any{"op": "wait", "table": table, "where": where, "columns": columns, "rows": rows, "until": "==", "timeout": 0}
}
func nativeAtom(value any, kind string) (any, error) {
	switch kind {
	case native.TypeInteger:
		s, ok := value.(string)
		if !ok {
			return nil, errors.New("NATIVE_INTEGER_UNKNOWN")
		}
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			return nil, err
		}
		return json.Number(s), nil
	case native.TypeUUID:
		s, ok := value.(string)
		if !ok || !apitypes.UUID(s) {
			return nil, errors.New("NATIVE_UUID_UNKNOWN")
		}
		return uuidValue(s), nil
	case native.TypeString:
		if _, ok := value.(string); !ok {
			return nil, errors.New("NATIVE_STRING_UNKNOWN")
		}
		return value, nil
	case native.TypeBoolean:
		if _, ok := value.(bool); !ok {
			return nil, errors.New("NATIVE_BOOLEAN_UNKNOWN")
		}
		return value, nil
	}
	return nil, errors.New("NATIVE_GUARD_UNSUPPORTED")
}
func nativeValue(value any, column *native.ColumnSchema) (any, error) {
	if column == nil || column.TypeObj == nil || column.TypeObj.Key == nil {
		return nil, errors.New("NATIVE_GUARD_UNSUPPORTED")
	}
	if column.Type == native.TypeMap {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("NATIVE_MAP_UNKNOWN")
		}
		keys := []string{}
		for key := range m {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		pairs := []any{}
		for _, key := range keys {
			v, err := nativeAtom(m[key], column.TypeObj.Value.Type)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, []any{key, v})
		}
		return []any{"map", pairs}, nil
	}
	if column.Type == native.TypeSet {
		values, ok := value.([]any)
		if !ok {
			values = []any{value}
		}
		out := []any{}
		for _, value := range values {
			v, err := nativeAtom(value, column.TypeObj.Key.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return []any{"set", out}, nil
	}
	return nativeAtom(value, column.TypeObj.Key.Type)
}
func guard(d discovered, table string, row inventory.Row, names []string, where []any) (map[string]any, error) {
	columns := append([]string{"_uuid"}, names...)
	values := map[string]any{"_uuid": uuidValue(row.UUID)}
	for _, name := range names {
		value, exists := row.Values[name]
		if !exists {
			return nil, errors.New("NATIVE_DEPENDENCY_UNKNOWN")
		}
		v, err := nativeValue(value, d.native.Tables[table].Columns[name])
		if err != nil {
			return nil, err
		}
		values[name] = v
	}
	return waitRows(table, where, columns, []any{values}), nil
}
func vlanRow(v candidate.VLAN) map[string]any {
	mode, tag := []any{}, []any{}
	if v.Mode != nil {
		mode = append(mode, *v.Mode)
	}
	if v.Tag != nil {
		tag = append(tag, *v.Tag)
	}
	trunks, cvlans := []any{}, []any{}
	for _, n := range v.Trunks {
		trunks = append(trunks, n)
	}
	for _, n := range v.CVLANs {
		cvlans = append(cvlans, n)
	}
	return map[string]any{"vlan_mode": []any{"set", mode}, "tag": []any{"set", tag}, "trunks": []any{"set", trunks}, "cvlans": []any{"set", cvlans}}
}
func nativeRefs(value any) []string {
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	ids := []string{}
	for _, v := range values {
		if s, ok := v.(string); ok {
			ids = append(ids, s)
		}
	}
	return ids
}
func number(value any) (int64, error) {
	s, ok := value.(string)
	if !ok {
		return 0, errors.New("APPLIED_COUNTER_UNKNOWN")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("APPLIED_COUNTER_UNKNOWN")
	}
	return n, nil
}

func (e *Executor) connect(ctx context.Context) (net.Conn, *bufio.Reader, discovered, string, int, error) {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", e.provider.options.Socket)
	if err != nil {
		return nil, nil, discovered{}, "", 0, err
	}
	identity, pid, err := peer(conn, e.provider.options.PeerUID)
	if err != nil {
		conn.Close()
		return nil, nil, discovered{}, "", 0, err
	}
	r := bufio.NewReaderSize(conn, 32<<10)
	body, err := call(conn, r, 1, "get_schema", []any{"Open_vSwitch"})
	var d discovered
	if err == nil {
		d, err = discover(body)
	}
	if err != nil {
		conn.Close()
		return nil, nil, discovered{}, "", 0, err
	}
	return conn, r, d, identity, pid, nil
}

func (e *Executor) Prepare(ctx context.Context, id, marker string, envelope candidate.Envelope) (execution.Plan, error) {
	var out execution.Plan
	if !apitypes.ManagementID(id) || len(marker) != 64 || candidate.Budget(envelope) != nil {
		return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
	}
	view, err := e.inventory.ExecutionView(ctx, candidate.Bindings(envelope.Candidate))
	if err != nil {
		return out, err
	}
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return out, apitypes.Fail(409, "EXECUTION_PREFLIGHT_FAILED")
	}
	conn, _, d, identity, _, err := e.connect(ctx)
	if err != nil {
		return out, err
	}
	conn.Close()
	if d.public.Digest != view.Candidate.Schema || identity != view.Observation.Evidence.Peer {
		return out, apitypes.Fail(409, "PROVIDER_IDENTITY_CHANGED")
	}
	return compileExecution(id, marker, envelope, view, d)
}
func compileExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	next, err := number(root.Values["next_cfg"])
	if err != nil || next == math.MaxInt64 {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	cur, err := number(root.Values["cur_cfg"])
	if err != nil || cur > next {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != native.TypeInteger || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
		}
	}
	n := nativePlan{Operations: []map[string]any{}, CountIndexes: []int{}, Evidence: view.Observation.Evidence}
	rootGuard, err := guard(d, "Open_vSwitch", root, []string{"external_ids"}, []any{uuidCondition(root.UUID), []any{"next_cfg", "<", json.Number(strconv.FormatInt(math.MaxInt64, 10))}, []any{"next_cfg", ">=", next}, []any{"cur_cfg", ">=", cur}})
	if err != nil {
		return out, err
	}
	n.Operations = append(n.Operations, rootGuard)
	seen := map[string]bool{}
	for _, intent := range envelope.Candidate.Intents {
		if intent.Operation != "port.vlan.set" || seen[intent.Object.OVSUUID] {
			return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
		}
		seen[intent.Object.OVSUUID] = true
		port := view.Observation.Rows["Port"][intent.Object.OVSUUID]
		where := []any{uuidCondition(port.UUID)}
		g, err := guard(d, "Port", port, []string{"name", "interfaces", "vlan_mode", "tag", "trunks", "cvlans", "external_ids"}, where)
		if err != nil {
			return out, err
		}
		n.Operations = append(n.Operations, g)
		meta := d.native.Tables["Port"].Columns["external_ids"]
		if meta == nil || meta.Type != native.TypeMap || !meta.Mutable() || meta.Ephemeral() || meta.TypeObj.Key.Type != native.TypeString || meta.TypeObj.Value.Type != native.TypeString {
			return out, apitypes.Fail(409, "COMMIT_EVIDENCE_UNSUPPORTED")
		}
		labels, ok := port.Values["external_ids"].(map[string]any)
		if !ok || len(labels) >= 128 && labels[execution.MarkerKey] == nil {
			return out, apitypes.Fail(429, "COMMIT_EVIDENCE_CAPACITY")
		}
		for _, bridge := range view.Observation.Rows["Bridge"] {
			if !slices.Contains(nativeRefs(bridge.Values["ports"]), port.UUID) {
				continue
			}
			g, err = guard(d, "Bridge", bridge, []string{"name", "datapath_type", "external_ids"}, []any{[]any{"ports", "includes", uuidSet(port.UUID)}})
			if err != nil {
				return out, err
			}
			n.Operations = append(n.Operations, g)
			n.Operations = append(n.Operations, waitRows("Open_vSwitch", []any{uuidCondition(root.UUID), []any{"bridges", "includes", uuidSet(bridge.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(root.UUID)}}))
		}
		for _, id := range nativeRefs(port.Values["interfaces"]) {
			member := view.Observation.Rows["Interface"][id]
			g, err = guard(d, "Interface", member, []string{"name", "type", "options", "external_ids"}, []any{uuidCondition(id)})
			if err != nil {
				return out, err
			}
			n.Operations = append(n.Operations, g)
		}
		n.CountIndexes = append(n.CountIndexes, len(n.Operations))
		n.Operations = append(n.Operations, map[string]any{"op": "update", "table": "Port", "where": where, "row": vlanRow(intent.Value)})
		n.CountIndexes = append(n.CountIndexes, len(n.Operations))
		n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Port", "where": where, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{execution.MarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{execution.MarkerKey, marker}}}}}})
	}
	if len(seen) == 0 {
		return out, apitypes.Fail(422, "EMPTY_EXECUTION")
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	b, err := json.Marshal(n)
	if err != nil || len(b) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	out = execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: b}
	return out, nil
}

func (e *Executor) Commit(ctx context.Context, p execution.Plan, beforeSend func() error) execution.Outcome {
	notSent := execution.Outcome{Commit: "rejected", Applied: "not-applied", Reason: "preflight-expired"}
	if time.Since(p.Prepared) < 0 || time.Since(p.Prepared) > execution.PreflightFor || ctx.Err() != nil {
		return notSent
	}
	var n nativePlan
	d := json.NewDecoder(bytes.NewReader(p.Native))
	d.UseNumber()
	if d.Decode(&n) != nil || len(p.Native) > execution.MaxPlanBytes {
		notSent.Reason = "execution-plan-invalid"
		return notSent
	}
	view, err := e.inventory.ExecutionView(ctx, candidate.Bindings(p.Envelope.Candidate))
	if err != nil || view.Candidate.Generation != p.Generation || view.Candidate.Schema != p.Schema {
		notSent.Reason = "identity-or-provider-unavailable"
		return notSent
	}
	checks, _ := candidate.Checks(p.Envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		notSent.Reason = "execution-preflight-conflict"
		return notSent
	}
	conn, r, schema, identity, pid, err := e.connect(ctx)
	if err != nil {
		notSent.Reason = "provider-connect-failed"
		return notSent
	}
	defer conn.Close()
	if schema.public.Digest != p.Schema || identity != n.Evidence.Peer || !sameExecutionFile(e.provider.options, pid, n.Evidence) {
		notSent.Reason = "provider-identity-changed"
		return notSent
	}
	if time.Since(p.Prepared) > execution.PreflightFor || ctx.Err() != nil {
		return notSent
	}
	params := []any{"Open_vSwitch"}
	for _, op := range n.Operations {
		params = append(params, op)
	}
	// This is the sole mutation transport. No generic RPC method is added to the
	// discovery reader and no raw transact input is exposed through HTTP or IPC.
	data, err := json.Marshal(map[string]any{"method": "transact", "params": params, "id": 2})
	if err != nil {
		notSent.Reason = "execution-plan-invalid"
		return notSent
	}
	data = append(data, '\n')
	if beforeSend == nil || beforeSend() != nil {
		notSent.Reason = "dispatch-admission-denied"
		return notSent
	}
	if ctx.Err() != nil || time.Since(p.Prepared) > execution.PreflightFor {
		return notSent
	}
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	written, err := conn.Write(data)
	unknown := execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: "provider-reply-unknown"}
	if err != nil || written != len(data) {
		if written == 0 {
			notSent.Reason = "provider-not-sent"
			return notSent
		}
		return unknown
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for count := 0; count < 32; count++ {
		m, err := readMessage(r)
		if err != nil {
			return unknown
		}
		if m.Method == "echo" {
			if replyEcho(conn, m) != nil {
				return unknown
			}
			continue
		}
		if m.Method != "" || string(m.ID) != "2" || len(m.Error) > 0 && string(m.Error) != "null" {
			return unknown
		}
		return decodeCommit(n, p, m.Result)
	}
	return unknown
}
func decodeCommit(n nativePlan, p execution.Plan, body []byte) execution.Outcome {
	out := execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: "provider-result-invalid"}
	var results []map[string]json.RawMessage
	if json.Unmarshal(body, &results) != nil || (len(results) != len(n.Operations) && len(results) != len(n.Operations)+1) {
		return out
	}
	// A structured operation error means the entire native transaction aborted,
	// including writes preceding the failing wait. Never retry an uncertain send.
	for _, r := range results {
		if len(r["error"]) > 0 {
			var code string
			if json.Unmarshal(r["error"], &code) == nil && code != "" {
				return execution.Outcome{Commit: "rejected", Applied: "not-applied", Reason: "ovsdb-transaction-rejected"}
			}
		}
	}
	if len(results) != len(n.Operations) {
		return out
	}
	for _, index := range n.CountIndexes {
		var count int
		if index >= len(results) || json.Unmarshal(results[index]["count"], &count) != nil || count != 1 {
			return out
		}
	}
	var rows []struct {
		ID   []string    `json:"_uuid"`
		Next json.Number `json:"next_cfg"`
	}
	if n.TargetIndex >= len(results) || json.Unmarshal(results[n.TargetIndex]["rows"], &rows) != nil || len(rows) != 1 || len(rows[0].ID) != 2 || rows[0].ID[0] != "uuid" || rows[0].ID[1] != p.Root {
		return out
	}
	target, err := rows[0].Next.Int64()
	floor, _ := strconv.ParseInt(p.NextFloor, 10, 64)
	if err != nil || target <= floor {
		return out
	}
	value := strconv.FormatInt(target, 10)
	return execution.Outcome{Commit: "committed", Applied: "pending", Reason: "ovsdb-committed", Target: &value}
}

func (e *Executor) Observe(ctx context.Context, p execution.Plan, prior execution.Outcome) execution.Outcome {
	out := prior
	view, err := e.inventory.ExecutionView(ctx, candidate.Bindings(p.Envelope.Candidate))
	if err != nil {
		out.Reason = "provider-resync-required"
		return out
	}
	if view.Candidate.Generation != p.Generation || view.Candidate.Schema != p.Schema || view.Observation.Evidence.Root != p.Root {
		out.Applied = "unknown"
		out.Reason = "identity-changed-recovery-required"
		return out
	}
	all, anyMarker := true, false
	for _, intent := range p.Envelope.Candidate.Intents {
		row, exists := view.Observation.Rows["Port"][intent.Object.OVSUUID]
		labels, _ := row.Values["external_ids"].(map[string]any)
		match := exists && labels[execution.MarkerKey] == p.Marker
		all = all && match
		anyMarker = anyMarker || match
	}
	if anyMarker {
		out.Commit = "committed"
	}
	if !all || out.Commit != "committed" {
		out.Applied = "unknown"
		out.Reason = "commit-evidence-insufficient"
		return out
	}
	after := p.Envelope.Candidate
	after.Intents = append([]candidate.StoredIntent{}, after.Intents...)
	for i := range after.Intents {
		after.Intents[i].Before = after.Intents[i].Value
	}
	checks, _ := candidate.Checks(after, view.Candidate)
	if !candidate.Passed(checks) {
		out.Applied = "unknown"
		out.Reason = "committed-target-changed"
		return out
	}
	// Marker evidence can prove commit after a lost reply, but cannot recover the
	// exact increment result. Do not invent an Applied target from a later value.
	if out.Target == nil {
		out.Applied = "unknown"
		out.Reason = "applied-target-unavailable"
		return out
	}
	root := view.Observation.Rows["Open_vSwitch"][p.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	target, e3 := strconv.ParseInt(*out.Target, 10, 64)
	curFloor, _ := strconv.ParseInt(p.CurFloor, 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || next < target || cur < curFloor || cur > next {
		out.Applied = "unknown"
		out.Reason = "applied-counter-discontinuity"
		return out
	}
	value := strconv.FormatInt(cur, 10)
	out.Current = &value
	observed := view.Observation.Evidence.ObservedAt
	out.Observed = &observed
	if cur < target {
		out.Applied = "pending"
		out.Reason = "awaiting-ovs-vswitchd"
		return out
	}
	for _, intent := range after.Intents {
		port := view.Observation.Rows["Port"][intent.Object.OVSUUID]
		for _, id := range nativeRefs(port.Values["interfaces"]) {
			member := view.Observation.Rows["Interface"][id]
			if _, ok := member.Values["error"]; !ok {
				out.Applied = "unknown"
				out.Reason = "interface-apply-evidence-unavailable"
				return out
			}
			if value, ok := member.Values["error"]; ok {
				if values, ok := value.([]any); !ok || len(values) > 0 {
					out.Applied = "unknown"
					out.Reason = "interface-apply-error"
					return out
				}
			}
		}
	}
	out.Applied = "applied"
	out.Reason = "ovs-vswitchd-applied"
	return out
}

var _ execution.Provider = (*Executor)(nil)
