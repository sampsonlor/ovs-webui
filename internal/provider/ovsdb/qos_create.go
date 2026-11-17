package ovsdb

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

type qosCreationRow struct {
	table, uuid string
	row         map[string]any
}

func qosCreationIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) == 1 && c.Intents[0].Operation == candidate.QoSCreate && c.Intents[0].QoSCreation != nil {
		return c.Intents[0], true
	}
	return candidate.StoredIntent{}, false
}

func qosCreationRows(d discovered, i candidate.StoredIntent, marker string) ([]qosCreationRow, error) {
	c := i.QoSCreation
	labels := map[string]any{candidate.BridgeCreationMarker: marker}
	rows := make([]qosCreationRow, 0, len(c.Rates.Queues)+1)
	queues := make(map[string]any, len(c.Rates.Queues))
	nativeRow := func(table, uuid string, values map[string]any) error {
		row := make(map[string]any, len(values))
		for col, value := range values {
			wire, err := nativeValue(value, d.native.Tables[table].Columns[col])
			if err != nil {
				return err
			}
			row[col] = wire
		}
		rows = append(rows, qosCreationRow{table: table, uuid: uuid, row: row})
		return nil
	}
	for _, q := range c.Rates.Queues {
		key := strconv.FormatUint(uint64(q.ID), 10)
		b := c.Queues[key]
		queues[key] = b.OVSUUID
		err := nativeRow("Queue", b.OVSUUID, map[string]any{
			"external_ids": labels, "dscp": []any{},
			"other_config": map[string]any{
				"min-rate": strconv.FormatUint(q.Minimum, 10),
				"max-rate": strconv.FormatUint(q.Maximum, 10),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	err := nativeRow("QoS", i.Object.OVSUUID, map[string]any{
		"external_ids": map[string]any{candidate.BridgeCreationMarker: marker, candidate.QoSProfileName: c.Name},
		"type":         c.Rates.Type,
		"queues":       queues,
		"other_config": map[string]any{
			"max-rate": strconv.FormatUint(c.Rates.Maximum, 10),
		},
	})
	return rows, err
}

// OVSDB has no map-value-only reference predicate. Prove Queue ownership
// against the entire reference projection and fence that same projection in
// the transaction. A foreign reference added after the monitor snapshot
// therefore cannot be lost or damaged by a rollback.
func qosCreationQueueFence(d discovered, view inventory.ExecutionView, qos string, created map[string]bool) (map[string]any, error) {
	rows := view.Observation.Rows["QoS"]
	refs := make([]any, 0, len(rows))
	for _, uuid := range slices.Sorted(maps.Keys(rows)) {
		mapping, ok := rows[uuid].Values["queues"].(map[string]any)
		if !ok {
			return nil, apitypes.Fail(409, "QOS_QUEUE_SHARING_UNPROVEN")
		}
		if uuid != qos {
			for _, value := range mapping {
				id, ok := value.(string)
				if !ok || created[id] {
					return nil, apitypes.Fail(409, "QOS_QUEUE_SHARING_UNPROVEN")
				}
			}
		}
		wire, err := nativeValue(mapping, d.native.Tables["QoS"].Columns["queues"])
		if err != nil {
			return nil, err
		}
		refs = append(refs, map[string]any{"_uuid": uuidValue(uuid), "queues": wire})
	}
	return waitRows("QoS", []any{}, []string{"_uuid", "queues"}, refs), nil
}

func qosCreationGuards(d discovered, i candidate.StoredIntent, view inventory.ExecutionView, rows []qosCreationRow, present bool) ([]map[string]any, error) {
	qos := i.Object.OVSUUID
	labels := []any{"map", []any{[]any{candidate.QoSProfileName, i.QoSCreation.Name}}}
	wanted := []any{}
	if present {
		wanted = append(wanted, map[string]any{"_uuid": uuidValue(qos)})
		for _, port := range view.Observation.Rows["Port"] {
			if slices.Contains(nativeRefs(port.Configuration["qos"]), qos) {
				return nil, apitypes.Fail(409, "QOS_CREATED_PROFILE_BOUND")
			}
		}
	}
	ops := []map[string]any{qosUniqueReference("Port", "qos", qos, "", false)}
	// After deletion, a new unrelated profile may claim the freed alias.
	// Prove removal of the exact reserved identities independently of that name.
	if present || !i.QoSCreation.Compensating {
		ops = append(ops, waitRows("QoS", []any{[]any{"external_ids", "includes", labels}}, []string{"_uuid"}, wanted))
	}
	createdQueues := make(map[string]bool, len(rows)-1)
	for _, row := range rows {
		if row.table == "Queue" {
			createdQueues[row.uuid] = true
		}
		where := []any{uuidCondition(row.uuid)}
		if !present {
			ops = append(ops, waitRows(row.table, where, []string{"_uuid"}, []any{}))
			continue
		}
		values := maps.Clone(row.row)
		current := view.Observation.Rows[row.table][row.uuid]
		for column, want := range values {
			actualValues := current.Values
			if column == "external_ids" || column == "other_config" {
				actualValues = current.Configuration
			}
			actual, err := nativeValue(actualValues[column], d.native.Tables[row.table].Columns[column])
			if err != nil || candidate.Digest(actual) != candidate.Digest(want) {
				return nil, apitypes.Fail(409, "QOS_CREATED_CONFIGURATION_CHANGED")
			}
		}
		values["_uuid"] = uuidValue(row.uuid)
		ops = append(ops, waitRows(row.table, where, slices.Sorted(maps.Keys(values)), []any{values}))
	}
	if present {
		fence, err := qosCreationQueueFence(d, view, qos, createdQueues)
		if err != nil {
			return nil, err
		}
		ops = append(ops, fence)
	}
	return ops, nil
}

func compileQoSCreation(id, marker, creationMarker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	i, ok := qosCreationIntent(envelope.Candidate)
	if !ok || i.QoSCreation.BeforePresent == i.QoSCreation.AfterPresent {
		return out, apitypes.Fail(422, "INVALID_EXECUTION_SCOPE")
	}
	if view.Observation.Evidence.Root != i.QoSCreation.Root {
		return out, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	if !d.public.QoSCreate {
		return out, apitypes.Fail(409, "QOS_CREATION_SCHEMA_UNSUPPORTED")
	}
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) {
		return out, apitypes.Fail(409, "QOS_CREATION_CONFLICT")
	}
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	ceiling := int64(math.MaxInt64)
	if i.QoSCreation.AfterPresent {
		ceiling-- // Leave one counter advance for guarded compensation.
	}
	if e1 != nil || e2 != nil || next >= ceiling || cur > next {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	meta, ok := root.Values["external_ids"].(map[string]any)
	if !ok || len(meta) >= 128 && meta[candidate.QoSProfileMarker] == nil {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_CAPACITY")
	}
	if i.QoSCreation.BeforePresent && meta[candidate.QoSProfileMarker] != creationMarker {
		return out, apitypes.Fail(409, "ROLLBACK_CONFLICT")
	}
	g, err := guard(d, "Open_vSwitch", root, []string{"external_ids"}, []any{
		uuidCondition(root.UUID), []any{"next_cfg", "<", json.Number(strconv.FormatInt(ceiling, 10))},
		[]any{"next_cfg", ">=", next}, []any{"cur_cfg", ">=", cur},
	})
	if err != nil {
		return out, err
	}
	rows, err := qosCreationRows(d, i, creationMarker)
	if err != nil {
		return out, err
	}
	guards, err := qosCreationGuards(d, i, view, rows, i.QoSCreation.BeforePresent)
	if err != nil {
		return out, err
	}
	if i.QoSCreation.AfterPresent {
		// The resulting graph must admit exact ownership proof and
		// compensation. Do not create it if the current reference
		// projection is already unavailable or exceeds the plan budget.
		referenceGuard, err := qosCreationQueueFence(d, view, i.Object.OVSUUID, nil)
		if err != nil {
			return out, err
		}
		guards = append(guards, referenceGuard)
	}
	n := nativePlan{
		Operations: append([]map[string]any{g}, guards...), Evidence: view.Observation.Evidence,
		CreationMarker: creationMarker, InsertUUIDs: make(map[int]string),
	}
	if i.QoSCreation.AfterPresent {
		for _, row := range rows {
			n.InsertUUIDs[len(n.Operations)] = row.uuid
			n.Operations = append(n.Operations, map[string]any{"op": "insert", "table": row.table, "uuid": row.uuid, "row": row.row})
		}
	} else {
		// Delete the owning QoS first, then the exact Queue rows. Their
		// contents and foreign references have already been fenced.
		for k := len(rows) - 1; k >= 0; k-- {
			row := rows[k]
			n.CountIndexes = append(n.CountIndexes, len(n.Operations))
			n.Operations = append(n.Operations, map[string]any{"op": "delete", "table": row.table, "where": []any{uuidCondition(row.uuid)}})
		}
	}
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{
		"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)},
		"mutations": []any{
			[]any{"external_ids", "delete", []any{"set", []any{candidate.QoSProfileMarker}}},
			[]any{"external_ids", "insert", []any{"map", []any{[]any{candidate.QoSProfileMarker, marker}}}},
			[]any{"next_cfg", "+=", 1},
		},
	})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations,
		map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}},
		map[string]any{"op": "commit", "durable": true},
	)
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation, Root: root.UUID, Marker: marker,
		NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data}, nil
}

