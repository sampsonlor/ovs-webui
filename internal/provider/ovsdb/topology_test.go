package ovsdb

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestTopologyNativePlansPreserveUnknownConfigurationAndGuardForeignReferences(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v, e := executionFixture(t, version)
			root := v.Observation.Rows["Open_vSwitch"][v.Observation.Evidence.Root]
			root.Configuration = map[string]any{"bridges": root.Values["bridges"], "external_ids": root.Values["external_ids"]}
			v.Observation.Rows["Open_vSwitch"][root.UUID] = root
			top := candidate.TopologySnapshot{Root: root.UUID, RootDependency: "root-policy", Nodes: map[string]candidate.TopologyNode{}, Configurations: map[string]map[string]any{}, Defaults: d.public.GraphDefaults}
			before, after := []candidate.TopologyNode{}, []candidate.TopologyNode{}
			var object candidate.Binding
			for _, table := range []string{"Bridge", "Port", "Interface"} {
				for id, row := range v.Observation.Rows[table] {
					row.Configuration = candidate.CloneConfiguration(row.Values)
					delete(row.Configuration, "error")
					if table == "Port" {
						object = e.Candidate.Intents[0].Object
						row.Configuration["other_config"] = map[string]any{"unknown-key": "preserve-private"}
					}
					b := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: table, Generation: v.Candidate.Generation}
					if table == "Port" {
						b = object
					}
					n := candidate.TopologyNode{Binding: b, Name: row.Values["name"].(string), Links: []candidate.Binding{}, Digest: candidate.ConfigurationDigest(row.Configuration)}
					if table == "Interface" {
						n.Type = "internal"
						row.Configuration["type"] = "internal"
						n.Digest = candidate.ConfigurationDigest(row.Configuration)
					}
					top.Nodes[b.ManagementID] = n
					top.Configurations[b.ManagementID] = candidate.CloneConfiguration(row.Configuration)
					v.Observation.Rows[table][id] = row
					before = append(before, n)
				}
			}
			// A narrow configuration operation demonstrates that complete native guards
			for k, n := range before {
				column := "ports"
				if n.Binding.Table == "Port" {
					column = "interfaces"
				}
				if n.Binding.Table != "Interface" {
					for _, uuid := range nativeRefs(top.Configurations[n.Binding.ManagementID][column]) {
						for _, child := range before {
							if child.Binding.OVSUUID == uuid {
								n.Links = append(n.Links, child.Binding)
							}
						}
					}
				}
				before[k] = n
				top.Nodes[n.Binding.ManagementID] = n
			}
			// coexist with single-column updates and private restoration images.
			object = before[2].Binding
			cfg := candidate.CloneConfiguration(top.Configurations[object.ManagementID])
			cfg["ofport_request"] = []any{"23"}
			for _, n := range before {
				if n.Binding == object {
					n.Digest = candidate.ConfigurationDigest(cfg)
				}
				after = append(after, n)
			}
			i := candidate.StoredIntent{ID: repository.NewID(), Object: object, Operation: "interface.ofport.set", Topology: &candidate.TopologyChange{Root: root.UUID, RootDependency: "root-policy", Before: before, After: after, Request: candidate.TopologyRequest{Ofport: 23}}}
			e.Candidate.Intents = []candidate.StoredIntent{i}
			v.Candidate.Topology = top
			p, err := compileTopologyExecution(repository.NewID(), strings.Repeat("a", 64), e, v, d, nil)
			if err != nil {
				t.Fatal(err)
			}
			var plan nativePlan
			if json.Unmarshal(p.Native, &plan) != nil {
				t.Fatal("plan")
			}
			updates := 0
			mirrorGuard := false
			for _, op := range plan.Operations {
				if op["op"] == "update" {
					updates++
					row := op["row"].(map[string]any)
					if op["table"] != "Interface" || len(row) != 1 || row["ofport_request"] == nil {
						t.Fatal("unrelated configuration overwritten", op)
					}
				}
				if op["op"] == "wait" && op["table"] == "Mirror" {
					mirrorGuard = true
				}
			}
			if updates != 1 || !mirrorGuard || !strings.Contains(string(p.Native), "preserve-private") {
				t.Fatal("missing native dependency/private image")
			}
			public, _ := json.Marshal(e.Candidate)
			if strings.Contains(string(public), "preserve-private") {
				t.Fatal("private image in public Candidate")
			}
		})
	}
}
