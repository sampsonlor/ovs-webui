package candidate

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func topologyFixture() (Snapshot, Binding, Binding, Binding) {
	s := Snapshot{Generation: repository.NewID(), Schema: "schema", Revision: "revision", Topology: TopologySnapshot{Root: repository.NewID(), RootDependency: "root-policy", Supported: true, Capacity: true, Nodes: map[string]TopologyNode{}, Configurations: map[string]map[string]any{}, Authority: map[string]bool{}, Creates: map[string]bool{}, Retired: map[string]bool{}, Defaults: map[string]map[string]any{
		"Port":      {"name": "", "interfaces": []any{}, "bond_mode": []any{}, "lacp": []any{}, "external_ids": map[string]any{}, "other_config": map[string]any{}, "vlan_mode": []any{}, "tag": []any{}, "trunks": []any{}, "cvlans": []any{}},
		"Interface": {"name": "", "type": "", "options": map[string]any{}, "external_ids": map[string]any{}, "ofport_request": []any{}, "mtu_request": []any{}, "ingress_policing_rate": "0", "ingress_policing_burst": "0", "ingress_policing_kpkts_rate": "0", "ingress_policing_kpkts_burst": "0"},
	}}}
	add := func(table, name, typ string, links []Binding) Binding {
		b := Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
		cfg := CloneConfiguration(s.Topology.Defaults[table])
		if s.Topology.Allocations == nil {
			s.Topology.Allocations = map[string]string{}
		}
		if table == "Interface" {
			s.Topology.Allocations[b.ManagementID] = "1"
			if name == "eth2" {
				s.Topology.Allocations[b.ManagementID] = "2"
			}
		}
		cfg["name"] = name
		if table == "Interface" {
			cfg["type"] = typ
		}
		col := "interfaces"
		if table == "Bridge" {
			col = "ports"
			cfg["datapath_type"] = "system"
			cfg["controller"] = []any{}
			cfg["stp_enable"] = false
			cfg["rstp_enable"] = false
			cfg["external_ids"] = map[string]any{}
		}
		if table != "Interface" {
			v := []any{}
			for _, b := range links {
				v = append(v, b.OVSUUID)
			}
			cfg[col] = v
		}
		s.Topology.Configurations[b.ManagementID] = cfg
		s.Topology.Nodes[b.ManagementID] = TopologyNode{Binding: b, Name: name, Type: typ, Links: append([]Binding{}, links...), Digest: ConfigurationDigest(cfg)}
		s.Topology.Authority[b.ManagementID] = true
		return b
	}
	f1 := add("Interface", "eth1", "system", nil)
	f2 := add("Interface", "eth2", "system", nil)
	p1 := add("Port", "p1", "", []Binding{f1})
	p2 := add("Port", "p2", "", []Binding{f2})
	bridge := add("Bridge", "br1", "system", []Binding{p1, p2})
	add("Bridge", "br2", "system", nil)
	for _, name := range []string{"new1", "bond0", "eth3", "eth4", "eth1", "eth2"} {
		s.Topology.Creates[bridge.ManagementID+":"+name] = true
	}
	return s, bridge, p1, p2
}
func stageGraph(t *testing.T, s Snapshot, b Binding, op string, r TopologyRequest) StoredIntent {
	t.Helper()
	c, err := stageTopology(Candidate{}, Command{Intents: []Intent{{ID: repository.NewID(), Object: b, Operation: op, Topology: &r}}}, s)
	if err != nil {
		t.Fatal(err)
	}
	return c.Intents[0]
}