func qosCreationProof(p execution.Plan, view inventory.ExecutionView, d discovered) ([]any, error) {
	i, ok := qosCreationIntent(p.Envelope.Candidate)
	var n nativePlan
	if !ok || !d.public.QoSCreate || json.Unmarshal(p.Native, &n) != nil {
		return nil, errors.New("INVALID_QOS_CREATION_PLAN")
	}
	root := view.Observation.Rows["Open_vSwitch"][p.Root]
	labels, _ := root.Values["external_ids"].(map[string]any)
	if labels[candidate.QoSProfileMarker] != p.Marker {
		return nil, errors.New("QOS_COMMIT_MARKER_CHANGED")
	}
	g, err := guard(d, "Open_vSwitch", root, []string{"external_ids"}, []any{uuidCondition(root.UUID)})
	if err != nil {
		return nil, err
	}
	rows, err := qosCreationRows(d, i, n.CreationMarker)
	if err != nil {
		return nil, err
	}
	guards, err := qosCreationGuards(d, i, view, rows, i.QoSCreation.AfterPresent)
	if err != nil {
		return nil, err
	}
	ops := make([]any, 0, len(guards)+2)
	ops = append(ops, g)
	for _, op := range guards {
		ops = append(ops, op)
	}
	ops = append(ops, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(p.Root)}, "columns": []string{"_uuid", "next_cfg", "cur_cfg"}})
	if data, err := json.Marshal(ops); err != nil || len(data) > execution.MaxPlanBytes {
		return nil, errors.New("EXECUTION_PLAN_BUDGET")
	}
	return ops, nil
}
