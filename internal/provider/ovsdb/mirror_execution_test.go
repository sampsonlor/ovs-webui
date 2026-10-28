package ovsdb

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestMirrorNativePlanFencesEverySessionAndExactIncomingReferences(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, v := treeFenceFixture(t, version)
			v.Observation.Rows["Mirror"] = map[string]inventory.Row{}
			topo := candidate.TopologySnapshot{Root: v.Observation.Evidence.Root, RootDependency: "captured", Supported: true, Capacity: true, MirrorSupported: true, MirrorAuthorityRevision: "root-policy", MirrorAuthority: map[string]bool{}, MirrorCreates: map[string]bool{}, MirrorDefaults: d.public.MirrorDefaults, Nodes: map[string]candidate.TopologyNode{}, Configurations: map[string]map[string]any{}, Defaults: d.public.GraphDefaults, Retired: map[string]bool{}}
			bindings := map[string]candidate.Binding{}
			for _, table := range []string{"Bridge", "Port", "Interface"} {
				for id := range v.Observation.Rows[table] {
					bindings[id] = candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: table, Generation: v.Candidate.Generation}
				}
			}
			var bridge, port candidate.Binding
			for _, table := range []string{"Bridge", "Port", "Interface"} {
				for id, row := range v.Observation.Rows[table] {
					b, cfg := bindings[id], candidate.CloneConfiguration(row.Configuration)
					typ, links := "internal", []candidate.Binding{}
					switch table {
					case "Bridge":
						bridge, typ = b, "system"
						cfg["datapath_type"], cfg["stp_enable"], cfg["rstp_enable"] = "system", false, false
						for _, child := range cfg["ports"].([]any) {
							links = append(links, bindings[child.(string)])
						}
					case "Port":
						port = b
						for _, child := range cfg["interfaces"].([]any) {
							links = append(links, bindings[child.(string)])
						}
					case "Interface":
						cfg["type"], cfg["options"] = "internal", map[string]any{}
					}
					row.Configuration = cfg
					v.Observation.Rows[table][id] = row
					topo.Nodes[b.ManagementID] = candidate.TopologyNode{Binding: b, Name: cfg["name"].(string), Type: typ, Links: links, Digest: candidate.ConfigurationDigest(cfg)}
					topo.Configurations[b.ManagementID], topo.MirrorAuthority[b.ManagementID] = cfg, true
				}
			}
			topo.MirrorCreates[bridge.ManagementID+":span-new"] = true
			v.Candidate.Topology = topo
			vlan := "200"
			config := mirror.Configuration{SourcePorts: []string{port.ManagementID}, DestinationPorts: []string{}, VLANs: []string{}, OutputVLAN: &vlan}
			env := candidate.Envelope{Owner: repository.NewID(), Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{}}}
			cmd := candidate.Command{Operation: "stage", Intents: []candidate.Intent{{ID: repository.NewID(), Object: bridge, Operation: candidate.MirrorCreate, Topology: &candidate.TopologyRequest{Name: "span-new", Mirror: &config}}}}
			env, err := candidate.Prepare(env, cmd, v.Candidate)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := compileTopologyExecution(repository.NewID(), strings.Repeat("c", 64), env, v, d, nil)
			if err != nil {
				t.Fatal(err)
			}
			pinned, err := pinMirrorRuntime(plan, MirrorExecutionOptions{}, "synthetic-qualified-source")
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the same pinning used by real preparation. OVS counters
			// and integer predicates must retain exact int64 wire values.
			if !bytes.Contains(pinned.Native, []byte(strconv.FormatInt(math.MaxInt64, 10))) || bytes.Contains(pinned.Native, []byte("9223372036854776000")) {
				t.Fatal("runtime pin rounded the native signed counter bound")
			}
			var native nativePlan
			if json.Unmarshal(pinned.Native, &native) != nil {
				t.Fatal("invalid native plan")
			}
			whole := map[string]bool{}
			for _, op := range native.Operations {
				if op["op"] == "wait" && len(op["where"].([]any)) == 0 {
					whole[op["table"].(string)] = true
					if op["table"] == "Mirror" && slices.Contains(op["columns"].([]any), any("statistics")) {
						t.Fatal("statistics fenced as persistent desired configuration")
					}
				}
			}
			if len(whole) != 5 || !whole["Mirror"] || native.Operations[len(native.Operations)-1]["durable"] != true {
				t.Fatal("five-table fence or durable commit missing", whole)
			}
			if !strings.Contains(string(plan.Native), `"table":"Mirror"`) || !strings.Contains(string(plan.Native), `"mirrors"`) {
				t.Fatal("native Mirror or parent reference missing")
			}
			// Every incoming native Port reference includes other Mirror sessions,
			// even though selectors are not generic TopologyNode.Links.
			uuid := repository.NewID()
			private := candidate.CloneConfiguration(d.public.MirrorDefaults)
			private["name"], private["select_src_port"] = "other-session", []any{port.OVSUUID}
			v.Observation.Rows["Mirror"][uuid] = inventory.Row{UUID: uuid, Configuration: private}
			refs, err := mirrorReferenceOperations(d, []candidate.TopologyNode{topo.Nodes[port.ManagementID]}, v)
			if err != nil {
				t.Fatal(err)
			}
			blob, _ := json.Marshal(refs)
			if !strings.Contains(string(blob), uuid) {
				t.Fatal("incoming Mirror selector was assumed empty")
			}
			if _, err := mirrorFenceOperations(v, d); err != nil {
				t.Fatal(err)
			}
			delete(private, "select_vlan")
			if _, err := mirrorFenceOperations(v, d); err == nil {
				t.Fatal("missing private field became a known empty set")
			}
			delete(v.Observation.Rows, "Mirror")
			if _, err := mirrorFenceOperations(v, d); err == nil {
				t.Fatal("unobserved Mirror table became an empty table")
			}
		})
	}
}
