package ovsdb

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func qosIntent(c candidate.Candidate) (candidate.StoredIntent, bool) {
	if len(c.Intents) == 1 && c.Intents[0].Operation == candidate.QoSConfigure && c.Intents[0].QoS != nil {
		return c.Intents[0], true
	}
	return candidate.StoredIntent{}, false
}

func compileQoSExecution(id, marker string, envelope candidate.Envelope, view inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	i, ok := qosIntent(envelope.Candidate)
	if !ok {
		return out, apitypes.Fail(422, "INVALID_QOS_RATES")
	}
	checks, _ := candidate.Checks(envelope.Candidate, view.Candidate)
	if !candidate.Passed(checks) || !d.public.QoSRateWrite {
		return out, apitypes.Fail(409, "QOS_EXECUTION_CONFLICT")
	}
	p := view.Candidate.QoSPorts[i.Object.ManagementID]
	rows := view.Observation.Rows
	root := rows["Open_vSwitch"][view.Observation.Evidence.Root]
	port := rows["Port"][p.Binding.OVSUUID]
	iface := rows["Interface"][p.Interface.OVSUUID]
	next, e1 := number(root.Values["next_cfg"])
	cur, e2 := number(root.Values["cur_cfg"])
	if e1 != nil || e2 != nil || next == math.MaxInt64 || cur > next {
		return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSAFE")
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		c := d.native.Tables["Open_vSwitch"].Columns[name]
		if c == nil || c.Type != native.TypeInteger || name == "next_cfg" && (!c.Mutable() || c.Ephemeral()) {
			return out, apitypes.Fail(409, "APPLIED_COUNTER_UNSUPPORTED")
		}
	}
	labels, known := port.Configuration["external_ids"].(map[string]any)
	if !known || len(labels) >= 128 && labels[candidate.QoSMarker] == nil {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_UNSUPPORTED")
	}
	n := nativePlan{Evidence: view.Observation.Evidence}
	guardRow := func(table string, row inventory.Row, state ...string) error {
		columns := make([]string, 0, len(row.Configuration)+len(state))
		values := maps.Clone(row.Configuration)
		for name := range values {
			columns = append(columns, name)
		}
		for _, name := range state {
			if value, exists := row.Values[name]; exists {
				values[name] = value
			}
			if !slices.Contains(columns, name) {
				columns = append(columns, name)
			}
		}
		slices.Sort(columns)
		row.Values = values
		wait, err := guard(d, table, row, columns, []any{uuidCondition(row.UUID)})
		if err == nil {
			n.Operations = append(n.Operations, wait)
		}
		return err
	}
	if err := guardRow("Open_vSwitch", root, "next_cfg", "cur_cfg"); err != nil {
		return out, err
	}
	if err := guardRow("Bridge", rows["Bridge"][p.Bridge.OVSUUID]); err != nil {
		return out, err
	}
	if err := guardRow("Port", port); err != nil {
		return out, err
	}
	// Interface.error is sanitized by the monitor. Eligibility requires the
	// empty set, so the guarded wire value is exact without exposing messages.
	if err := guardRow("Interface", iface, "error", "ifindex", "ofport", "mtu"); err != nil {
		return out, err
	}
	qos := rows["QoS"][p.QoS.OVSUUID]
	if err := guardRow("QoS", qos, "type", "queues"); err != nil {
		return out, err
	}
	n.Operations = append(n.Operations,
		qosUniqueReference("Port", "qos", qos.UUID, port.UUID),
		qosUniqueReference("Port", "interfaces", iface.UUID, port.UUID),
		qosUniqueReference("Bridge", "ports", port.UUID, p.Bridge.OVSUUID),
	)
	// Queue references are map values. An exact table projection fences new
	// references from any native QoS row in the same transaction.
	ids := slices.Sorted(maps.Keys(rows["QoS"]))
	queueRefs := make([]any, 0, len(ids))
	for _, uuid := range ids {
		mapping, err := nativeValue(rows["QoS"][uuid].Values["queues"], d.native.Tables["QoS"].Columns["queues"])
		if err != nil {
			return out, err
		}
		queueRefs = append(queueRefs, map[string]any{"_uuid": uuidValue(uuid), "queues": mapping})
	}
	n.Operations = append(n.Operations, waitRows("QoS", []any{}, []string{"_uuid", "queues"}, queueRefs))
	for _, rate := range i.QoS.After.Queues {
		if err := guardRow("Queue", rows["Queue"][p.QueueUUIDs[rate.ID]], "dscp"); err != nil {
			return out, err
		}
	}
	mutate := func(table, uuid string, mutations ...any) {
		n.CountIndexes = append(n.CountIndexes, len(n.Operations))
		n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": table, "where": []any{uuidCondition(uuid)}, "mutations": mutations})
	}
	put := func(column string, keys []any, pairs []any) []any {
		return []any{[]any{column, "delete", []any{"set", keys}}, []any{column, "insert", []any{"map", pairs}}}
	}
	decimal := func(n uint64) string { return strconv.FormatUint(n, 10) }
	if i.QoS.Before.Maximum != i.QoS.After.Maximum {
		mutate("QoS", qos.UUID, put("other_config", []any{"max-rate"}, []any{[]any{"max-rate", decimal(i.QoS.After.Maximum)}})...)
	}
	for index, rate := range i.QoS.After.Queues {
		if rate != i.QoS.Before.Queues[index] {
			mutate("Queue", p.QueueUUIDs[rate.ID], put("other_config", []any{"min-rate", "max-rate"},
				[]any{[]any{"min-rate", decimal(rate.Minimum)}, []any{"max-rate", decimal(rate.Maximum)}})...)
		}
	}
	mutate("Port", port.UUID, put("external_ids", []any{candidate.QoSMarker}, []any{[]any{candidate.QoSMarker, marker}})...)
	mutate("Open_vSwitch", root.UUID, []any{"next_cfg", "+=", 1})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: envelope, Schema: d.public.Digest, Generation: view.Candidate.Generation,
		Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: data}, nil
}

