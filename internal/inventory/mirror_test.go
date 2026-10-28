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

func nativeMirrorFixture(t *testing.T) (*Service, Observation, Decision, candidate.Binding, string, string) {
	t.Helper()
	s, o, d, port, _ := staticTunnelFixture(t)
	o.Schema.MirrorConfiguration = true
	o.Schema.MirrorDefaults = map[string]any{"name": "", "select_all": false, "select_src_port": []any{}, "select_dst_port": []any{}, "select_vlan": []any{}, "output_port": []any{}, "output_vlan": []any{}, "snaplen": []any{}, "filter": []any{}, "external_ids": map[string]any{}}
	uuid, id := repository.NewID(), repository.NewID()
	values := candidate.CloneConfiguration(o.Schema.MirrorDefaults)
	values["name"], values["select_src_port"], values["output_vlan"] = "span-1", []any{port.OVSUUID}, []any{"200"}
	o.Rows["Mirror"] = map[string]Row{uuid: {UUID: uuid, Values: values, Configuration: candidate.CloneConfiguration(values)}}
	d.Bindings[Key("Mirror", uuid)] = Binding{ManagementID: id, UUID: uuid, Table: "Mirror", State: "active"}
	var bridge string
	for bid, row := range o.Rows["Bridge"] {
		if !strings.Contains(string(mustJSON(t, row.Values["ports"])), port.OVSUUID) {
			continue
		}
		row.Values["mirrors"] = []any{uuid}
		row.Configuration = candidate.CloneConfiguration(row.Values)
		o.Rows["Bridge"][bid] = row
		bridge = d.Bindings[Key("Bridge", bid)].ManagementID
	}
	o.Schema.Tables = append(o.Schema.Tables, Table{Name: "Mirror", Columns: []Column{{Name: "name", Monitored: true}, {Name: "statistics", Monitored: true}, {Name: "select_src_port", Monitored: true, References: []Reference{{Table: "Port", Position: "key", Strength: "weak"}}}}})
	s.install(o, d)
	return s, o, d, port, id, bridge
}

