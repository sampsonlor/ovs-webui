package inventory

import (
	"context"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestQinQProjectionPreservesTPIDAndRejectsUnprovenGraph(t *testing.T) {
	for _, scenario := range []string{"default", "802.1q", "802.1ad", "unknown-tpid", "internal", "netdev", "bond", "shared-member", "orphan", "retired-member", "options", "one-header", "default-limit", "missing-datapath"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, d, _ := fixture(t)
			var row, bridge Row
			for _, r := range o.Rows["Port"] {
				row = r
			}
			for _, r := range o.Rows["Bridge"] {
				bridge = r
			}
			ids := refs(row.Values["interfaces"])
			row.Values["interfaces"] = []any{ids[0]}
			config := map[string]any{}
			row.Values["other_config"] = config
			bridge.Values["datapath_type"] = "system"
			root := repository.NewID()
			o.Rows["Open_vSwitch"][root] = Row{UUID: root, Values: map[string]any{"bridges": []any{bridge.UUID}}}
			o.Evidence.Root = root
			dp := repository.NewID()
			o.Rows["Open_vSwitch"][root].Values["other_config"] = map[string]any{"vlan-limit": "2"}
			o.Rows["Open_vSwitch"][root].Values["datapaths"] = map[string]any{"system": dp}
			o.Rows["Datapath"] = map[string]Row{dp: {UUID: dp, Values: map[string]any{"capabilities": map[string]any{"max_vlan_headers": "2"}}}}
			switch scenario {
			case "802.1q", "802.1ad", "unknown-tpid":
				config["qinq-ethtype"] = scenario
			case "one-header":
				o.Rows["Datapath"][dp].Values["capabilities"] = map[string]any{"max_vlan_headers": "1"}
			case "default-limit":
				o.Rows["Open_vSwitch"][root].Values["other_config"] = map[string]any{}
			case "missing-datapath":
				delete(o.Rows["Datapath"], dp)
			case "internal":
				o.Rows["Interface"][ids[0]].Values["type"] = "internal"
			case "netdev":
				bridge.Values["datapath_type"] = "netdev"
			case "bond":
				row.Values["interfaces"] = []any{ids[0], ids[1]}
			case "shared-member":
				id := repository.NewID()
				o.Rows["Port"][id] = Row{UUID: id, Values: map[string]any{"interfaces": []any{ids[0]}}}
			case "orphan":
				o.Rows["Open_vSwitch"][root].Values["bridges"] = []any{}
			case "retired-member":
				delete(d.Bindings, Key("Interface", ids[0]))
			case "options":
				o.Rows["Interface"][ids[0]].Values["options"] = map[string]any{"unproven": "value"}
			}
			b := d.Bindings[Key("Port", row.UUID)]
			binding := candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Port", Generation: d.Generation}
			s.install(o, d)
			read := func() candidate.Port {
				v, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding})
				if err != nil {
					t.Fatal(err)
				}
				return v.Ports[b.ManagementID]
			}
			p := read()
			if p.QinQSupported != (scenario == "default" || scenario == "802.1q" || scenario == "802.1ad") {
				t.Fatal(p)
			}
			initial := Digest(p.QinQ)
			config["unrelated"] = "keep"
			s.install(o, d)
			if Digest(read().QinQ) != initial {
				t.Fatal("unrelated map key invalidated compensation")
			}
			config["qinq-ethtype"] = "external"
			s.install(o, d)
			if Digest(read().QinQ) == initial {
				t.Fatal("TPID dependency not protected")
			}
		})
	}
}
