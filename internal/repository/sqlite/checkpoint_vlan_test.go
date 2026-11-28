package sqlite

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/configtransfer"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func vlanCheckpointFixture(t *testing.T) (vlanCheckpointSource, []candidate.Binding) {
	t.Helper()
	generation, tx := repository.NewID(), repository.NewID()
	bindings := []candidate.Binding{
		{Table: "Port", Generation: generation, ManagementID: repository.NewID(), OVSUUID: repository.NewID()},
		{Table: "Interface", Generation: generation, ManagementID: repository.NewID(), OVSUUID: repository.NewID()},
	}
	source := vlanCheckpointSource{
		manifest: VLANCheckpointManifest{
			Format: checkpointFormat, EvidenceScope: checkpointScope, SourceDatabaseID: repository.NewID(), SourceSchema: 1,
			SourceSoftware: "test-manager-version", ConfirmedAt: time.UnixMilli(1700000000010).UTC(),
			TransactionID: tx, InstanceGeneration: generation,
		},
		root: repository.NewID(), schema: strings.Repeat("f", 64),
	}
	wait := func(table string, row map[string]any) map[string]any {
		return map[string]any{"op": "wait", "table": table, "rows": []any{row}}
	}
	native, err := json.Marshal(map[string]any{"operations": []any{
		wait("Port", map[string]any{
			"_uuid": []string{"uuid", bindings[0].OVSUUID}, "name": "synthetic-port",
			"interfaces":   []any{"set", [][]string{{"uuid", bindings[1].OVSUUID}}},
			"external_ids": []any{"map", [][]string{{"unrelated-key", "opaque-fixture-secret"}}},
		}),
		wait("Bridge", map[string]any{"_uuid": []string{"uuid", repository.NewID()}, "name": "synthetic-bridge"}),
		wait("Interface", map[string]any{"_uuid": []string{"uuid", bindings[1].OVSUUID}, "type": "internal"}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	mode, tag := "access", 23
	source.document, err = json.Marshal(execution.Plan{
		ID: tx, Generation: generation, Root: source.root, Schema: source.schema, Native: native,
		Envelope: candidate.Envelope{Owner: "opaque-fixture-owner", Seal: "opaque-fixture-seal", Candidate: candidate.Candidate{
			Generation: &generation, Intents: []candidate.StoredIntent{{
				Operation: "port.vlan.set", Object: bindings[0], Schema: source.schema,
				Value: candidate.VLAN{Mode: &mode, Tag: &tag, Trunks: []int{}, CVLANs: []int{}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return source, bindings
}

func checkpointFixtureBindings(bindings []candidate.Binding) func(string, string) (candidate.Binding, error) {
	return func(table, nativeID string) (candidate.Binding, error) {
		for _, entry := range bindings {
			if entry.Table == table && entry.OVSUUID == nativeID {
				return entry, nil
			}
		}
		return candidate.Binding{}, repository.ErrUnavailable
	}
}

func TestVLANCheckpointContainsIndependentTypedRecoveryAndManifest(t *testing.T) {
	source, bindings := vlanCheckpointFixture(t)
	data, err := projectVLANCheckpoint(source, checkpointFixtureBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeVLANCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"opaque-fixture", "synthetic-port", "operations", "external_ids", "envelope", "native"} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatalf("checkpoint contains private key or value %q", private)
		}
	}
	report, err := decodeVLANCheckpoint(encoded)
	if err != nil || report.Manifest != source.manifest || report.SHA256 != data.SHA256 {
		t.Fatal("independent report lost its source or digest", err)
	}
	recoveryJSON, _ := json.Marshal(report.Recovery)
	recovery, err := configtransfer.Decode(recoveryJSON)
	if err != nil {
		t.Fatal("checkpoint is not usable for normal reviewed transfer", err)
	}
	port := recovery.Ports[0]
	if port.Binding != bindings[0] || port.Member != bindings[1] || port.Shape != configtransfer.StandaloneInternal ||
		*port.VLAN.Mode != "access" || (*port.VLAN.Tag)[0] != 23 || *port.VLAN.Trunks == nil || len(*port.VLAN.Trunks) != 0 {
		t.Fatalf("wrong exact scoped recovery fields: %+v", port)
	}
	mutated := []int{24}
	report.Recovery.Ports[0].VLAN.Tag = &mutated
	damaged, err := encodeVLANCheckpoint(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeVLANCheckpoint(damaged); err == nil {
		t.Fatal("modified payload passed its original checksum")
	}
	unknown := append([]byte("{\n  \"unreviewed\": 1,"), encoded[1:]...)
	if _, err = decodeVLANCheckpoint(unknown); err == nil {
		t.Fatal("extra unreviewed envelope key")
	}
}

func TestVLANCheckpointFailsClosedOnUnsupportedOrWrongSource(t *testing.T) {
	for _, tc := range []struct {
		name, old, substitute string
		want                  error
	}{
		{"other operation", "port.vlan.set", "bridge.qos.set", ErrCheckpointScope},
		{"non-standalone or unmodeled type", "\"internal\"", "\"vxlan\"", ErrCheckpointScope},
		{"wrong original generation", "", "", repository.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, bindings := vlanCheckpointFixture(t)
			if tc.old == "" {
				source.manifest.InstanceGeneration = repository.NewID()
			} else {
				source.document = bytes.ReplaceAll(source.document, []byte(tc.old), []byte(tc.substitute))
			}
			if _, err := projectVLANCheckpoint(source, checkpointFixtureBindings(bindings)); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
