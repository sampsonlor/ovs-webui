package ovsdb

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/tunnel"
)

func TestStaticTunnelDiscoveryRejectsRestrictedNativeTypeOrOptionMaps(t *testing.T) {
	data, err := os.ReadFile("testdata/vswitch-3.3.9.ovsschema")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, column, wire string }{
		{"type-enumerated", "type", `{"type":{"key":{"type":"string","enum":["set",["internal","vxlan"]]}}}`},
		{"type-immutable", "type", `{"type":"string","mutable":false}`},
		{"type-ephemeral", "type", `{"type":"string","ephemeral":true}`},
		{"option-enumerated", "options", `{"type":{"key":{"type":"string","enum":["set",["remote_ip"]]},"value":"string","min":0,"max":"unlimited"}}`},
		{"option-limited", "options", `{"type":{"key":"string","value":"string","min":0,"max":3}}`},
		{"option-immutable", "options", `{"type":{"key":"string","value":"string","min":0,"max":"unlimited"},"mutable":false}`},
		{"option-ephemeral", "options", `{"type":{"key":"string","value":"string","min":0,"max":"unlimited"},"ephemeral":true}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			var column map[string]any
			if err := json.Unmarshal([]byte(row.wire), &column); err != nil {
				t.Fatal(err)
			}
			columns := raw["tables"].(map[string]any)["Interface"].(map[string]any)["columns"].(map[string]any)
			columns[row.column] = column
			encoded, _ := json.Marshal(raw)
			d, err := discover(encoded)
			if err == nil && d.public.TunnelConfiguration {
				t.Fatal("restricted schema admitted static tunnel writes")
			}
		})
	}
}

func TestStaticTunnelNativePlanGuardsReceiveIdentityAndPreservesPrivateImages(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v := treeFenceFixture(t, version)
			if !d.public.TunnelConfiguration {
				t.Fatal("upstream schema did not qualify")
			}
			topo := candidate.TopologySnapshot{Root: v.Observation.Evidence.Root, RootDependency: "captured", Supported: true, Capacity: true, TunnelSupported: true, TunnelTypes: tunnel.Types, TunnelAuthorityRevision: "root-policy", TunnelAuthority: map[string]bool{}, TunnelCreates: map[string]bool{}, Nodes: map[string]candidate.TopologyNode{}, Configurations: map[string]map[string]any{}, Defaults: d.public.GraphDefaults, Retired: map[string]bool{}}
			bindings := map[string]candidate.Binding{}
			for _, table := range []string{"Bridge", "Port", "Interface"} {
				for id := range v.Observation.Rows[table] {
					bindings[id] = candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: table, Generation: v.Candidate.Generation}
				}
			}
			var bridge candidate.Binding
			for _, table := range []string{"Bridge", "Port", "Interface"} {
				for id, row := range v.Observation.Rows[table] {
					b := bindings[id]
					cfg := candidate.CloneConfiguration(row.Configuration)
					typ, _ := cfg["type"].(string)
					links := []candidate.Binding{}
					column := "interfaces"
					if table == "Bridge" {
						bridge, typ, column = b, "system", "ports"
						cfg["datapath_type"], cfg["stp_enable"], cfg["rstp_enable"] = "system", false, false
					}
					if table != "Interface" {
						for _, child := range cfg[column].([]any) {
							links = append(links, bindings[child.(string)])
						}
					}
					if table == "Interface" {
						typ, cfg["type"], cfg["options"] = "vxlan", "vxlan", map[string]any{"remote_ip": "192.0.2.20"}
					}
					row.Configuration = cfg
					v.Observation.Rows[table][id] = row
					topo.Nodes[b.ManagementID] = candidate.TopologyNode{Binding: b, Name: cfg["name"].(string), Type: typ, Links: links, Digest: candidate.ConfigurationDigest(cfg)}
					topo.Configurations[b.ManagementID] = cfg
					topo.TunnelAuthority[b.ManagementID] = true
				}
			}
			topo.TunnelCreates[bridge.ManagementID+":tun-new"] = true
			v.Candidate.Topology = topo
			root := v.Observation.Rows["Open_vSwitch"][topo.Root]
			root.Values["iface_types"] = []any{"vxlan", "geneve", "gre"}
			v.Observation.Rows["Open_vSwitch"][root.UUID] = root
			remote := "198.51.100.20"
			env := candidate.Envelope{Owner: repository.NewID(), Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{}}}
			command := candidate.Command{Operation: "stage", Intents: []candidate.Intent{{ID: repository.NewID(), Object: bridge, Operation: candidate.TunnelCreate, Topology: &candidate.TopologyRequest{Name: "tun-new", Tunnel: &tunnel.Request{Type: "vxlan", Options: map[string]*string{"remote_ip": &remote}}}}}}
			env, err := candidate.Prepare(env, command, v.Candidate)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := compileTopologyExecution(repository.NewID(), strings.Repeat("b", 64), env, v, d, nil)
			if err != nil {
				t.Fatal(err)
			}
			var native nativePlan
			if json.Unmarshal(plan.Native, &native) != nil {
				t.Fatal("invalid native plan")
			}
			whole, types := map[string]bool{}, false
			for _, op := range native.Operations {
				if op["op"] != "wait" {
					continue
				}
				if where, ok := op["where"].([]any); ok && len(where) == 0 {
					whole[op["table"].(string)] = true
				}
				if columns, ok := op["columns"].([]any); ok && slices.Contains(columns, any("iface_types")) {
					types = true
				}
			}
			if len(whole) != 4 || !types || native.Operations[len(native.Operations)-1]["durable"] != true {
				t.Fatal("whole native graph/type/durable guard missing", whole, types)
			}
		})
	}
}
