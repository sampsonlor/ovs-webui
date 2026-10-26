package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func staticTunnelFixture(t *testing.T) (*Service, Observation, Decision, candidate.Binding, candidate.Binding) {
	s, o, d, port := isolationFixture(t)
	o.Schema.BridgeCreation, o.Schema.InternalPortCreation, o.Schema.TunnelConfiguration = true, true, true
	o.Schema.GraphDefaults = map[string]map[string]any{"Port": {}, "Interface": {}}
	root := o.Rows["Open_vSwitch"][o.Evidence.Root]
	root.Values["iface_types"] = []any{"system", "internal", "vxlan", "geneve", "gre"}
	root.Configuration = candidate.CloneConfiguration(root.Values)
	o.Rows["Open_vSwitch"][root.UUID] = root
	for id, row := range o.Rows["Port"] {
		for _, key := range []string{"qos", "cvlans", "bond_mode", "lacp", "vlan_mode"} {
			if row.Values[key] == nil {
				row.Values[key] = []any{}
			}
		}
		row.Values["protected"] = false
		row.Configuration = candidate.CloneConfiguration(row.Values)
		o.Rows["Port"][id] = row
	}
	for id, row := range o.Rows["Bridge"] {
		for _, key := range []string{"controller", "mirrors", "sflow", "netflow", "ipfix", "flood_vlans"} {
			row.Values[key] = []any{}
		}
		row.Values["stp_enable"], row.Values["rstp_enable"], row.Values["mcast_snooping_enable"] = false, false, false
		row.Configuration = candidate.CloneConfiguration(row.Values)
		o.Rows["Bridge"][id] = row
	}
	ifaceID := refs(o.Rows["Port"][port.OVSUUID].Values["interfaces"])[0]
	row := o.Rows["Interface"][ifaceID]
	row.Values["type"], row.Values["options"] = "vxlan", map[string]any{"remote_ip": "192.0.2.20"}
	row.Configuration = candidate.CloneConfiguration(row.Values)
	o.Rows["Interface"][ifaceID] = row
	ib := d.Bindings[Key("Interface", ifaceID)]
	iface := candidate.Binding{ManagementID: ib.ManagementID, OVSUUID: ib.UUID, Table: "Interface", Generation: d.Generation}
	s.install(o, d)
	return s, o, d, port, iface
}

