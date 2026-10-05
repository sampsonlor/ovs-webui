package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func patchFixture(t *testing.T) (*Service, Observation, Decision, authn.Claims, Binding, Binding) {
	t.Helper()
	s, o, d, c := fixture(t)
	var a, b Binding
	for _, binding := range d.Bindings {
		if binding.Table == "Interface" {
			if a.UUID == "" {
				a = binding
			} else {
				b = binding
			}
		}
	}
	one, two := o.Rows["Interface"][a.UUID], o.Rows["Interface"][b.UUID]
	one.Values["type"], two.Values["type"] = "patch", "patch"
	one.Values["options"] = map[string]any{"peer": two.Values["name"]}
	two.Values["options"] = map[string]any{"peer": one.Values["name"]}
	for j := range o.Schema.Tables {
		for k := range o.Schema.Tables[j].Columns {
			col := &o.Schema.Tables[j].Columns[k]
			col.PatchCompatible = o.Schema.Tables[j].Name == "Interface" && (col.Name == "type" || col.Name == "options") || o.Schema.Tables[j].Name == "Bridge" && col.Name == "datapath_type"
		}
	}
	s.install(o, d)
	return s, o, d, c, a, b
}

func TestInterfacePatchPeerSnapshotPermissionAndContract(t *testing.T) {
	s, o, d, c, a, b := patchFixture(t)
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		s.install(o, d)
		v, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": a.ManagementID}, url.Values{}, c)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(v)
		op, _, _ := contract.Match("GET", "/api/v1/interfaces/"+a.ManagementID)
		if err := op.ValidateResponse(200, data); err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)["patch_peer"].(map[string]any)
	}
	p := read()
	if p["availability"] != "known" || p["reason"] != "PATCH_RECIPROCAL_CONFIGURATION" || p["peer_ref"].(map[string]any)["id"] != b.ManagementID || p["source"].(map[string]any)["authority"] != "ovsdb-configuration" {
		t.Fatal(p)
	}
	c.Capabilities = []string{"inventory.read", "state.read"}
	for _, typ := range []string{"patch", "internal", "future-native"} {
		o.Rows["Interface"][a.UUID].Values["type"] = typ
		p = read()
		if p["availability"] != "withheld" || p["reason"] != "CONFIGURATION_WITHHELD" || p["peer_ref"] != nil || p["peer_port_ref"] != nil || p["peer_bridge_ref"] != nil {
			t.Fatal("type/peer permission leak", p)
		}
	}
}

