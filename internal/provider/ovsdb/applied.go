package ovsdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Compile only zero-timeout guards and a counter select. No update, mutate or
// commit operation is allowed into this private, read-only proof transaction.
func appliedProofOperations(p execution.Plan, view inventory.ExecutionView, d discovered) ([]any, error) {
	checked, err := compileExecution(p.ID, p.Marker, p.Envelope, view, d)
	if err != nil {
		return nil, err
	}
	var compiled nativePlan
	decoder := json.NewDecoder(bytes.NewReader(checked.Native))
	decoder.UseNumber()
	if err = decoder.Decode(&compiled); err != nil {
		return nil, err
	}
	ops := []any{}
	for _, op := range compiled.Operations {
		if op["op"] == "wait" {
			ops = append(ops, op)
		}
	}
	for _, intent := range p.Envelope.Candidate.Intents {
		port := view.Observation.Rows["Port"][intent.Object.OVSUUID]
		for _, id := range nativeRefs(port.Values["interfaces"]) {
			g, err := guard(d, "Interface", view.Observation.Rows["Interface"][id], []string{"error"}, []any{uuidCondition(id)})
			if err != nil {
				return nil, err
			}
			ops = append(ops, g)
		}
	}
	return append(ops, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(p.Root)}, "columns": []string{"_uuid", "next_cfg", "cur_cfg"}}), nil
}

// A monitor snapshot is a hint, even within its freshness window. Before
// claiming Applied, atomically verify its after-image, dependencies, marker and
// interface health against OVSDB itself. This closes monitor batching latency.
func (e *Executor) verifyApplied(ctx context.Context, p execution.Plan, view inventory.ExecutionView, out *execution.Outcome) error {
	conn, reader, d, identity, pid, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var original nativePlan
	if json.Unmarshal(p.Native, &original) != nil || identity != original.Evidence.Peer || d.public.Digest != p.Schema || !sameExecutionFile(e.provider.options, pid, original.Evidence) {
		return errors.New("APPLIED_IDENTITY_CHANGED")
	}
	ops, err := appliedProofOperations(p, view, d)
	if err != nil {
		return err
	}
	if err = send(conn, map[string]any{"method": "transact", "params": append([]any{"Open_vSwitch"}, ops...), "id": 3}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 32; i++ {
		message, err := readMessage(reader)
		if err != nil {
			return err
		}
		if message.Method == "echo" {
			if err = replyEcho(conn, message); err != nil {
				return err
			}
			continue
		}
		if message.Method != "" || string(message.ID) != "3" || len(message.Error) != 0 && string(message.Error) != "null" {
			return errors.New("APPLIED_PROOF_INVALID")
		}
		var results []map[string]json.RawMessage
		if json.Unmarshal(message.Result, &results) != nil || len(results) != len(ops) {
			return errors.New("APPLIED_PROOF_INVALID")
		}
		for _, result := range results {
			if result == nil || len(result["error"]) != 0 {
				return errors.New("APPLIED_GUARD_CHANGED")
			}
		}
		var rows []struct {
			ID      []string    `json:"_uuid"`
			Next    json.Number `json:"next_cfg"`
			Current json.Number `json:"cur_cfg"`
		}
		if out.Target == nil || json.Unmarshal(results[len(results)-1]["rows"], &rows) != nil || len(rows) != 1 || len(rows[0].ID) != 2 || rows[0].ID[0] != "uuid" || rows[0].ID[1] != p.Root {
			return errors.New("APPLIED_TARGET_UNAVAILABLE")
		}
		next, e1 := rows[0].Next.Int64()
		current, e2 := rows[0].Current.Int64()
		target, e3 := strconv.ParseInt(*out.Target, 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || current < target || next < current {
			return errors.New("APPLIED_COUNTER_UNPROVEN")
		}
		if ctx.Err() != nil || !sameExecutionFile(e.provider.options, pid, original.Evidence) {
			return errors.New("APPLIED_IDENTITY_CHANGED")
		}
		value, observed := strconv.FormatInt(current, 10), time.Now().UTC()
		out.Current, out.Observed = &value, &observed
		return nil
	}
	return errors.New("APPLIED_PROOF_BUDGET")
}
