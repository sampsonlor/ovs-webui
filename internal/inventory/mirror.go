package inventory

import (
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/tunnel"
)

func (s *Service) SetLocalMirrorObjects(ids []string) error {
	if len(ids) > 256 {
		return apitypes.Fail(422, "INVALID_MIRROR_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_MIRROR_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mirrorAuthorityRevision == "" || Digest(allow) != Digest(s.localMirrorObjects) {
		s.mirrorAuthorityRevision = repository.NewID()
	}
	s.localMirrorObjects = allow
	return nil
}

func (s *Service) SetLocalMirrorCreates(targets []string) error {
	if len(targets) > 128 {
		return apitypes.Fail(422, "INVALID_MIRROR_CREATION_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, target := range targets {
		parts := strings.Split(target, ":")
		if len(parts) != 2 || !apitypes.ManagementID(parts[0]) || !mirror.ValidName(parts[1]) || allow[target] {
			return apitypes.Fail(422, "INVALID_MIRROR_CREATION_AUTHORITY")
		}
		allow[target] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mirrorAuthorityRevision == "" || Digest(allow) != Digest(s.localMirrorCreates) {
		s.mirrorAuthorityRevision = repository.NewID()
	}
	s.localMirrorCreates = allow
	return nil
}

func mirrorConfiguration(v *view, row Row) (mirror.Configuration, error) {
	return mirror.Decode(row.Configuration, func(uuid string) (string, bool) {
		b := v.decision.Bindings[Key("Port", uuid)]
		return b.ManagementID, b.State == "active" && b.Table == "Port" && b.UUID == uuid
	})
}

// Limit the public creation selector to explicitly provisioned Bridges. This
// never changes the complete private snapshot used for validation/execution.
func mirrorCreationSelection(t candidate.TopologySnapshot) map[string]bool {
	selected := map[string]bool{}
	for target, allowed := range t.MirrorCreates {
		id, _, _ := strings.Cut(target, ":")
		bridge, known := t.Nodes[id]
		if !allowed || !known || bridge.Binding.Table != "Bridge" {
			continue
		}
		selected[id] = true
		for _, binding := range bridge.Links {
			selected[binding.ManagementID] = true
			for _, child := range t.Nodes[binding.ManagementID].Links {
				selected[child.ManagementID] = true
			}
		}
		for _, value := range refs(t.Configurations[id]["mirrors"]) {
			for mid, node := range t.Nodes {
				if node.Binding.Table == "Mirror" && node.Binding.OVSUUID == value {
					selected[mid] = true
				}
			}
		}
	}
	return selected
}

func projectMirror(v *view, allow, creates map[string]bool, revision string, out *candidate.Snapshot) {
	t := &out.Topology
	t.MirrorSupported = t.Supported && v.observation.Schema.MirrorConfiguration
	if _, observed := v.observation.Rows["Mirror"]; !observed || len(v.observation.Schema.MirrorDefaults) < 9 {
		t.MirrorSupported = false
	}
	t.MirrorDefaults = candidate.CloneConfiguration(v.observation.Schema.MirrorDefaults)
	t.MirrorAuthorityRevision = revision
	t.MirrorAuthority, t.MirrorCreates = map[string]bool{}, map[string]bool{}
	for key, value := range creates {
		t.MirrorCreates[key] = value
	}
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	other, known := root.Configuration["other_config"].(map[string]any)
	if !known || other["hw-offload"] != nil && other["hw-offload"] != "false" || other["dpdk-init"] != nil && other["dpdk-init"] != "false" || externalControl(root.Values["external_ids"]) {
		t.MirrorSupported = false
	}
	for uuid, row := range v.observation.Rows["Mirror"] {
		b := v.decision.Bindings[Key("Mirror", uuid)]
		if b.State != "active" {
			t.MirrorSupported = false
			continue
		}
		name, _ := row.Values["name"].(string)
		n := candidate.TopologyNode{Binding: candidate.Binding{ManagementID: b.ManagementID, OVSUUID: uuid, Table: "Mirror", Generation: out.Generation}, Name: name, Type: "mirror", Links: []candidate.Binding{}, Digest: candidate.ConfigurationDigest(row.Configuration)}
		if value, err := mirrorConfiguration(v, row); err == nil {
			n.Summary = &candidate.TopologyConfigurationSummary{Mirror: &value}
		}
		t.Nodes[b.ManagementID] = n
		t.Configurations[b.ManagementID] = candidate.CloneConfiguration(row.Configuration)
	}
	for id, n := range t.Nodes {
		t.MirrorAuthority[id] = t.MirrorSupported && allow[id] && mirrorNodeEligible(v, n.Binding.Table, n.Binding.OVSUUID)
	}
	out.Policy = Digest([]any{out.Policy, allow, creates, revision})
	out.Revision = Digest([]any{out.Revision, t.Nodes, t.MirrorAuthority})
}

func mirrorReferencesKnown(v *view, table string, row Row) bool {
	if len(row.Configuration) == 0 || externalControl(row.Values["external_ids"]) {
		return false
	}
	for _, schema := range v.observation.Schema.Tables {
		if schema.Name != table {
			continue
		}
		for _, col := range schema.Columns {
			if len(col.References) == 0 || table == "Bridge" && slices.Contains([]string{"ports", "mirrors"}, col.Name) || table == "Port" && col.Name == "interfaces" || table == "Mirror" && slices.Contains([]string{"select_src_port", "select_dst_port", "output_port"}, col.Name) {
				continue
			}
			switch value := row.Configuration[col.Name].(type) {
			case []any:
				if len(value) != 0 {
					return false
				}
			case map[string]any:
				if len(value) != 0 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func mirrorNodeEligible(v *view, table, uuid string) bool {
	row := v.observation.Rows[table][uuid]
	if v.decision.Bindings[Key(table, uuid)].State != "active" || !mirrorReferencesKnown(v, table, row) {
		return false
	}
	cfg := row.Configuration
	switch table {
	case "Mirror":
		_, err := mirrorConfiguration(v, row)
		bridge, known := parent(v, "Bridge", "mirrors", uuid)
		return err == nil && known && slices.Contains(refs(bridge.Values["mirrors"]), uuid)
	case "Interface":
		typ, known := cfg["type"].(string)
		options, optionsKnown := cfg["options"].(map[string]any)
		if !known || !optionsKnown {
			return false
		}
		if slices.Contains(tunnel.Types, typ) {
			_, err := tunnel.Decode(typ, options)
			return err == nil
		}
		return slices.Contains([]string{"", "system", "internal"}, typ) && len(options) == 0
	case "Port":
		members, known := cfg["interfaces"].([]any)
		if !known || len(members) != 1 || cfg["protected"] != false {
			return false
		}
		for _, key := range []string{"qos", "cvlans", "bond_mode"} {
			values, known := cfg[key].([]any)
			if !known || len(values) != 0 {
				return false
			}
		}
		lacp, known := cfg["lacp"].([]any)
		if !known || len(lacp) > 1 || len(lacp) == 1 && lacp[0] != "off" {
			return false
		}
		mode, known := cfg["vlan_mode"].([]any)
		if !known || len(mode) > 1 || len(mode) == 1 && !slices.Contains([]any{"access", "trunk", "native-tagged", "native-untagged"}, mode[0]) {
			return false
		}
		child, known := members[0].(string)
		if !known || !mirrorNodeEligible(v, "Interface", child) {
			return false
		}
		parent, known := parent(v, "Port", "interfaces", child)
		return known && parent.UUID == uuid
	case "Bridge":
		if cfg["datapath_type"] != "" && cfg["datapath_type"] != "system" || cfg["stp_enable"] != false || cfg["rstp_enable"] != false || cfg["mcast_snooping_enable"] != false {
			return false
		}
		for _, key := range []string{"controller", "sflow", "netflow", "ipfix", "flood_vlans"} {
			values, known := cfg[key].([]any)
			if !known || len(values) != 0 {
				return false
			}
		}
		ports, known := cfg["ports"].([]any)
		if !known || len(ports) == 0 || len(ports) > candidate.MaxTopologyNodes {
			return false
		}
		local := 0
		for _, value := range ports {
			child, known := value.(string)
			if !known || !mirrorNodeEligible(v, "Port", child) {
				return false
			}
			p, known := parent(v, "Bridge", "ports", child)
			if !known || p.UUID != uuid {
				return false
			}
			port := v.observation.Rows["Port"][child]
			if port.Values["name"] == row.Values["name"] {
				members := refs(port.Values["interfaces"])
				if len(members) != 1 || v.observation.Rows["Interface"][members[0]].Values["type"] != "internal" {
					return false
				}
				local++
			}
		}
		mirrors, known := cfg["mirrors"].([]any)
		if !known || len(mirrors) > 32 {
			return false
		}
		for _, value := range mirrors {
			child, known := value.(string)
			if !known || !mirrorNodeEligible(v, "Mirror", child) {
				return false
			}
		}
		return local == 1
	}
	return false
}
