package ovsdb

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

const (
	telemetryRootID      = "00000000-0000-4000-8000-000000000001"
	telemetryBridgeID    = "00000000-0000-4000-8000-000000000002"
	telemetryExporterID  = "00000000-0000-4000-8000-000000000003"
	telemetryRestoreID   = "00000000-0000-4000-8000-000000000004"
	telemetryCollectorID = "00000000-0000-4000-8000-000000000005"
)

func nativeTelemetryFixture(t *testing.T, d discovered) inventory.ExecutionView {
	t.Helper()
	bridge := map[string]any{}
	for name, col := range d.native.Tables["Bridge"].Columns {
		if !graphConfigurationColumn("Bridge", name, col) {
			continue
		}
		empty, err := emptyNative(col)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(empty)
		reader := json.NewDecoder(bytes.NewReader(body))
		reader.UseNumber()
		var value any
		if err := reader.Decode(&value); err != nil {
			t.Fatal(err)
		}
		bridge[name], err = normalize(value, col)
		if err != nil {
			t.Fatal(err)
		}
	}
	bridge["name"] = "br-fixture"
	root := map[string]any{
		"next_cfg": "10", "cur_cfg": "10", "bridges": []any{telemetryBridgeID},
		"external_ids": map[string]any{}, "other_config": map[string]any{},
	}
	return inventory.ExecutionView{Observation: inventory.Observation{
		Schema: d.public, Evidence: inventory.Evidence{Root: telemetryRootID},
		Rows: inventory.Rows{
			"Open_vSwitch": {telemetryRootID: {UUID: telemetryRootID, Values: root, Configuration: root}},
			"Bridge":       {telemetryBridgeID: {UUID: telemetryBridgeID, Values: maps.Clone(bridge), Configuration: bridge}},
		},
	}}
}

func telemetryTestEnvelope(operation string, change *candidate.TelemetryChange) candidate.Envelope {
	intent := candidate.StoredIntent{Operation: operation, Telemetry: change, Object: candidate.Binding{Table: "Bridge", OVSUUID: telemetryBridgeID}}
	return candidate.Envelope{Candidate: candidate.Candidate{Intents: []candidate.StoredIntent{intent}}}
}

func telemetryPlanForTest(t *testing.T, plan execution.Plan) nativePlan {
	t.Helper()
	var compiled nativePlan
	if err := json.Unmarshal(plan.Native, &compiled); err != nil || compiled.Telemetry == nil {
		t.Fatalf("missing telemetry checkpoint: %v", err)
	}
	return compiled
}

func setTelemetryBridgeImage(view inventory.ExecutionView, image map[string]any) {
	bridge := view.Observation.Rows["Bridge"][telemetryBridgeID]
	bridge.Configuration = image
	view.Observation.Rows["Bridge"][telemetryBridgeID] = bridge
}

func TestTelemetryWriteUsesReservedInsertAndNativeAppliedGuards(t *testing.T) {
	d := schema(t, "3.3.9")
	for _, protocol := range []string{"sflow", "netflow", "ipfix"} {
		t.Run(protocol, func(t *testing.T) {
			view := nativeTelemetryFixture(t, d)
			envelope := telemetryTestEnvelope(candidate.TelemetrySet, &candidate.TelemetryChange{Protocol: protocol,
				After: &candidate.TelemetryImage{UUID: telemetryExporterID, Targets: []string{"127.0.0.1:6343"}},
			})
			marker := strings.Repeat("a", 64)
			p, err := compileTelemetryExecution("plan-id", marker, envelope, view, d)
			if err != nil {
				t.Fatal(err)
			}
			n := telemetryPlanForTest(t, p)
			if len(n.InsertUUIDs) != 1 || !strings.Contains(string(p.Native), `"uuid":"`+telemetryExporterID+`"`) {
				t.Fatal("create did not use reserved row UUID")
			}
			state := n.Telemetry
			if protocol == "netflow" && (state.After["active_timeout"] != "0" || state.After["add_id_to_interface"] != false) {
				t.Fatal("scalar NetFlow defaults were lost")
			}
			setTelemetryBridgeImage(view, state.BridgeAfter)
			view.Observation.Rows[telemetryTables[protocol]] = map[string]inventory.Row{telemetryExporterID: {UUID: telemetryExporterID, Configuration: state.After}}
			proof, err := telemetryAppliedProof(p, view, d)
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range proof[:len(proof)-1] {
				if raw.(map[string]any)["op"] != "wait" {
					t.Fatal("Applied proof may not mutate OVSDB")
				}
			}
			compensation, err := compileTelemetryRollback(p, strings.Repeat("b", 64), state, view, d)
			if err != nil {
				t.Fatal(err)
			}
			reverse := telemetryPlanForTest(t, compensation).Telemetry
			if !reverse.Rollback || reverse.BeforeUUID != telemetryExporterID || reverse.AfterUUID != "" {
				t.Fatal("new exporter did not compensate by guarded detachment")
			}
		})
	}
}

