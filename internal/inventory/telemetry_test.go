package inventory

import (
	"fmt"
	"testing"
)

func TestTelemetryProjectsIndependentReferencesAndSafeCollectorLabels(t *testing.T) {
	const netflowID = "11111111-1111-4111-8111-111111111111"
	const ipfixID = "22222222-2222-4222-8222-222222222222"
	bridge := Row{Values: map[string]any{
		"sflow": []any{netflowID}, "netflow": []any{netflowID}, "ipfix": []any{ipfixID},
	}}
	v := &view{observation: Observation{
		Schema: Schema{Telemetry: map[string]bool{"netflow": true, "ipfix": true}},
		Rows: Rows{
			"NetFlow": {netflowID: {UUID: netflowID, Values: map[string]any{"targets": []any{"user:password@example.test:2055"}}}},
			"IPFIX":   {ipfixID: {UUID: ipfixID, Values: map[string]any{"targets": []any{}}}},
		},
	}}
	sflow := telemetryProjection(v, bridge, true, "sflow", "sFlow", nil)
	netflow := telemetryProjection(v, bridge, true, "netflow", "NetFlow", []string{"engine_id"})
	ipfix := telemetryProjection(v, bridge, true, "ipfix", "IPFIX", nil)
	if sflow.State != "unsupported" || sflow.NativeUUID != nil || netflow.State != "configured" ||
		netflow.TargetsState != "unknown" || netflow.Targets != nil || netflow.TargetCount != nil ||
		netflow.Settings["engine_id"].State != "unsupported" {
		t.Fatalf("unexpected independent exporter projection: sFlow=%+v NetFlow=%+v", sflow, netflow)
	}
	if ipfix.State != "configured" || ipfix.TargetsState != "known" || ipfix.TargetCount == nil || *ipfix.TargetCount != 0 || ipfix.Targets == nil {
		t.Fatalf("empty native IPFIX target set must remain distinct from a missing observation: %+v", ipfix)
	}

	targets := make([]any, telemetryTargetLimit+1)
	for index := range targets {
		targets[index] = fmt.Sprintf("127.0.0.1:%d", 2000+index)
	}
	v.observation.Rows["NetFlow"][netflowID].Values["targets"] = targets
	netflow = telemetryProjection(v, bridge, true, "netflow", "NetFlow", nil)
	if netflow.TargetsState != "limited" || len(netflow.Targets) != telemetryTargetLimit ||
		netflow.TargetCount == nil || *netflow.TargetCount != len(targets) {
		t.Fatalf("detail did not preserve target count and bound the displayed subset: %+v", netflow)
	}
	delete(v.observation.Rows["IPFIX"], ipfixID)
	if missing := telemetryProjection(v, bridge, true, "ipfix", "IPFIX", nil); missing.State != "unknown" || missing.NativeUUID != nil {
		t.Fatalf("unresolved reference was interpreted as configured: %+v", missing)
	}
}
