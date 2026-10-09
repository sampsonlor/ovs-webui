package inventory

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"slices"
	"strings"
)

// Root-owned grants are independent of existing VLAN, Bond, MTU and policing
// authority. Every existing object in the touched read/write graph needs a grant.
func (s *Service) SetLocalTopologyObjects(ids []string) error {
	if len(ids) > 256 {
		return apitypes.Fail(422, "INVALID_TOPOLOGY_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_TOPOLOGY_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localTopology = allow
	return nil
}
func (s *Service) SetLocalTopologyCreates(targets []string) error {
	if len(targets) > 128 {
		return apitypes.Fail(422, "INVALID_TOPOLOGY_CREATION_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, target := range targets {
		v := strings.Split(target, ":")
		if len(v) != 2 || !apitypes.ManagementID(v[0]) || !candidate.ValidBridgeName(v[1]) || allow[target] {
			return apitypes.Fail(422, "INVALID_TOPOLOGY_CREATION_AUTHORITY")
		}
		allow[target] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localTopologyCreates = allow
	return nil
}
func projectTopology(v *view, allow, creates map[string]bool, out *candidate.Snapshot) {
	t := candidate.TopologySnapshot{Root: v.observation.Evidence.Root, Supported: v.observation.Schema.BridgeCreation && v.observation.Schema.InternalPortCreation, Nodes: map[string]candidate.TopologyNode{}, Configurations: map[string]map[string]any{}, Defaults: v.observation.Schema.GraphDefaults, Authority: map[string]bool{}, Creates: map[string]bool{}, Retired: map[string]bool{}}
	if len(t.Defaults) != 2 {
		t.Supported = false
	}
	t.Allocations = map[string]string{}
	root := v.observation.Rows["Open_vSwitch"][t.Root]
	rootConfig := candidate.CloneConfiguration(root.Configuration)
	delete(rootConfig, "bridges")
	t.RootDependency = candidate.ConfigurationDigest(rootConfig)
	if len(root.Configuration) == 0 || externalControl(root.Values["external_ids"]) {
		t.Supported = false
	}
	for id, b := range v.decision.Retired {
		t.Retired[id] = b
	}
	for id, b := range creates {
		t.Creates[id] = b
	}
	for _, table := range []string{"Bridge", "Port", "Interface"} {
		for uuid, row := range v.observation.Rows[table] {
			binding := v.decision.Bindings[Key(table, uuid)]
			if binding.State != "active" {
				t.Supported = false
				continue
			}
			b := candidate.Binding{ManagementID: binding.ManagementID, OVSUUID: uuid, Table: table, Generation: out.Generation}
			name, _ := row.Values["name"].(string)
			typ, _ := row.Values["type"].(string)
			if table == "Bridge" {
				typ, _ = row.Values["datapath_type"].(string)
			}
			n := candidate.TopologyNode{Binding: b, Name: name, Type: typ, Links: []candidate.Binding{}, Digest: candidate.ConfigurationDigest(row.Configuration)}
			col, child := "ports", "Port"
			if table == "Port" {
				col, child = "interfaces", "Interface"
			}
			if table != "Interface" {
				for _, id := range refs(row.Values[col]) {
					link := v.decision.Bindings[Key(child, id)]
					if link.State != "active" {
						t.Supported = false
						continue
					}
					n.Links = append(n.Links, candidate.Binding{ManagementID: link.ManagementID, OVSUUID: id, Table: child, Generation: out.Generation})
				}
			}
			t.Nodes[b.ManagementID] = n
			n.Summary = candidate.TopologySummary(table, row.Configuration)
			t.Nodes[b.ManagementID] = n
			if table == "Interface" {
				if a, ok := row.Values["ofport"].([]any); ok && len(a) == 1 {
					if value, ok := a[0].(string); ok {
						t.Allocations[b.ManagementID] = value
					}
				}
			}
			t.Configurations[b.ManagementID] = candidate.CloneConfiguration(row.Configuration)
			t.Authority[b.ManagementID] = allow[b.ManagementID] && !externalControl(row.Values["external_ids"]) && len(row.Configuration) != 0
			// Configuration referencing other managed domains cannot be deleted and
			// reconstructed safely by the core graph service. Keep it observable.
			for _, schema := range v.observation.Schema.Tables {
				if schema.Name != table {
					continue
				}
				for _, c := range schema.Columns {
					if len(c.References) == 0 || table == "Bridge" && c.Name == "ports" || table == "Port" && c.Name == "interfaces" {
						continue
					}
					value := row.Configuration[c.Name]
					if a, ok := value.([]any); ok && len(a) == 0 {
						continue
					}
					if m, ok := value.(map[string]any); ok && len(m) == 0 {
						continue
					}
					if value != nil {
						t.Authority[b.ManagementID] = false
					}
				}
			}
		}
	}
	// Existing unsupported device types are read-only; never activate DPDK,
	// tunnels, offload or provider-specific host provisioning through graph CRUD.
	for id, n := range t.Nodes {
		if n.Binding.Table == "Interface" && !slices.Contains([]string{"", "system", "internal", "patch"}, n.Type) {
			t.Authority[id] = false
		}
	}
	count := 0
	for _, rows := range v.observation.Rows {
		count += len(rows)
	}
	data, err := json.Marshal(v.observation.Rows)
	t.Capacity = err == nil && count+candidate.MaxTopologyNodes <= MaxRows && len(data)+candidate.MaxTopologyNodes*MaxRowBytes <= MaxSnapshotBytes
	out.Topology = t
	out.Policy = Digest([]any{out.Policy, allow, creates})
	out.Revision = Digest([]any{out.Revision, t.RootDependency, t.Nodes, t.Authority})
}
