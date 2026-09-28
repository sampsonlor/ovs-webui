package ovsdb

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestIsolatedBridgeNativeSchemaGraphAndCompensation(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			if !d.public.BridgeCreation {
				t.Fatal("schema lifecycle unsupported")
			}
			bind := func(table string) candidate.Binding {
				return candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: v.Candidate.Generation}
			}
			i := candidate.StoredIntent{ID: repository.NewID(), Operation: candidate.BridgeCreate, Object: bind("Bridge"), Creation: &candidate.BridgeCreation{Name: "br-new", Root: v.Observation.Evidence.Root, Port: bind("Port"), Interface: bind("Interface"), AfterPresent: true}}
			e.Candidate.Intents = []candidate.StoredIntent{i}
			p, err := compileExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if err = json.Unmarshal(p.Native, &n); err != nil {
				t.Fatal(err)
			}
			if len(n.InsertUUIDs) != 3 || len(n.CountIndexes) != 1 || strings.Contains(string(p.Native), `"op":"update"`) {
				t.Fatal("graph is not atomic/narrow")
			}
			candidate.Reverse(&e.Candidate.Intents[0])
			rollback, err := compileBridgeExecution(p.ID, strings.Repeat("b", 64), p.Marker, e, v, d)
			if err != nil {
				t.Fatal(err)
			}
			var r nativePlan
			_ = json.Unmarshal(rollback.Native, &r)
			if len(r.InsertUUIDs) != 0 || strings.Contains(string(rollback.Native), `"op":"delete"`) {
				t.Fatal("rollback must detach and let GC collect")
			}
			for _, needle := range []string{`"qos"`, `"mirrors"`, `"other_config"`, `"mtu_request"`, `"Mirror"`, `"bridges","delete"`} {
				if !strings.Contains(string(rollback.Native), needle) {
					t.Fatal("unguarded graph dependency", needle)
				}
			}
			if strings.Contains(string(rollback.Native), `"datapath_version"`) {
				t.Fatal("status became config conflict")
			}
			// Root and reference metadata, rather than a version string, gate the plan.
			tab := d.native.Tables["Bridge"]
			tab.IsRoot = true
			d.native.Tables["Bridge"] = tab
			if bridgeSchemaSupported(d) {
				t.Fatal("GC root change ignored")
			}
		})
	}
}
