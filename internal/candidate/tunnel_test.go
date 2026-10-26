package candidate

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/tunnel"
)

func tunnelFixture() (Snapshot, Binding) {
	s, b, _, _ := topologyFixture()
	s.Topology.TunnelSupported = true
	s.Topology.TunnelTypes = append([]string{}, tunnel.Types...)
	s.Topology.TunnelAuthorityRevision = "root-policy-first"
	s.Topology.TunnelAuthority = map[string]bool{}
	s.Topology.TunnelCreates = map[string]bool{b.ManagementID + ":tun0": true}
	for id := range s.Topology.Nodes {
		s.Topology.TunnelAuthority[id] = true
	}
	return s, b
}

func TestStaticTunnelLifecycleUsesNativeGraphAndFreshDeletionRecovery(t *testing.T) {
	for _, typ := range tunnel.Types {
		t.Run(typ, func(t *testing.T) {
			s, b := tunnelFixture()
			remote := "192.0.2.20"
			r := TopologyRequest{Name: "tun0", Tunnel: &tunnel.Request{Type: typ, Options: map[string]*string{"remote_ip": &remote}}}
			i := stageGraph(t, s, b, TunnelCreate, r)
			remote = "192.0.2.99"
			if *i.Topology.Request.Tunnel.Options["remote_ip"] != "192.0.2.20" || i.Topology.Tunnel.After.Options["remote_ip"] != "192.0.2.20" {
				t.Fatal("stage retained caller pointers")
			}
			if caps := Capabilities(Candidate{Intents: []StoredIntent{i}}); !slices.Contains(caps, TunnelCapability) || slices.Contains(caps, "ovs.topology.write") {
				t.Fatal("tunnel inherited core topology authority")
			}
			s = graphAfter(t, s, i)
			proof := i
			AfterImage(&proof)
			if problem := topologyProblem(proof, s); problem != "" {
				t.Fatal("exact tunnel after-image rejected", problem)
			}
			for id, node := range s.Topology.Nodes {
				owned := slices.ContainsFunc(i.Topology.Before, func(n TopologyNode) bool { return n.Binding.ManagementID == id }) || slices.ContainsFunc(i.Topology.After, func(n TopologyNode) bool { return n.Binding.ManagementID == id })
				if owned {
					continue
				}
				changed := node
				changed.Digest = "external-persistent-change"
				s.Topology.Nodes[id] = changed
				if problem := topologyProblem(proof, s); problem != "TUNNEL_DEPENDENCY_CHANGED" {
					t.Fatal("Applied adopted a changed dependency", problem)
				}
				s.Topology.Nodes[id] = node
				break
			}
			var port Binding
			for _, n := range i.Topology.After {
				s.Topology.TunnelAuthority[n.Binding.ManagementID] = true
				if n.Name == "tun0" && n.Binding.Table == "Port" {
					port = n.Binding
				}
			}
			oldIface := i.Topology.Tunnel.Interface
			key := "99"
			edit := stageGraph(t, s, port, TunnelConfigure, TopologyRequest{Tunnel: &tunnel.Request{Options: map[string]*string{"key": &key}}})
			if edit.Topology.Tunnel.Interface != oldIface || edit.Topology.Tunnel.Before.Options["key"] != "" {
				t.Fatal("edit replaced identity or inserted a default")
			}
			s = graphAfter(t, s, edit)
			before := CloneConfiguration(s.Topology.Configurations[oldIface.ManagementID])
			deleted := stageGraph(t, s, port, TunnelDelete, TopologyRequest{})
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
			replacement := deleted.Topology.Replacements[oldIface.OVSUUID]
			if replacement.OVSUUID == oldIface.OVSUUID || replacement.ManagementID == oldIface.ManagementID || Digest(images[replacement.OVSUUID]) != Digest(before) {
				t.Fatal("delete recovery did not preserve exact values with a fresh identity")
			}
			if problem := topologyProblem(deleted, s); problem != "" {
				t.Fatal("captured restoration rejected", problem)
			}
			advertised := s.Topology.TunnelTypes
			s.Topology.TunnelTypes = nil
			if problem := topologyProblem(deleted, s); problem != "TUNNEL_RUNTIME_TYPE_UNAVAILABLE" {
				t.Fatal("restoration guessed unavailable native support", problem)
			}
			s.Topology.TunnelTypes = advertised
			for _, n := range deleted.Topology.After {
				delete(s.Topology.Nodes, n.Binding.ManagementID)
				delete(s.Topology.Configurations, n.Binding.ManagementID)
			}
			for _, n := range deleted.Topology.Restored {
				s.Topology.Nodes[n.Binding.ManagementID] = n
				s.Topology.Configurations[n.Binding.ManagementID] = images[n.Binding.OVSUUID]
			}
			AfterImage(&deleted)
			if problem := topologyProblem(deleted, s); problem != "" {
				t.Fatal("fresh restored identity conflicted with itself", problem)
			}
		})
	}
}

func TestStaticTunnelRequiresIndependentGrantRevisionAndReceiveIdentity(t *testing.T) {
	s, b := tunnelFixture()
	remote := "192.0.2.20"
	r := TopologyRequest{Name: "tun0", Tunnel: &tunnel.Request{Type: "vxlan", Options: map[string]*string{"remote_ip": &remote}}}
	command := Command{Intents: []Intent{{ID: repository.NewID(), Object: b, Operation: TunnelCreate, Topology: &r}}}
	delete(s.Topology.TunnelAuthority, b.ManagementID)
	if _, err := stageTopology(Candidate{}, command, s); err == nil {
		t.Fatal("core root authority substituted for tunnel grant")
	}
	s.Topology.TunnelAuthority[b.ManagementID] = true
	i := stageGraph(t, s, b, TunnelCreate, r)
	s.Topology.TunnelAuthorityRevision = "root-policy-removed-and-restored"
	if topologyProblem(i, s) != "TUNNEL_AUTHORITY_REVISION_CHANGED" {
		t.Fatal("root ABA accepted")
	}
	s.Topology.TunnelAuthorityRevision = i.Topology.TunnelAuthorityRevision
	s = graphAfter(t, s, i)
	s.Topology.TunnelCreates[b.ManagementID+":tun1"] = true
	r.Name = "tun1"
	r.Tunnel.Options["key"] = nil
	if _, err := stageTopology(Candidate{}, Command{Intents: []Intent{{ID: repository.NewID(), Object: b, Operation: TunnelCreate, Topology: &r}}}, s); err == nil || !strings.Contains(err.Error(), "TUNNEL_RECEIVE_IDENTITY_IN_USE") {
		t.Fatal("native unset/zero receive identity duplicate accepted", err)
	}
	unknown := map[string]any{"name": "private", "type": "vxlan", "options": map[string]any{"remote_ip": "192.0.2.20", "psk": "do-not-publish"}}
	data, _ := json.Marshal(TopologySummary("Interface", unknown))
	if strings.Contains(string(data), "do-not-publish") || strings.Contains(string(data), "psk") || !strings.Contains(string(data), "TUNNEL_ADVANCED_OPTIONS_READ_ONLY") {
		t.Fatal("private advanced options leaked or became editable")
	}
}