func qosUniqueReference(table, column, target, row string) map[string]any {
	return waitRows(table, []any{[]any{column, "includes", uuidSet(target)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(row)}})
}

func (e *Executor) prepareQoSKernel(ctx context.Context, p execution.Plan, v inventory.ExecutionView, original *nativePlan) (execution.Plan, error) {
	i, ok := qosIntent(p.Envelope.Candidate)
	if !ok {
		return p, nil
	}
	target := v.Candidate.QoSPorts[i.Object.ManagementID]
	sample, err := e.inventory.QoSRateEvidence(ctx, target)
	if err != nil {
		return execution.Plan{}, err
	}
	matches := inventory.QoSRatesMatch(sample.Rates, i.QoS.Before, target.MTU)
	if i.QoS.Compensating {
		matches = original != nil && sample.StructureDigest == original.QoSKernelStructure &&
			(matches || sample.ConfigurationDigest == original.QoSKernelBefore)
	}
	if !matches {
		return execution.Plan{}, apitypes.Fail(409, "QOS_KERNEL_CONFIGURATION_CONFLICT")
	}
	var n nativePlan
	decoder := json.NewDecoder(bytes.NewReader(p.Native))
	decoder.UseNumber()
	if decoder.Decode(&n) != nil {
		return execution.Plan{}, apitypes.Fail(503, "EXECUTION_PLAN_INVALID")
	}
	n.QoSKernelBefore, n.QoSKernelStructure = sample.ConfigurationDigest, sample.StructureDigest
	if original != nil {
		n.QoSKernelRestore = original.QoSKernelBefore
	}
	p.Native, err = json.Marshal(n)
	if err == nil && len(p.Native) > execution.MaxPlanBytes {
		err = apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return p, err
}

func (e *Executor) qosDispatchCheck(ctx context.Context, p execution.Plan, n nativePlan) error {
	i, ok := qosIntent(p.Envelope.Candidate)
	if !ok {
		return nil
	}
	s, err := e.inventory.CandidateSnapshot(ctx, []candidate.Binding{i.Object})
	if err != nil {
		return err
	}
	checks, _ := candidate.Checks(p.Envelope.Candidate, s)
	if !candidate.Passed(checks) {
		return apitypes.Fail(409, "QOS_EXECUTION_CONFLICT")
	}
	kernel, err := e.inventory.QoSRateEvidence(ctx, s.QoSPorts[i.Object.ManagementID])
	if err != nil {
		return err
	}
	if kernel.ConfigurationDigest != n.QoSKernelBefore || kernel.StructureDigest != n.QoSKernelStructure {
		return apitypes.Fail(409, "QOS_KERNEL_CHANGED")
	}
	return nil
}

func (e *Executor) qosAppliedEvidence(ctx context.Context, p execution.Plan, v inventory.ExecutionView, n nativePlan) (string, error) {
	i, ok := qosIntent(p.Envelope.Candidate)
	if !ok {
		return "", nil
	}
	target := v.Candidate.QoSPorts[i.Object.ManagementID]
	sample, err := e.inventory.QoSRateEvidence(ctx, target)
	if err != nil {
		return "", err
	}
	if sample.StructureDigest != n.QoSKernelStructure || !inventory.QoSRatesMatch(sample.Rates, i.QoS.After, target.MTU) ||
		i.QoS.Compensating && sample.ConfigurationDigest != n.QoSKernelRestore {
		return "", apitypes.Fail(409, "QOS_INSTALLED_RATES_UNPROVEN")
	}
	return sample.ConfigurationDigest, nil
}