func TestMirrorInventoryKeepsIndependentAuthorityPrivateFiltersAndObservationStates(t *testing.T) {
	s, o, d, _, id, bridge := nativeMirrorFixture(t)
	var uuid string
	for key := range o.Rows["Mirror"] {
		uuid = key
	}
	ids := []string{}
	for _, b := range d.Bindings {
		ids = append(ids, b.ManagementID)
	}
	if err := s.SetLocalTunnelObjects(ids); err != nil {
		t.Fatal(err)
	}
	readSnapshot := func() candidate.Snapshot {
		t.Helper()
		x, err := s.CandidateSnapshot(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	if readSnapshot().Topology.MirrorAuthority[id] {
		t.Fatal("Tunnel object grant leaked into Mirror")
	}
	if err := s.SetLocalMirrorObjects(ids); err != nil {
		t.Fatal(err)
	}
	first := readSnapshot()
	if !first.Topology.MirrorSupported || !first.Topology.MirrorAuthority[id] || !first.Topology.MirrorAuthority[bridge] {
		t.Fatal("qualified native Mirror graph denied")
	}
	if err := s.SetLocalMirrorObjects(ids); err != nil || first.Topology.MirrorAuthorityRevision != readSnapshot().Topology.MirrorAuthorityRevision {
		t.Fatal("identical root policy invalidated a review")
	}
	if err := s.SetLocalMirrorObjects(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocalMirrorObjects(ids); err != nil || first.Topology.MirrorAuthorityRevision == readSnapshot().Topology.MirrorAuthorityRevision {
		t.Fatal("grant ABA retained an old review")
	}
	claims := authn.Claims{Capabilities: []string{"inventory.read", "configuration.read", "workspace.write", candidate.MirrorCapability}}
	read := func() map[string]any {
		t.Helper()
		x, err := s.Read(context.Background(), "readMirror", map[string]string{"mirror_id": id}, nil, claims)
		if err != nil {
			t.Fatal(err)
		}
		return x.(map[string]any)
	}
	item := read()
	if item["resource_kind"] != "mirror" || item["native_mirror"].(map[string]any)["execution"].(map[string]any)["available"] != true {
		t.Fatal("native Mirror resource or independent execution projection missing")
	}
	stat := item["fields"].(map[string]any)["statistics"].(map[string]any)
	if stat["value"] != nil || stat["availability"] != "unknown" {
		t.Fatal("missing counters became zero")
	}
	row := o.Rows["Mirror"][uuid]
	row.Configuration["filter"] = []any{"private-expression-must-not-leak"}
	s.install(o, d)
	blob, _ := json.Marshal(read())
	if strings.Contains(string(blob), "private-expression-must-not-leak") || !strings.Contains(string(blob), "MIRROR_ADVANCED_FILTER_UNQUALIFIED") {
		t.Fatal("private advanced filter leaked or was adopted")
	}
	row.Configuration["filter"] = []any{}
	s.install(o, d)
	delete(row.Configuration, "select_vlan")
	s.install(o, d)
	unknown := read()["native_mirror"].(map[string]any)
	if unknown["configuration_availability"] != "unknown" || unknown["configuration"] != nil || unknown["execution"].(map[string]any)["reason"] != "MIRROR_CONFIGURATION_UNKNOWN" {
		t.Fatal("missing native fields became a known or merely unsupported configuration")
	}
	row.Configuration["select_vlan"] = []any{}
	delete(o.Rows, "Mirror")
	s.install(o, d)
	if snapshot := readSnapshot(); snapshot.Topology.MirrorSupported || snapshot.Topology.MirrorAuthority[bridge] {
		t.Fatal("unobserved Mirror table became a qualified empty graph")
	}
	o.Rows["Mirror"] = map[string]Row{uuid: row}
	s.install(o, d)
	s.now = func() time.Time { return o.Evidence.ObservedAt.Add(FreshFor + time.Second) }
	if !strings.Contains(string(mustJSON(t, read())), "MIRROR_OBSERVATION_STALE") {
		t.Fatal("stale observation retained Mirror editing")
	}
	if _, err := s.Read(context.Background(), "listMirrors", nil, nil, authn.Claims{Capabilities: []string{"inventory.read"}}); err == nil {
		t.Fatal("Mirror selectors leaked without configuration permission")
	}
}

func TestMirrorIncompleteTopologyAndPortChoicesCloseEditing(t *testing.T) {
	for _, scenario := range []string{"node_budget", "byte_budget", "missing_port_binding", "port_choice_budget"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, d, port, id, bridge := nativeMirrorFixture(t)
			claims := authn.Claims{Capabilities: []string{"inventory.read", "configuration.read", "workspace.write", candidate.MirrorCapability}}
			portRow := o.Rows["Port"][port.OVSUUID]
			ifaceUUID := refs(portRow.Values["interfaces"])[0]
			ifaceRow := o.Rows["Interface"][ifaceUUID]
			switch scenario {
			case "node_budget":
				// Unrelated observed Interfaces can fill the inventory view while
				// the selected Bridge remains independently eligible.
				for n := 0; n < 65; n++ {
					uuid := repository.NewID()
					cfg := candidate.CloneConfiguration(ifaceRow.Values)
					cfg["name"] = "unrelated-" + strconv.Itoa(n)
					o.Rows["Interface"][uuid] = Row{UUID: uuid, Values: cfg, Configuration: candidate.CloneConfiguration(cfg)}
					d.Bindings[Key("Interface", uuid)] = Binding{ManagementID: repository.NewID(), UUID: uuid, Table: "Interface", State: "active"}
				}
			case "byte_budget", "port_choice_budget":
				count := 6
				if scenario == "port_choice_budget" {
					count = 33
				}
				for n := 0; n < count; n++ {
					pid, iid := repository.NewID(), repository.NewID()
					pv, iv := candidate.CloneConfiguration(portRow.Values), candidate.CloneConfiguration(ifaceRow.Values)
					pv["name"], pv["interfaces"], iv["name"] = "bulk"+strconv.Itoa(n), []any{iid}, "bulk"+strconv.Itoa(n)
					o.Rows["Port"][pid] = Row{UUID: pid, Values: pv, Configuration: candidate.CloneConfiguration(pv)}
					o.Rows["Interface"][iid] = Row{UUID: iid, Values: iv, Configuration: candidate.CloneConfiguration(iv)}
					d.Bindings[Key("Port", pid)] = Binding{ManagementID: repository.NewID(), UUID: pid, Table: "Port", State: "active"}
					d.Bindings[Key("Interface", iid)] = Binding{ManagementID: repository.NewID(), UUID: iid, Table: "Interface", State: "active"}
					for bid, row := range o.Rows["Bridge"] {
						if d.Bindings[Key("Bridge", bid)].ManagementID == bridge {
							row.Values["ports"] = append(row.Values["ports"].([]any), pid)
							row.Configuration = candidate.CloneConfiguration(row.Values)
							o.Rows["Bridge"][bid] = row
						}
					}
				}
				if scenario == "byte_budget" {
					for pid, row := range o.Rows["Port"] {
						trunks := []any{}
						for vlan := 1; vlan <= 1700; vlan++ {
							trunks = append(trunks, strconv.Itoa(vlan))
						}
						row.Values["trunks"], row.Values["vlan_mode"] = trunks, []any{"trunk"}
						row.Configuration = candidate.CloneConfiguration(row.Values)
						o.Rows["Port"][pid] = row
					}
				}
			case "missing_port_binding":
				delete(d.Bindings, Key("Port", port.OVSUUID))
			}
			ids := []string{}
			for _, b := range d.Bindings {
				ids = append(ids, b.ManagementID)
			}
			if err := s.SetLocalMirrorObjects(ids); err != nil {
				t.Fatal(err)
			}
			if err := s.SetLocalMirrorCreates([]string{bridge + ":span-new"}); err != nil {
				t.Fatal(err)
			}
			s.install(o, d)
			if scenario == "node_budget" || scenario == "byte_budget" {
				snapshot, err := s.CandidateSnapshot(context.Background(), nil)
				if err != nil || !snapshot.Topology.MirrorAuthority[bridge] {
					t.Fatal("bounded view fixture lost the eligible Bridge", err)
				}
				view, err := s.Read(context.Background(), "readInventoryTopology", nil, nil, claims)
				if err != nil {
					t.Fatal(err)
				}
				data := view.(map[string]any)
				if data["truncated"] != true || data["mirror_editable"] != false || len(mustJSON(t, view)) > 48<<10 || scenario == "byte_budget" && len(d.Bindings) >= 64 {
					t.Fatal("incomplete topology retained Mirror creation", scenario)
				}
				if scenario == "node_budget" {
					scoped, err := s.Read(context.Background(), "readInventoryTopology", nil, url.Values{"domain": {"mirror"}}, claims)
					if err != nil {
						t.Fatal(err)
					}
					selected := scoped.(map[string]any)
					if selected["truncated"] != false || selected["mirror_editable"] != true || len(mustJSON(t, scoped)) > 48<<10 {
						t.Fatal("bounded provisioned Mirror graph could not be reviewed")
					}
					for _, node := range selected["nodes"].([]candidate.TopologyNode) {
						if strings.HasPrefix(node.Name, "unrelated-") {
							t.Fatal("unrelated inventory consumed the scoped selector budget")
						}
					}
					full, err := s.CandidateSnapshot(context.Background(), nil)
					if err != nil || len(full.Topology.Nodes) < 65 {
						t.Fatal("public selector scope discarded private safety dependencies", err)
					}
				}
				return
			}
			item, err := s.Read(context.Background(), "readMirror", map[string]string{"mirror_id": id}, nil, claims)
			if err != nil {
				t.Fatal(err)
			}
			native := item.(map[string]any)["native_mirror"].(map[string]any)
			if native["execution"].(map[string]any)["available"] != false {
				t.Fatal("incomplete current Port choices retained Mirror editing", scenario)
			}
			if scenario == "port_choice_budget" && (native["ports_truncated"] != true || len(native["ports"].([]map[string]any)) != 32 || native["execution"].(map[string]any)["reason"] != "MIRROR_PORT_DETAIL_TRUNCATED") {
				t.Fatal("Port choice budget was hidden or expanded")
			}
		})
	}
}