func TestStaticTunnelInventoryKeepsRootAuthorityAndPrivateOptionsSeparate(t *testing.T) {
	s, o, d, port, iface := staticTunnelFixture(t)
	ids := []string{}
	for _, b := range d.Bindings {
		ids = append(ids, b.ManagementID)
	}
	if err := s.SetLocalTopologyObjects(ids); err != nil {
		t.Fatal(err)
	}
	read := func() candidate.Snapshot {
		t.Helper()
		x, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{port})
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	if read().Topology.TunnelAuthority[port.ManagementID] {
		t.Fatal("core grant leaked")
	}
	if err := s.SetLocalTunnelObjects(ids); err != nil {
		t.Fatal(err)
	}
	first := read()
	if !first.Topology.TunnelAuthority[port.ManagementID] || !first.Topology.TunnelAuthority[iface.ManagementID] || !first.Topology.TunnelSupported {
		t.Fatalf("qualified root graph rejected: %#v", first.Topology)
	}
	if err := s.SetLocalTunnelObjects(ids); err != nil || first.Topology.TunnelAuthorityRevision != read().Topology.TunnelAuthorityRevision {
		t.Fatal("identical root policy was not idempotent")
	}
	if err := s.SetLocalTunnelObjects(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocalTunnelObjects(ids); err != nil || first.Topology.TunnelAuthorityRevision == read().Topology.TunnelAuthorityRevision {
		t.Fatal("root policy ABA retained validation")
	}
	c := authn.Claims{PrincipalID: repository.NewID(), Revision: repository.NewID(), Capabilities: []string{"inventory.read", "configuration.read", "workspace.write", candidate.TunnelCapability}}
	item, err := s.Read(context.Background(), "readTunnel", map[string]string{"tunnel_id": port.ManagementID}, nil, c)
	if err != nil || item.(map[string]any)["native_tunnel"].(map[string]any)["configuration_availability"] != "known" {
		t.Fatal("known tunnel read failed", item, err)
	}
	row := o.Rows["Interface"][iface.OVSUUID]
	row.Configuration["options"].(map[string]any)["psk"] = "private-tunnel-credential"
	s.install(o, d)
	item, err = s.Read(context.Background(), "readTunnel", map[string]string{"tunnel_id": port.ManagementID}, nil, c)
	data, _ := json.Marshal(item)
	if err != nil || strings.Contains(string(data), "private-tunnel-credential") || !strings.Contains(string(data), "TUNNEL_ADVANCED_OPTIONS_READ_ONLY") {
		t.Fatal("advanced options leaked or lost read-only boundary", err)
	}
	if _, err := s.Read(context.Background(), "listTunnels", nil, url.Values{}, authn.Claims{Capabilities: []string{"inventory.read"}}); err == nil {
		t.Fatal("configuration permission bypassed by type-selected list")
	}
	s.now = func() time.Time { return o.Evidence.ObservedAt.Add(FreshFor + time.Second) }
	item, err = s.Read(context.Background(), "readTunnel", map[string]string{"tunnel_id": port.ManagementID}, nil, c)
	if err != nil || !strings.Contains(string(mustJSON(t, item)), "TUNNEL_OBSERVATION_STALE") {
		t.Fatal("stale observation retained configuration action", err)
	}
	t.Run("bounded_topology_closes_creation", func(t *testing.T) {
		s, o, d, p, f := staticTunnelFixture(t)
		portRow, ifaceRow := o.Rows["Port"][p.OVSUUID], o.Rows["Interface"][f.OVSUUID]
		for n := 0; n < 6; n++ {
			pid, iid := repository.NewID(), repository.NewID()
			name := "bulk" + strconv.Itoa(n)
			pv, iv := candidate.CloneConfiguration(portRow.Values), candidate.CloneConfiguration(ifaceRow.Values)
			pv["name"], pv["interfaces"], iv["name"] = name, []any{iid}, name
			o.Rows["Port"][pid] = Row{UUID: pid, Values: pv, Configuration: candidate.CloneConfiguration(pv)}
			o.Rows["Interface"][iid] = Row{UUID: iid, Values: iv, Configuration: candidate.CloneConfiguration(iv)}
			d.Bindings[Key("Port", pid)] = Binding{ManagementID: repository.NewID(), UUID: pid, Table: "Port", State: "active"}
			d.Bindings[Key("Interface", iid)] = Binding{ManagementID: repository.NewID(), UUID: iid, Table: "Interface", State: "active"}
			for bid, bridge := range o.Rows["Bridge"] {
				owner := false
				for _, id := range refs(bridge.Values["ports"]) {
					if id == p.OVSUUID {
						owner = true
					}
				}
				if !owner {
					continue
				}
				values := append(append([]any{}, bridge.Values["ports"].([]any)...), pid)
				bridge.Values["ports"] = values
				bridge.Configuration = candidate.CloneConfiguration(bridge.Values)
				o.Rows["Bridge"][bid] = bridge
			}
		}
		for id, row := range o.Rows["Port"] {
			trunks := []any{}
			for v := 1; v <= 1700; v++ {
				trunks = append(trunks, strconv.Itoa(v))
			}
			row.Values["trunks"], row.Values["vlan_mode"] = trunks, []any{"trunk"}
			row.Configuration = candidate.CloneConfiguration(row.Values)
			o.Rows["Port"][id] = row
		}
		s.install(o, d)
		ids := []string{}
		for _, b := range d.Bindings {
			ids = append(ids, b.ManagementID)
		}
		if err := s.SetLocalTunnelObjects(ids); err != nil {
			t.Fatal(err)
		}
		if snapshot, err := s.CandidateSnapshot(context.Background(), nil); err != nil || !snapshot.Topology.TunnelAuthority[p.ManagementID] {
			t.Fatal("bounded fixture did not retain an eligible native graph", err)
		}
		view, err := s.Read(context.Background(), "readInventoryTopology", nil, nil, c)
		if err != nil {
			t.Fatal(err)
		}
		data := view.(map[string]any)
		if len(d.Bindings) >= 64 || data["truncated"] != true || data["tunnel_editable"] != false || len(mustJSON(t, view)) > 56<<10 {
			t.Fatal("byte-bounded native topology retained tunnel creation", data["truncated"], data["tunnel_editable"])
		}
	})
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
