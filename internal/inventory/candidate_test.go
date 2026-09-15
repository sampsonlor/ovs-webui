package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func TestCandidateProjectionKeepsNativeFieldsAndRelevantDependencies(t *testing.T) {
	s, o, d, _ := fixture(t)
	var binding candidate.Binding
	for _, b := range d.Bindings {
		if b.Table == "Port" {
			binding = candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Port", Generation: d.Generation}
		}
	}
	row := o.Rows["Port"][binding.OVSUUID]
	row.Values["tag"] = []any{"10"}
	for n, table := range o.Schema.Tables {
		if table.Name == "Port" {
			for j, c := range table.Columns {
				if c.Name == "tag" || c.Name == "vlan_mode" || c.Name == "trunks" || c.Name == "cvlans" {
					o.Schema.Tables[n].Columns[j].VLANCompatible = true
					o.Schema.Tables[n].Columns[j].VLANModes = []string{"access", "trunk", "native-tagged", "native-untagged"}
				}
			}
		}
	}
	s.install(o, d)
	read := func() candidate.Snapshot {
		t.Helper()
		v, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := read()
	p := initial.Ports[binding.ManagementID]
	if !p.Known || !p.SchemaSupported || p.Authority != "unknown" || *p.VLAN.Mode != "native-untagged" || *p.VLAN.Tag != 10 || len(p.VLAN.Trunks) != 0 {
		t.Fatal(p)
	}
	if err := s.SetLocalVLANPorts([]string{binding.ManagementID}); err != nil {
		t.Fatal(err)
	}
	local := read()
	if local.Policy == initial.Policy || local.Ports[binding.ManagementID].Authority != "local-managed" {
		t.Fatal("ownership policy missing")
	}
	for _, member := range o.Rows["Interface"] {
		member.Values["link_state"] = []any{"down"}
	}
	s.install(o, d)
	if read().Revision != local.Revision {
		t.Fatal("operational observation invalidated VLAN")
	}
	for _, member := range o.Rows["Interface"] {
		member.Values["type"] = "internal"
	}
	s.install(o, d)
	changed := read()
	if changed.Ports[binding.ManagementID].Dependency == local.Ports[binding.ManagementID].Dependency {
		t.Fatal("member dependency ignored")
	}
	row.Values["external_ids"] = map[string]any{"ovn-port": "synthetic-controlled"}
	s.install(o, d)
	if read().Ports[binding.ManagementID].Authority != "externally-controlled" {
		t.Fatal("explicit external control overridden")
	}
	delete(row.Values, "tag")
	s.install(o, d)
	if read().Ports[binding.ManagementID].Known {
		t.Fatal("missing native value normalized")
	}
	s.now = func() time.Time { return o.Evidence.ObservedAt.Add(FreshFor + time.Second) }
	if _, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding}); err == nil {
		t.Fatal("stale snapshot used")
	}
}