func TestTelemetryClearRestoresOpaqueIPFIXImageWithFreshUUID(t *testing.T) {
	d := schema(t, "3.3.9")
	view := nativeTelemetryFixture(t, d)
	bridge := view.Observation.Rows["Bridge"][telemetryBridgeID].Configuration
	bridge["ipfix"] = []any{telemetryExporterID}
	row := telemetryDefaults(d, "IPFIX")
	row["targets"] = []any{"127.0.0.1:4739"}
	row["cache_active_timeout"] = []any{"33"}
	row["other_config"] = map[string]any{"virtual_obs_id": "preserved"}
	row["external_ids"] = map[string]any{"test-id": "owned-by-local-operator"}
	view.Observation.Rows["IPFIX"] = map[string]inventory.Row{telemetryExporterID: {UUID: telemetryExporterID, Configuration: row}}
	envelope := telemetryTestEnvelope(candidate.TelemetryClear, &candidate.TelemetryChange{Protocol: "ipfix", RestoreUUID: telemetryRestoreID,
		Before: &candidate.TelemetryImage{UUID: telemetryExporterID, Digest: inventory.Digest(row)},
	})
	marker := strings.Repeat("a", 64)
	view.Observation.Rows[flowSampleCollectorTable] = map[string]inventory.Row{telemetryCollectorID: {
		UUID: telemetryCollectorID, Configuration: map[string]any{"ipfix": []any{telemetryExporterID}},
	}}
	if _, err := compileTelemetryExecution("plan-id", marker, envelope, view, d); err == nil {
		t.Fatal("a per-flow writer could share the Bridge's IPFIX row")
	}
	delete(view.Observation.Rows, flowSampleCollectorTable)
	p, err := compileTelemetryExecution("plan-id", marker, envelope, view, d)
	if err != nil {
		t.Fatal(err)
	}
	n := telemetryPlanForTest(t, p)
	if !bytes.Contains(p.Native, []byte(flowSampleCollectorTable)) || bytes.Contains(p.Native, []byte(`"op":"delete"`)) {
		t.Fatal("clear must guard concurrent per-flow references and let OVSDB collect the row")
	}
	setTelemetryBridgeImage(view, n.Telemetry.BridgeAfter)
	delete(view.Observation.Rows["IPFIX"], telemetryExporterID)
	reverse, err := compileTelemetryRollback(p, strings.Repeat("b", 64), n.Telemetry, view, d)
	if err != nil {
		t.Fatal(err)
	}
	state := telemetryPlanForTest(t, reverse).Telemetry
	if state.AfterUUID != telemetryRestoreID || state.RetiredUUID != telemetryExporterID ||
		inventory.Digest(state.After) != inventory.Digest(row) {
		t.Fatal("rollback lost protected configuration or reused the old UUID")
	}
	externalChange := maps.Clone(n.Telemetry.BridgeAfter)
	externalChange["rstp_enable"] = true
	setTelemetryBridgeImage(view, externalChange)
	if _, err = compileTelemetryRollback(p, strings.Repeat("b", 64), n.Telemetry, view, d); err == nil {
		t.Fatal("rollback accepted an externally changed Bridge")
	}
}
