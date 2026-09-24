package inventory

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestBondProjectionHasIndependentAuthorityAndIdentityDependencies(t *testing.T) {
	s, o, d, claims := fixture(t)
	var binding candidate.Binding
	var bridge Row
	for _, b := range o.Rows["Bridge"] {
		bridge = b
	}
	for _, b := range d.Bindings {
		if b.Table == "Port" {
			binding = candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: b.Table, Generation: d.Generation}
		}
	}
	root := repository.NewID()
	o.Evidence.Root = root
	o.Rows["Open_vSwitch"][root] = Row{UUID: root, Values: map[string]any{"bridges": []any{bridge.UUID}}}
	bridge.Values["stp_enable"], bridge.Values["rstp_enable"], bridge.Values["flood_vlans"] = false, false, []any{}
	port := o.Rows["Port"][binding.OVSUUID]
	port.Values["other_config"] = map[string]any{"unrelated": "keep"}
	for n, table := range o.Schema.Tables {
		if table.Name == "Port" {
			for j, c := range table.Columns {
				if c.Name == "lacp" || c.Name == "bond_mode" {
					o.Schema.Tables[n].Columns[j].BondCompatible = true
				}
			}
			o.Schema.Tables[n].Columns = append(o.Schema.Tables[n].Columns, Column{Name: "other_config", Monitored: true, BondCompatible: true})
		}
	}
	s.install(o, d)
	read := func() candidate.Port {
		t.Helper()
		snapshot, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.Ports[binding.ManagementID]
	}
	p := read()
	if !p.BondKnown || !p.BondSupported || p.BondAuthority != "unknown" || len(p.Members) != 2 {
		t.Fatal(p)
	}
	if err := s.SetLocalVLANPorts([]string{binding.ManagementID}); err != nil {
		t.Fatal(err)
	}
	if read().BondAuthority != "unknown" {
		t.Fatal("VLAN ownership granted Bond authority")
	}
	if err := s.SetLocalBondPorts([]string{binding.ManagementID}); err != nil {
		t.Fatal(err)
	}
	p = read()
	if p.BondAuthority != "local-managed" {
		t.Fatal(p)
	}
	port.Values["other_config"].(map[string]any)["unrelated"] = "external-writer"
	s.install(o, d)
	if read().BondDependency != p.BondDependency || candidate.Digest(read().Bond) != candidate.Digest(p.Bond) {
		t.Fatal("unrelated map key invalidated target")
	}
	claims.Capabilities = append(claims.Capabilities, "workspace.write", "ovs.port.vlan.write")
	result, err := s.Read(context.Background(), "readPort", map[string]string{"port_id": binding.ManagementID}, url.Values{}, claims)
	if err != nil || result.(map[string]any)["bond_editable"] != false {
		t.Fatal("VLAN capability granted Bond edit", err)
	}
	claims.Capabilities = append(claims.Capabilities, "ovs.port.bond.write")
	result, err = s.Read(context.Background(), "readPort", map[string]string{"port_id": binding.ManagementID}, url.Values{}, claims)
	if err != nil || result.(map[string]any)["bond_editable"] != true {
		t.Fatal("Bond edit unavailable", err)
	}
	originalMode := port.Values["bond_mode"]
	port.Values["bond_mode"] = []any{"future-mode"}
	s.install(o, d)
	result, err = s.Read(context.Background(), "readPort", map[string]string{"port_id": binding.ManagementID}, url.Values{}, claims)
	if err != nil || result.(map[string]any)["bond_editable"] != false {
		t.Fatal("unproven original advertised edit access", err)
	}
	port.Values["bond_mode"] = originalMode
	bridge.Values["flood_vlans"] = []any{"20"}
	s.install(o, d)
	if read().BondDependency == p.BondDependency {
		t.Fatal("bridge constraint omitted from dependency")
	}
	for _, b := range d.Bindings {
		if b.Table == "Interface" {
			b.ManagementID = repository.NewID()
			d.Bindings[Key(b.Table, b.UUID)] = b
			break
		}
	}
	s.install(o, d)
	if read().BondDependency == p.BondDependency {
		t.Fatal("new member identity silently rebound")
	}
	port.Values["external_ids"] = map[string]any{"ovn-owned": "yes"}
	s.install(o, d)
	if read().BondAuthority != "externally-controlled" {
		t.Fatal("external ownership overridden")
	}
	s.now = func() time.Time { return o.Evidence.ObservedAt.Add(FreshFor + time.Second) }
	result, err = s.Read(context.Background(), "readPort", map[string]string{"port_id": binding.ManagementID}, url.Values{}, claims)
	if err != nil || result.(map[string]any)["bond_editable"] != false {
		t.Fatal("stale fields editable", err)
	}
}
