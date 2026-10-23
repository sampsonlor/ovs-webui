//go:build linux

package ovsdb

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestMulticastPlanGuardsWholeGraphAndPreservesForeignKeys(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v := treeFenceFixture(t, version)
			var uuid string
			for id := range v.Observation.Rows["Bridge"] {
				uuid = id
			}
			row := v.Observation.Rows["Bridge"][uuid]
			row.Values["mcast_snooping_enable"] = false
			row.Values["other_config"].(map[string]any)["keep-native"] = "opaque"
			v.Observation.Rows["Bridge"][uuid] = row
			cfg := multicast.Configuration{Parameters: map[string]*string{multicast.Keys[0]: nil, multicast.Keys[1]: nil, multicast.Keys[2]: nil}}
			age := "300"
			after := multicast.Clone(cfg)
			after.Enabled = true
			after.Parameters[multicast.Keys[0]] = &age
			b := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: uuid, Table: "Bridge", Generation: v.Candidate.Generation}
			v.Candidate.Multicasts = map[string]candidate.MulticastBridge{b.ManagementID: {Binding: b, Configuration: cfg, Known: true, Supported: true, Authority: "local-managed", Dependency: "whole-bridge", Bindings: []candidate.Binding{b}}}
			i := candidate.StoredIntent{ID: repository.NewID(), Object: b, Operation: candidate.BridgeMulticastSet, Schema: v.Candidate.Schema, Dependency: "whole-bridge", Multicast: &candidate.MulticastChange{Before: cfg, After: after}}
			envelope := candidate.Envelope{Candidate: candidate.Candidate{Intents: []candidate.StoredIntent{i}}}
			p, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), envelope, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var plan nativePlan
			if json.Unmarshal(p.Native, &plan) != nil || len(plan.Operations) != 9 || len(plan.CountIndexes) != 3 || len(p.GraphBindings) != 1 {
				t.Fatal("native graph guard omitted")
			}
			for n, table := range treeFenceTables {
				if plan.Operations[n]["op"] != "wait" || plan.Operations[n]["table"] != table || plan.Operations[n]["timeout"] != float64(0) {
					t.Fatal("persistent row set unguarded", table)
				}
			}
			fields := plan.Operations[4]["row"].(map[string]any)
			if len(fields) != 2 || fields["mcast_snooping_enable"] != true || !strings.Contains(string(p.Native), "keep-native") || !strings.Contains(string(p.Native), candidate.MulticastMarker) {
				t.Fatal("native field/key preservation failed", string(p.Native))
			}
			bview := v.Candidate.Multicasts[b.ManagementID]
			bview.Configuration = after
			v.Candidate.Multicasts[b.ManagementID] = bview
			if _, err := compileExecution(p.ID, p.Marker, envelope, v, d); err == nil {
				t.Fatal("changed original accepted")
			}
			candidate.AfterImage(&envelope.Candidate.Intents[0])
			if _, err := compileExecution(p.ID, p.Marker, envelope, v, d); err != nil {
				t.Fatal("after image rejected", err)
			}
		})
	}
}
