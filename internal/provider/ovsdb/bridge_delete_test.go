package ovsdb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestDeletionNativeGraphsUseDistinctMarkersAndFreshUUIDs(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			bind := func(table string) candidate.Binding {
				return candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: v.Candidate.Generation}
			}
			graph := func() candidate.BridgeGraph {
				return candidate.BridgeGraph{Name: "br-del", Root: v.Observation.Evidence.Root, Bridge: bind("Bridge"), Port: bind("Port"), Interface: bind("Interface")}
			}
			original, replacement := graph(), graph()
			i := candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.BridgeDelete, Object: original.Bridge,
				Deletion: &candidate.BridgeDeletion{Source: original, Replacement: replacement, SourceMarker: strings.Repeat("a", 64)}}
			e.Candidate.Intents = []candidate.StoredIntent{i}
			p, err := compileExecution(repository.NewID(), strings.Repeat("b", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if err = json.Unmarshal(p.Native, &n); err != nil {
				t.Fatal(err)
			}
			if len(n.InsertUUIDs) != 0 || n.CreationMarker != i.Deletion.SourceMarker || strings.Contains(string(p.Native), `"op":"delete"`) {
				t.Fatal("deletion is not guarded root detach")
			}
			for _, needle := range []string{`"Mirror"`, `"qos"`, `"ingress_policing_rate"`} {
				if !strings.Contains(string(p.Native), needle) {
					t.Fatal("missing dependency guard", needle)
				}
			}
			candidate.Reverse(&e.Candidate.Intents[0])
			p, err = compileBridgeExecution(p.ID, strings.Repeat("c", 64), strings.Repeat("c", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(p.Native, &n); err != nil {
				t.Fatal(err)
			}
			if len(n.InsertUUIDs) != 3 {
				t.Fatal("missing replacement rows")
			}
			for index, uuid := range n.InsertUUIDs {
				for _, b := range original.Bindings() {
					if uuid == b.OVSUUID {
						t.Fatal("old identity resurrected")
					}
				}
				if n.Operations[index]["op"] != "insert" {
					t.Fatal(index)
				}
			}
			for _, b := range original.Bindings() {
				if !strings.Contains(string(p.Native), b.OVSUUID) {
					t.Fatal("old UUID absence unguarded")
				}
			}
		})
	}
}
