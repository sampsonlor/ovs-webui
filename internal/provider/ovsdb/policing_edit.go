package ovsdb

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func policingConstraint(name string, c *native.ColumnSchema) bool {
	if name == "ofport_request" || !interfaceConfigurationConstraint(name, c) || !c.Mutable() || c.Ephemeral() {
		return false
	}
	max, err := c.TypeObj.Key.MaxInteger()
	return err == nil && max >= 1000000
}
func compilePolicingExecution(id, marker string, e candidate.Envelope, v inventory.ExecutionView, d discovered) (execution.Plan, error) {
	var out execution.Plan
	if len(e.Candidate.Intents) != 1 {
		return out, apitypes.Fail(422, "POLICING_SINGLE_INTENT_REQUIRED")
	}
	i := e.Candidate.Intents[0]
	m := i.Policing
	if i.Operation != candidate.InterfacePolicingSet || m == nil || !candidate.ValidPolicing(m.Before) || !candidate.ValidPolicing(m.After) {
		return out, apitypes.Fail(422, "INVALID_INTERFACE_POLICING")
	}
	for _, name := range []string{"ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"} {
		if !policingConstraint(name, d.native.Tables["Interface"].Columns[name]) {
			return out, apitypes.Fail(409, "POLICING_SCHEMA_UNSUPPORTED")
		}
	}
	root := v.Observation.Rows["Open_vSwitch"][v.Observation.Evidence.Root]
	bridge, port, iface := v.Observation.Rows["Bridge"][m.Bridge.OVSUUID], v.Observation.Rows["Port"][m.Port.OVSUUID], v.Observation.Rows["Interface"][i.Object.OVSUUID]
	if iface.InterfaceOptionsEmpty == nil || !*iface.InterfaceOptionsEmpty {
		return out, apitypes.Fail(409, "POLICING_OPTIONS_UNPROVEN")
	}
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
	meta := d.native.Tables["Interface"].Columns["external_ids"]
	labels, known := iface.Values["external_ids"].(map[string]any)
	if meta == nil || !bondConstraint("other_config", meta) || !known {
		return out, apitypes.Fail(409, "COMMIT_EVIDENCE_UNSUPPORTED")
	}
	if len(labels) >= 128 && labels[execution.MarkerKey] == nil {
		return out, apitypes.Fail(429, "COMMIT_EVIDENCE_CAPACITY")
	}
	n := nativePlan{Operations: []map[string]any{}, CountIndexes: []int{}, Evidence: v.Observation.Evidence}
	for _, g := range []struct {
		table   string
		row     inventory.Row
		columns []string
		where   []any
	}{
		{"Open_vSwitch", root, []string{"external_ids", "other_config"}, []any{uuidCondition(root.UUID), []any{"bridges", "includes", uuidSet(bridge.UUID)}, []any{"next_cfg", "<", json.Number(strconv.FormatInt(math.MaxInt64, 10))}, []any{"next_cfg", ">=", next}, []any{"cur_cfg", ">=", cur}}},
		{"Bridge", bridge, []string{"name", "datapath_type", "ports", "external_ids"}, []any{uuidCondition(bridge.UUID)}},
		{"Port", port, []string{"name", "interfaces", "external_ids"}, []any{uuidCondition(port.UUID)}},
		{"Interface", iface, []string{"name", "type", "options", "ifindex", "ofport", "error", "ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst", "external_ids"}, []any{uuidCondition(iface.UUID)}},
	} {
		op, err := guard(d, g.table, g.row, g.columns, g.where)
		if err != nil {
			return out, err
		}
		n.Operations = append(n.Operations, op)
	}
	n.Operations = append(n.Operations, waitRows("Port", []any{[]any{"interfaces", "includes", uuidSet(iface.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(port.UUID)}}), waitRows("Bridge", []any{[]any{"ports", "includes", uuidSet(port.UUID)}}, []string{"_uuid"}, []any{map[string]any{"_uuid": uuidValue(bridge.UUID)}}))
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	// Bursts are guarded at native zero and are never rewritten by this batch.
	n.Operations = append(n.Operations, map[string]any{"op": "update", "table": "Interface", "where": []any{uuidCondition(iface.UUID)}, "row": map[string]any{"ingress_policing_rate": m.After.Rate, "ingress_policing_kpkts_rate": m.After.PacketRate}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Interface", "where": []any{uuidCondition(iface.UUID)}, "mutations": []any{[]any{"external_ids", "delete", []any{"set", []any{execution.MarkerKey}}}, []any{"external_ids", "insert", []any{"map", []any{[]any{execution.MarkerKey, marker}}}}}})
	n.CountIndexes = append(n.CountIndexes, len(n.Operations))
	n.Operations = append(n.Operations, map[string]any{"op": "mutate", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "mutations": []any{[]any{"next_cfg", "+=", 1}}})
	n.TargetIndex = len(n.Operations)
	n.Operations = append(n.Operations, map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{uuidCondition(root.UUID)}, "columns": []string{"_uuid", "next_cfg"}}, map[string]any{"op": "commit", "durable": true})
	body, err := json.Marshal(n)
	if err != nil || len(body) > execution.MaxPlanBytes {
		return out, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return execution.Plan{ID: id, Envelope: e, Schema: d.public.Digest, Generation: v.Candidate.Generation, Root: root.UUID, Marker: marker, NextFloor: strconv.FormatInt(next, 10), CurFloor: strconv.FormatInt(cur, 10), Prepared: time.Now().UTC(), Native: body}, nil
}

// Kernel before-image belongs to the durable prepared execution, never to a
// browser-supplied Candidate. All kernel reads retain bounded native bindings.
func (e *Executor) preparePolicingKernel(ctx context.Context, p execution.Plan, v inventory.ExecutionView, original *nativePlan) (execution.Plan, error) {
	i := p.Envelope.Candidate.Intents[0]
	if i.Policing == nil {
		return p, nil
	}
	sample, err := e.inventory.PolicingEvidence(ctx, v.Candidate.Policings[i.Object.ManagementID])
	if err != nil {
		return execution.Plan{}, err
	}
	matched := inventory.PolicingMatches(sample, i.Policing.Before)
	if i.Policing.Compensating && original != nil && sample.ConfigurationDigest == original.PolicingKernelBefore {
		// ovs-vswitchd can be paused after the configuration commit; restoring
		// the original request must not wait for a rule that never got installed.
		matched = true
	}
	if !matched {
		return execution.Plan{}, apitypes.Fail(409, "POLICING_KERNEL_CONFIGURATION_CONFLICT")
	}
	var n nativePlan
	decoder := json.NewDecoder(bytes.NewReader(p.Native))
	decoder.UseNumber()
	if decoder.Decode(&n) != nil {
		return execution.Plan{}, apitypes.Fail(503, "EXECUTION_PLAN_INVALID")
	}
	n.PolicingKernelBefore = sample.ConfigurationDigest
	p.Native, err = json.Marshal(n)
	if err == nil && len(p.Native) > execution.MaxPlanBytes {
		return execution.Plan{}, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	return p, err
}
func (e *Executor) policingDispatchCheck(ctx context.Context, p execution.Plan, n nativePlan) error {
	if len(p.Envelope.Candidate.Intents) != 1 || p.Envelope.Candidate.Intents[0].Policing == nil {
		return nil
	}
	i := p.Envelope.Candidate.Intents[0]
	v, err := e.inventory.ExecutionView(ctx, candidate.Bindings(p.Envelope.Candidate))
	if err != nil {
		return err
	}
	checks, _ := candidate.Checks(p.Envelope.Candidate, v.Candidate)
	if !candidate.Passed(checks) || len(n.PolicingKernelBefore) != 64 {
		return apitypes.Fail(409, "POLICING_DISPATCH_CONFLICT")
	}
	sample, err := e.inventory.PolicingEvidence(ctx, v.Candidate.Policings[i.Object.ManagementID])
	if err != nil {
		return err
	}
	if sample.ConfigurationDigest != n.PolicingKernelBefore {
		return apitypes.Fail(409, "POLICING_KERNEL_CHANGED")
	}
	return nil
}
