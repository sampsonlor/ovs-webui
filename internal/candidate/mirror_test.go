package candidate

import (
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func mirrorFixture() (Snapshot, Binding, Binding, Binding) {
	s, bridge, source, output := topologyFixture()
	s.Topology.MirrorSupported = true
	s.Topology.MirrorAuthorityRevision = "explicit-mirror-authority"
	s.Topology.MirrorAuthority = map[string]bool{}
	s.Topology.MirrorCreates = map[string]bool{bridge.ManagementID + ":span-1": true}
	s.Topology.MirrorDefaults = map[string]any{"name": "", "select_all": false, "select_src_port": []any{}, "select_dst_port": []any{}, "select_vlan": []any{}, "output_port": []any{}, "output_vlan": []any{}, "snaplen": []any{}, "filter": []any{}, "external_ids": map[string]any{}}
	for id, n := range s.Topology.Nodes {
		s.Topology.MirrorAuthority[id] = true
		if n.Binding.Table == "Bridge" {
			s.Topology.Configurations[id]["mirrors"] = []any{}
			n.Digest = ConfigurationDigest(s.Topology.Configurations[id])
			s.Topology.Nodes[id] = n
		}
	}
	return s, bridge, source, output
}

func mirrorConfig(source, output Binding) mirror.Configuration {
	return mirror.Configuration{SourcePorts: []string{source.ManagementID}, DestinationPorts: []string{}, VLANs: []string{}, OutputPort: &output.ManagementID}
}

func TestMirrorLifecycleSealsNativeSelectorsAndFreshRecoveryReferences(t *testing.T) {
	s, bridge, source, output := mirrorFixture()
	config := mirrorConfig(source, output)
	created := stageGraph(t, s, bridge, MirrorCreate, TopologyRequest{Name: "span-1", Mirror: &config})
	config.SourcePorts[0] = output.ManagementID
	if created.Topology.Mirror.After.SourcePorts[0] != source.ManagementID || created.Topology.Request.Mirror.SourcePorts[0] != source.ManagementID {
		t.Fatal("Mirror original retained mutable caller aliases")
	}
	if caps := Capabilities(Candidate{Intents: []StoredIntent{created}}); !slices.Contains(caps, MirrorCapability) || slices.Contains(caps, "ovs.topology.write") || slices.Contains(caps, TunnelCapability) {
		t.Fatal("Mirror inherited another domain's write authority")
	}
	s = graphAfter(t, s, created)
	object := created.Topology.Mirror.Object
	proof := created
	AfterImage(&proof)
	if problem := topologyProblem(proof, s); problem != "" {
		t.Fatal("exact native Mirror image rejected", problem)
	}
	s.Topology.MirrorAuthority[object.ManagementID] = true // fixture-only explicit fresh-ID provision
	value := "200"
	config = mirror.Configuration{SourcePorts: []string{}, DestinationPorts: []string{source.ManagementID}, VLANs: []string{"100"}, OutputVLAN: &value}
	edit := stageGraph(t, s, object, MirrorConfigure, TopologyRequest{Mirror: &config})
	if edit.Topology.Mirror.Object != object || edit.Topology.Mirror.Before.OutputPort == nil || *edit.Topology.Mirror.After.OutputVLAN != "200" {
		t.Fatal("configuration replaced identity, direction or native output")
	}
	s = graphAfter(t, s, edit)
	private := s.Topology.Configurations[object.ManagementID]
	private["external_ids"] = map[string]any{"foreign": "preserve-native-marker"}
	n := s.Topology.Nodes[object.ManagementID]
	n.Digest = ConfigurationDigest(private)
	s.Topology.Nodes[object.ManagementID] = n
	deleted := stageGraph(t, s, object, MirrorDelete, TopologyRequest{})
	checkpoint := map[string]map[string]any{}
	for _, n := range deleted.Topology.Before {
		checkpoint[n.Binding.OVSUUID] = CloneConfiguration(s.Topology.Configurations[n.Binding.ManagementID])
	}
	s = graphAfter(t, s, deleted)
	Reverse(&deleted)
	images, err := TopologyConfigurations(deleted, s.Topology, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	replacement := deleted.Topology.Replacements[object.OVSUUID]
	if replacement.OVSUUID == object.OVSUUID || replacement.ManagementID == object.ManagementID || replacement.Table != "Mirror" || Digest(images[replacement.OVSUUID]) != Digest(private) {
		t.Fatal("Mirror recovery did not preserve exact native image with fresh identity")
	}
	refs := images[bridge.OVSUUID]["mirrors"].([]any)
	if len(refs) != 1 || refs[0] != replacement.OVSUUID {
		t.Fatal("Bridge restoration referenced the retired Mirror UUID")
	}
	if problem := topologyProblem(deleted, s); problem != "" {
		t.Fatal("sealed native Mirror recovery rejected", problem)
	}
	for id, n := range s.Topology.Nodes {
		if slices.ContainsFunc(deleted.Topology.Before, func(p TopologyNode) bool { return p.Binding == n.Binding }) {
			continue
		}
		n.Digest = "foreign-changed-dependency"
		s.Topology.Nodes[id] = n
		if problem := topologyProblem(deleted, s); problem != "MIRROR_DEPENDENCY_CHANGED" {
			t.Fatal("recovery adopted a changed private read dependency", problem)
		}
		break
	}
}

func TestMirrorRequiresIndependentAuthorityRevisionAndExplicitGraph(t *testing.T) {
	for _, scenario := range []string{"root-grant", "grant-aba", "other-bridge", "mixed-intent", "creation-name", "unproven-defaults"} {
		t.Run(scenario, func(t *testing.T) {
			s, bridge, source, output := mirrorFixture()
			config := mirrorConfig(source, output)
			r := TopologyRequest{Name: "span-1", Mirror: &config}
			switch scenario {
			case "root-grant":
				delete(s.Topology.MirrorAuthority, output.ManagementID)
			case "other-bridge":
				for _, n := range s.Topology.Nodes {
					if n.Name == "br2" {
						bridge = n.Binding
						s.Topology.MirrorCreates[bridge.ManagementID+":span-1"] = true
					}
				}
			case "mixed-intent":
				r.Ofport = 4
			case "creation-name":
				r.Name = "ungranted-name"
			case "unproven-defaults":
				s.Topology.MirrorDefaults = nil
			case "grant-aba":
				i := stageGraph(t, s, bridge, MirrorCreate, r)
				s.Topology.MirrorAuthorityRevision = repository.NewID()
				if problem := topologyProblem(i, s); problem != "MIRROR_AUTHORITY_REVISION_CHANGED" {
					t.Fatal("grant ABA adopted a new authority policy", problem)
				}
				return
			}
			_, err := stageTopology(Candidate{}, Command{Intents: []Intent{{ID: repository.NewID(), Object: bridge, Operation: MirrorCreate, Topology: &r}}}, s)
			if err == nil {
				t.Fatal("unproven Mirror command admitted")
			}
			if scenario == "root-grant" && !strings.Contains(err.Error(), "TOPOLOGY_AUTHORITY_REQUIRED") {
				t.Fatal(err)
			}
		})
	}
}