func TestTopologySplitClearsBondOnlySettingsAndPreservesUnknownMap(t *testing.T) {
	s, _, p1, p2 := topologyFixture()
	i := stageGraph(t, s, p1, "bond.members.set", TopologyRequest{Sources: []Binding{p2}, Members: []Binding{s.Topology.Nodes[p1.ManagementID].Links[0], s.Topology.Nodes[p2.ManagementID].Links[0]}})
	s = graphAfter(t, s, i)
	cfg := s.Topology.Configurations[p1.ManagementID]
	cfg["lacp"] = []any{"active"}
	cfg["bond_mode"] = []any{"balance-tcp"}
	cfg["other_config"] = map[string]any{"lacp-fallback-ab": "true", "synthetic-unknown": "keep"}
	n := s.Topology.Nodes[p1.ManagementID]
	n.Digest = ConfigurationDigest(cfg)
	s.Topology.Nodes[p1.ManagementID] = n
	i = stageGraph(t, s, p1, "bond.members.set", TopologyRequest{Members: n.Links[:1]})
	images, err := TopologyConfigurations(i, s.Topology, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range i.Topology.After {
		if p.Binding.Table != "Port" {
			continue
		}
		cfg := images[p.Binding.OVSUUID]
		other := cfg["other_config"].(map[string]any)
		if other["synthetic-unknown"] != "keep" || other["lacp-fallback-ab"] != nil || len(cfg["lacp"].([]any)) != 0 || len(cfg["bond_mode"].([]any)) != 0 {
			t.Fatal("split changed unknown configuration or retained Bond-only settings", cfg)
		}
	}
}
func graphAfter(t *testing.T, s Snapshot, i StoredIntent) Snapshot {
	t.Helper()
	cfg, err := TopologyConfigurations(i, s.Topology, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range i.Topology.Before {
		delete(s.Topology.Nodes, n.Binding.ManagementID)
		delete(s.Topology.Configurations, n.Binding.ManagementID)
	}
	for _, n := range i.Topology.After {
		s.Topology.Nodes[n.Binding.ManagementID] = n
		s.Topology.Configurations[n.Binding.ManagementID] = cfg[n.Binding.OVSUUID]
	}
	return s
}
func TestTopologyLifecycleImmutableIdentitiesAndPrivateImages(t *testing.T) {
	for _, op := range []string{"port.create", "bond.create", "port.delete", "bridge.delete-tree", "port.move", "interface.ofport.set", "interface.ofport.clear", "bond.members.set"} {
		t.Run(op, func(t *testing.T) {
			s, b, p1, p2 := topologyFixture()
			object := p1
			r := TopologyRequest{}
			switch op {
			case "port.create":
				object = b
				r = TopologyRequest{Name: "new1", NativeType: "internal"}
			case "bond.create":
				object = b
				r = TopologyRequest{Name: "bond0", NativeType: "system", InterfaceNames: []string{"eth3", "eth4"}}
			case "bridge.delete-tree":
				object = b
			case "port.move":
				for _, n := range s.Topology.Nodes {
					if n.Name == "br2" {
						r.Destination = &n.Binding
					}
				}
			case "interface.ofport.set", "interface.ofport.clear":
				object = s.Topology.Nodes[p1.ManagementID].Links[0]
				if op == "interface.ofport.set" {
					r.Ofport = 23
				}
			case "bond.members.set":
				r.Sources = []Binding{p2}
				r.Members = append(append([]Binding{}, s.Topology.Nodes[p1.ManagementID].Links...), s.Topology.Nodes[p2.ManagementID].Links...)
			}
			// Unknown native configuration is private and retained across changes.
			cfg := s.Topology.Configurations[p1.ManagementID]
			cfg["other_config"] = map[string]any{"synthetic-private": "do-not-publish"}
			n := s.Topology.Nodes[p1.ManagementID]
			n.Digest = ConfigurationDigest(cfg)
			s.Topology.Nodes[p1.ManagementID] = n
			i := stageGraph(t, s, object, op, r)
			data, _ := json.Marshal(i)
			if strings.Contains(string(data), "do-not-publish") {
				t.Fatal("private before-image exposed")
			}
			original := map[string]map[string]any{}
			for _, n := range i.Topology.Before {
				original[n.Binding.OVSUUID] = CloneConfiguration(s.Topology.Configurations[n.Binding.ManagementID])
			}
			s = graphAfter(t, s, i)
			proof := i
			AfterImage(&proof)
			if !Passed(topologyChecks(proof, s)) {
				t.Fatal(topologyChecks(proof, s))
			}
			Reverse(&i)
			if !Passed(topologyChecks(i, s)) {
				t.Fatal(topologyChecks(i, s))
			}
			if _, err := TopologyConfigurations(i, s.Topology, original); err != nil {
				t.Fatal(err)
			}
			for id, b := range i.Topology.Replacements {
				if id == b.OVSUUID {
					t.Fatal("deleted identity resurrected")
				}
			}
		})
	}
}
func TestTopologyRejectsImplicitStealingAndStaleOriginals(t *testing.T) {
	for _, kind := range []string{"implicit-member", "foreign-property", "missing-authority", "missing-name-grant", "binding", "local-port", "changed-image", "root-policy", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			s, b, p1, p2 := topologyFixture()
			r := TopologyRequest{Sources: []Binding{p2}, Members: append(s.Topology.Nodes[p1.ManagementID].Links, s.Topology.Nodes[p2.ManagementID].Links...)}
			op := "bond.members.set"
			object := p1
			switch kind {
			case "implicit-member":
				r.Sources = nil
			case "foreign-property":
				r.Ofport = 4
			case "missing-authority":
				s.Topology.Authority[p2.ManagementID] = false
			case "missing-name-grant":
				object = b
				op = "port.create"
				r = TopologyRequest{Name: "not-granted", NativeType: "internal"}
			case "binding":
				object.OVSUUID = repository.NewID()
			case "local-port":
				n := s.Topology.Nodes[p1.ManagementID]
				n.Name = "br1"
				s.Topology.Nodes[p1.ManagementID] = n
			}
			if kind == "changed-image" || kind == "root-policy" {
				i := stageGraph(t, s, p1, op, r)
				if kind == "changed-image" {
					n := s.Topology.Nodes[p1.ManagementID]
					n.Digest = "external-change"
					s.Topology.Nodes[p1.ManagementID] = n
				} else {
					s.Topology.RootDependency = "changed"
				}
				if topologyProblem(i, s) == "" {
					t.Fatal("stale original accepted")
				}
				return
			}
			cmd := Command{Intents: []Intent{{ID: repository.NewID(), Object: object, Operation: op, Topology: &r}}}
			if kind == "mixed" {
				cmd.Intents = append(cmd.Intents, cmd.Intents[0])
			}
			if _, err := stageTopology(Candidate{}, cmd, s); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
}
