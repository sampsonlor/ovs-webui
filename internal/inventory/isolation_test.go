package inventory

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func isolationFixture(t *testing.T) (*Service, Observation, Decision, candidate.Binding) {
	s, o, d, _, br := treePreparationFixture(t)
	b := o.Rows["Bridge"][br.OVSUUID]
	for _, k := range []string{"sflow", "netflow", "ipfix"} {
		b.Values[k] = []any{}
	}
	b.Values["mcast_snooping_enable"] = false
	b.Configuration = candidate.CloneConfiguration(b.Values)
	o.Rows["Bridge"][br.OVSUUID] = b
	pid, iid := repository.NewID(), repository.NewID()
	p := Row{UUID: pid, Values: map[string]any{"name": "iso-data", "interfaces": []any{iid}, "vlan_mode": []any{}, "tag": []any{}, "trunks": []any{}, "cvlans": []any{}, "bond_mode": []any{}, "lacp": []any{}, "qos": []any{}, "protected": false, "other_config": map[string]any{}, "external_ids": map[string]any{}}}
	i := Row{UUID: iid, Values: map[string]any{"name": "iso-data", "type": "system", "options": map[string]any{}, "external_ids": map[string]any{}}}
	p.Configuration = candidate.CloneConfiguration(p.Values)
	i.Configuration = candidate.CloneConfiguration(i.Values)
	o.Rows["Port"][pid] = p
	o.Rows["Interface"][iid] = i
	b.Values["ports"] = append(b.Values["ports"].([]any), pid)
	b.Configuration = candidate.CloneConfiguration(b.Values)
	o.Rows["Bridge"][br.OVSUUID] = b
	bind := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: pid, Table: "Port", Generation: d.Generation}
	d.Bindings[Key("Port", pid)] = Binding{ManagementID: bind.ManagementID, UUID: pid, Table: "Port", State: "active"}
	d.Bindings[Key("Interface", iid)] = Binding{ManagementID: repository.NewID(), UUID: iid, Table: "Interface", State: "active"}
	o.Schema.Tables = append(o.Schema.Tables, Table{Name: "Port", Columns: []Column{{Name: "protected", Monitored: true, Mutable: true, IsolationCompatible: true}}})
	s.install(o, d)
	return s, o, d, bind
}
func TestIsolationRootGrantGraphAndPrivateDependency(t *testing.T) {
	s, o, d, b := isolationFixture(t)
	read := func() candidate.IsolationPort {
		snapshot, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{b})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.Isolations[b.ManagementID]
	}
	if candidate.IsolationEditable(read()) {
		t.Fatal("implicit ownership")
	}
	if err := s.SetLocalVLANPorts([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	if candidate.IsolationEditable(read()) {
		t.Fatal("VLAN grant leaked")
	}
	if err := s.SetLocalIsolationPorts([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	p := read()
	if !candidate.IsolationEditable(p) || len(p.Bindings) != 5 {
		t.Fatal(p)
	}
	observed, err := s.Read(context.Background(), "readPort", map[string]string{"port_id": b.ManagementID}, nil, authn.Claims{Capabilities: []string{"inventory.read", "state.read"}})
	if err != nil {
		t.Fatal(err)
	}
	resource := observed.(map[string]any)
	permission := resource["isolation_execution"].(map[string]any)
	field := resource["fields"].(map[string]any)["protected"].(map[string]any)
	if permission["available"] != false || permission["authority"] != "withheld" || field["value"] != nil || field["availability"] != "withheld" {
		t.Fatal("observation exposed configuration or ownership", resource)
	}
	row := o.Rows["Port"][b.OVSUUID]
	row.Values["protected"] = true
	row.Configuration["protected"] = true
	o.Rows["Port"][b.OVSUUID] = row
	s.install(o, d)
	if read().Dependency != p.Dependency || !read().Protected {
		t.Fatal("own touched field became dependency")
	}
	row.Configuration["other_config"].(map[string]any)["unknown-native-key"] = "changed"
	s.install(o, d)
	if read().Dependency == p.Dependency {
		t.Fatal("unrelated raw key omitted")
	}
	if s.SetLocalIsolationPorts([]string{b.ManagementID, b.ManagementID}) == nil || !candidate.IsolationEditable(read()) {
		t.Fatal("invalid grant destroyed old policy")
	}
	if err := s.SetLocalIsolationPorts(nil); err != nil || candidate.IsolationEditable(read()) {
		t.Fatal("revocation")
	}
}
