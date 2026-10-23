package inventory

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"testing"
)

func multicastFixture(t *testing.T) (*Service, Observation, Decision, candidate.Binding) {
	s, o, d, _ := isolationFixture(t)
	var bridge candidate.Binding
	for _, b := range d.Bindings {
		if b.Table == "Bridge" {
			bridge = candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: b.Table, Generation: d.Generation}
			row := o.Rows["Bridge"][b.UUID]
			row.Configuration["flood_vlans"] = []any{}
			row.Values["flood_vlans"] = []any{}
			o.Rows["Bridge"][b.UUID] = row
		}
	}
	for id, p := range o.Rows["Port"] {
		p.Values["protected"] = false
		p.Configuration["protected"] = false
		o.Rows["Port"][id] = p
	}
	for i, table := range o.Schema.Tables {
		if table.Name == "Bridge" {
			o.Schema.Tables[i].Columns = append(table.Columns, Column{Name: "mcast_snooping_enable", Monitored: true, Mutable: true, MulticastCompatible: true})
			for _, name := range []string{"other_config", "external_ids"} {
				found := false
				for j, c := range o.Schema.Tables[i].Columns {
					if c.Name == name {
						c.Monitored = true
						c.Mutable = true
						c.MulticastCompatible = true
						o.Schema.Tables[i].Columns[j] = c
						found = true
					}
				}
				if !found {
					o.Schema.Tables[i].Columns = append(o.Schema.Tables[i].Columns, Column{Name: name, Monitored: true, Mutable: true, MulticastCompatible: true})
				}
			}
		}
	}
	s.install(o, d)
	return s, o, d, bridge
}
func TestMulticastRootGrantAndPersistentGraphRemainIndependent(t *testing.T) {
	s, o, d, b := multicastFixture(t)
	read := func() candidate.Snapshot {
		x, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{b})
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	if candidate.MulticastEditable(read().Multicasts[b.ManagementID]) {
		t.Fatal("implicit ownership")
	}
	if err := s.SetLocalSpanningTreeBridges([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	if candidate.MulticastEditable(read().Multicasts[b.ManagementID]) {
		t.Fatal("STP ownership leaked")
	}
	if err := s.SetLocalMulticastBridges([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	before := read()
	projected := before.Multicasts[b.ManagementID]
	if !candidate.MulticastEditable(projected) || len(projected.Bindings) != 5 {
		t.Fatal(projected)
	}
	for i, table := range o.Schema.Tables {
		if table.Name != "Bridge" {
			continue
		}
		for j, column := range table.Columns {
			if column.Name != "other_config" && column.Name != "external_ids" {
				continue
			}
			o.Schema.Tables[i].Columns[j].MulticastCompatible = false
			s.install(o, d)
			if read().Multicasts[b.ManagementID].Supported {
				t.Fatal("unqualified parameter/marker map became available", column.Name)
			}
			o.Schema.Tables[i].Columns[j].MulticastCompatible = true
			s.install(o, d)
		}
	}
	row := o.Rows["Bridge"][b.OVSUUID]
	row.Values["mcast_snooping_enable"] = true
	row.Configuration["mcast_snooping_enable"] = true
	row.Configuration["other_config"].(map[string]any)["mcast-snooping-aging-time"] = "300"
	o.Rows["Bridge"][b.OVSUUID] = row
	s.install(o, d)
	after := read().Multicasts[b.ManagementID]
	if projected.Dependency != after.Dependency || !after.Configuration.Enabled {
		t.Fatal("owned field became a dependency")
	}
	// Model the complete native transaction after-image, including its private
	// marker and all three parameters. It must retain the captured graph while
	// an unrelated map entry below must still invalidate that same graph.
	for key, value := range map[string]string{"mcast-snooping-aging-time": "300", "mcast-snooping-table-size": "64", "mcast-snooping-disable-flood-unregistered": "true"} {
		row.Configuration["other_config"].(map[string]any)[key] = value
	}
	row.Configuration["external_ids"].(map[string]any)[candidate.MulticastMarker] = "synthetic-owned-commit"
	row.Values["other_config"] = candidate.CloneConfiguration(row.Configuration["other_config"].(map[string]any))
	row.Values["external_ids"] = candidate.CloneConfiguration(row.Configuration["external_ids"].(map[string]any))
	o.Rows["Bridge"][b.OVSUUID] = row
	s.install(o, d)
	complete := read()
	i := candidate.StoredIntent{Object: b, Operation: candidate.BridgeMulticastSet, Schema: before.Schema, Dependency: projected.Dependency, Multicast: &candidate.MulticastChange{Before: projected.Configuration, After: complete.Multicasts[b.ManagementID].Configuration}}
	candidate.AfterImage(&i)
	checks, _ := candidate.Checks(candidate.Candidate{Intents: []candidate.StoredIntent{i}}, complete)
	if !candidate.Passed(checks) {
		t.Fatal("own native after-image was not admissible", checks)
	}
	private, err := s.ExecutionView(context.Background(), []candidate.Binding{b})
	if err != nil {
		t.Fatal(err)
	}
	checks, _ = candidate.Checks(candidate.Candidate{Intents: []candidate.StoredIntent{i}}, private.Candidate)
	if !candidate.Passed(checks) {
		t.Fatal("private execution snapshot lost the admissible after-image", checks)
	}
	row.Configuration["other_config"].(map[string]any)["foreign-key"] = "changed"
	s.install(o, d)
	if after.Dependency == read().Multicasts[b.ManagementID].Dependency {
		t.Fatal("unknown native key omitted")
	}
	if err := s.SetLocalMulticastBridges(nil); err != nil {
		t.Fatal(err)
	}
	if candidate.MulticastEditable(read().Multicasts[b.ManagementID]) {
		t.Fatal("revoke failed")
	}
	if err := s.SetLocalMulticastBridges([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	if before.Policy == read().Policy {
		t.Fatal("grant ABA invisible")
	}
	row.Configuration["flood_vlans"] = []any{"10"}
	s.install(o, d)
	if read().Multicasts[b.ManagementID].Gate != "MULTICAST_ADVANCED_POLICY_UNQUALIFIED" {
		t.Fatal("forced VLAN flooding became writable")
	}
	row.Configuration["flood_vlans"] = []any{}
	row.Configuration["other_config"].(map[string]any)["mcast-unqualified-policy"] = "true"
	s.install(o, d)
	if read().Multicasts[b.ManagementID].Gate != "MULTICAST_ADVANCED_POLICY_UNQUALIFIED" {
		t.Fatal("advanced Bridge policy became writable")
	}
	delete(row.Configuration["other_config"].(map[string]any), "mcast-unqualified-policy")
	for _, port := range o.Rows["Port"] {
		port.Configuration["other_config"].(map[string]any)["mcast-snooping-flood"] = "false"
		break
	}
	s.install(o, d)
	if read().Multicasts[b.ManagementID].Gate != "MULTICAST_ADVANCED_POLICY_UNQUALIFIED" {
		t.Fatal("unqualified explicit Port flooding became writable")
	}
}