func TestInterfacePatchPeerExceptionsNeverInventLinks(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(*Observation, *Decision, Binding, Binding)
	}{
		{"unknown-type", "PATCH_TYPE_UNKNOWN", func(o *Observation, _ *Decision, a, _ Binding) { delete(o.Rows["Interface"][a.UUID].Values, "type") }},
		{"different-type", "NOT_PATCH_INTERFACE", func(o *Observation, _ *Decision, a, _ Binding) {
			o.Rows["Interface"][a.UUID].Values["type"] = "future-native"
		}},
		{"unknown-options", "PATCH_OPTIONS_UNKNOWN", func(o *Observation, _ *Decision, a, _ Binding) { delete(o.Rows["Interface"][a.UUID].Values, "options") }},
		{"no-peer", "PATCH_PEER_UNSPECIFIED", func(o *Observation, _ *Decision, a, _ Binding) {
			o.Rows["Interface"][a.UUID].Values["options"] = map[string]any{}
		}},
		{"invalid-peer", "PATCH_PEER_UNSPECIFIED", func(o *Observation, _ *Decision, a, _ Binding) {
			o.Rows["Interface"][a.UUID].Values["options"] = map[string]any{"peer": 123}
		}},
		{"self", "PATCH_PEER_SELF", func(o *Observation, _ *Decision, a, _ Binding) {
			o.Rows["Interface"][a.UUID].Values["options"] = map[string]any{"peer": o.Rows["Interface"][a.UUID].Values["name"]}
		}},
		{"absent", "PATCH_PEER_NOT_FOUND", func(o *Observation, _ *Decision, a, _ Binding) {
			o.Rows["Interface"][a.UUID].Values["options"] = map[string]any{"peer": "absent-peer"}
		}},
		{"ambiguous", "PATCH_PEER_AMBIGUOUS", func(o *Observation, _ *Decision, _, b Binding) {
			id := repository.NewID()
			o.Rows["Interface"][id] = Row{UUID: id, Values: map[string]any{"name": o.Rows["Interface"][b.UUID].Values["name"]}}
		}},
		{"wrong-peer-type", "PATCH_PEER_TYPE_MISMATCH", func(o *Observation, _ *Decision, _, b Binding) { o.Rows["Interface"][b.UUID].Values["type"] = "system" }},
		{"one-way", "PATCH_PEER_NOT_RECIPROCAL", func(o *Observation, _ *Decision, _, b Binding) {
			o.Rows["Interface"][b.UUID].Values["options"] = map[string]any{"peer": "other-peer"}
		}},
		{"no-unique-parent", "PATCH_PEER_RELATION_UNKNOWN", func(o *Observation, _ *Decision, _, b Binding) {
			id := repository.NewID()
			o.Rows["Port"][id] = Row{UUID: id, Values: map[string]any{"interfaces": []any{b.UUID}}}
		}},
		{"unknown-datapath", "PATCH_DATAPATH_UNKNOWN", func(o *Observation, _ *Decision, _, _ Binding) {
			for _, r := range o.Rows["Bridge"] {
				delete(r.Values, "datapath_type")
			}
		}},
		{"retired-peer", "PATCH_PEER_IDENTITY_UNAVAILABLE", func(_ *Observation, d *Decision, _, b Binding) {
			x := d.Bindings[Key("Interface", b.UUID)]
			x.State = "retired"
			d.Bindings[Key("Interface", b.UUID)] = x
		}},
		{"missing-binding", "PATCH_PEER_IDENTITY_UNAVAILABLE", func(_ *Observation, d *Decision, _, b Binding) { delete(d.Bindings, Key("Interface", b.UUID)) }},
		{"stale", "PATCH_OBSERVATION_STALE", func(o *Observation, _ *Decision, _, _ Binding) {
			o.Evidence.ObservedAt = time.Now().Add(-10 * time.Second)
		}},
		{"partial", "PATCH_OBSERVATION_STALE", func(_ *Observation, d *Decision, _, _ Binding) { d.State = "unknown" }},
		{"schema", "PATCH_SCHEMA_UNSUPPORTED", func(o *Observation, _ *Decision, _, _ Binding) {
			for j := range o.Schema.Tables {
				for k := range o.Schema.Tables[j].Columns {
					o.Schema.Tables[j].Columns[k].PatchCompatible = false
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, o, d, _, a, b := patchFixture(t)
			tc.mutate(&o, &d, a, b)
			s.install(o, d)
			// Resolve against the same view even for intentionally malformed fixture
			// parents, which public resource reads separately reject.
			var bridge Row
			for _, r := range o.Rows["Bridge"] {
				bridge = r
				break
			}
			fresh := "fresh"
			if time.Since(o.Evidence.ObservedAt) > FreshFor {
				fresh = "stale"
			}
			p := interfacePatchPeer(s.current, o.Rows["Interface"][a.UUID], bridge, fresh, true)
			if p["reason"] != tc.reason || p["peer_ref"] != nil || p["peer_port_ref"] != nil || p["peer_bridge_ref"] != nil {
				t.Fatal(p)
			}
		})
	}
}

func TestInterfacePatchPeerDatapathsAndReplacementIdentity(t *testing.T) {
	s, o, d, c, a, b := patchFixture(t)
	// Separate the two original interfaces into independent Ports/Bridges.
	originalPort, _ := parent(s.current, "Port", "interfaces", a.UUID)
	originalBridge, _ := parent(s.current, "Bridge", "ports", originalPort.UUID)
	originalPort.Values["interfaces"] = []any{a.UUID}
	portID, bridgeID := repository.NewID(), repository.NewID()
	o.Rows["Port"][portID] = Row{UUID: portID, Values: map[string]any{"name": "second-port", "interfaces": []any{b.UUID}}}
	o.Rows["Bridge"][bridgeID] = Row{UUID: bridgeID, Values: map[string]any{"name": "second-bridge", "ports": []any{portID}, "datapath_type": "netdev"}}
	for table, id := range map[string]string{"Port": portID, "Bridge": bridgeID} {
		d.Bindings[Key(table, id)] = Binding{Table: table, UUID: id, ManagementID: repository.NewID(), State: "active"}
	}
	read := func() map[string]any {
		t.Helper()
		s.install(o, d)
		v, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": a.ManagementID}, url.Values{}, c)
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)["patch_peer"].(map[string]any)
	}
	if read()["reason"] != "PATCH_DATAPATH_MISMATCH" {
		t.Fatal("different datapaths matched")
	}
	originalBridge.Values["datapath_type"] = ""
	o.Rows["Bridge"][bridgeID].Values["datapath_type"] = "system"
	if read()["availability"] != "known" {
		t.Fatal("native default system not recognized")
	}
	old := b.ManagementID
	id := repository.NewID()
	r := o.Rows["Interface"][b.UUID]
	r.UUID = id
	delete(o.Rows["Interface"], b.UUID)
	o.Rows["Interface"][id] = r
	o.Rows["Port"][portID].Values["interfaces"] = []any{id}
	delete(d.Bindings, Key("Interface", b.UUID))
	if read()["reason"] != "PATCH_PEER_IDENTITY_UNAVAILABLE" {
		t.Fatal("unbound replacement selected")
	}
	d.Bindings[Key("Interface", id)] = Binding{Table: "Interface", UUID: id, ManagementID: repository.NewID(), State: "active"}
	p := read()
	if p["peer_ref"].(map[string]any)["id"] == old {
		t.Fatal("old identity reused")
	}
}
