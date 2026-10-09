package candidate

import (
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"slices"
	"sort"
	"strconv"
)

const MaxTopologyNodes = 32

// These are semantic graph commands. No caller can supply rows, before-images,
// replacement identities, field paths or native transactions.
func IsTopologyOperation(op string) bool {
	return slices.Contains([]string{"port.create", "bond.create", "port.delete", "bridge.delete-tree", "port.move", "bond.members.set", "interface.ofport.set", "interface.ofport.clear", "interface.patch.connect", "interface.patch.disconnect"}, op)
}

type TopologyRequest struct {
	Name           string    `json:"name,omitempty"`
	NativeType     string    `json:"native_type,omitempty"`
	InterfaceNames []string  `json:"interface_names,omitempty"`
	Sources        []Binding `json:"source_ports,omitempty"`
	Destination    *Binding  `json:"destination_bridge,omitempty"`
	Members        []Binding `json:"member_interfaces,omitempty"`
	Peer           *Binding  `json:"peer,omitempty"`
	Ofport         int       `json:"ofport_request,omitempty"`
}
type TopologyNode struct {
	Summary map[string]any `json:"configuration_summary,omitempty"`
	Binding Binding        `json:"binding"`
	Name    string         `json:"name"`
	Type    string         `json:"native_type"`
	Links   []Binding      `json:"links"`
	Digest  string         `json:"configuration_digest"`
}
type TopologyChange struct {
	Allocations    map[string]string `json:"allocations,omitempty"`
	Request        TopologyRequest   `json:"request"`
	Root           string            `json:"root_uuid"`
	RootDependency string            `json:"root_dependency"`
	Before         []TopologyNode    `json:"before"`
	After          []TopologyNode    `json:"after"`
	// A deleted UUID is never resurrected, including when restoring a source Port
	// consumed by an explicitly reviewed Bond membership change.
	Replacements map[string]Binding `json:"replacements"`
	Restored     []TopologyNode     `json:"restored"`
	Observed     bool               `json:"observed,omitempty"`
	Compensating bool               `json:"compensating,omitempty"`
}
type TopologySnapshot struct {
	Allocations          map[string]string
	Root, RootDependency string
	Supported, Capacity  bool
	Nodes                map[string]TopologyNode
	Configurations       map[string]map[string]any
	Defaults             map[string]map[string]any
	Authority            map[string]bool
	Creates              map[string]bool
	Retired              map[string]bool
}

func CloneConfiguration(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	b, _ := json.Marshal(m)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

// Only our own recovery metadata is excluded. Unknown native map keys and
// columns remain part of OCC, and are retained privately for compensation.
func ConfigurationDigest(m map[string]any) string {
	c := CloneConfiguration(m)
	if ids, ok := c["external_ids"].(map[string]any); ok {
		delete(ids, BridgeCreationMarker)
		delete(ids, "ovs-webui.commit")
		delete(ids, "ovs-webui.vlan-commit")
		delete(ids, "ovs-webui.bridge-commit")
	}
	for _, v := range c {
		if a, ok := v.([]any); ok {
			sort.Slice(a, func(i, j int) bool { return Digest(a[i]) < Digest(a[j]) })
		}
	}
	return Digest(c)
}
func topologyNodes(m map[string]TopologyNode) []TopologyNode {
	out := []TopologyNode{}
	for _, n := range m {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b TopologyNode) int {
		if a.Binding.ManagementID < b.Binding.ManagementID {
			return -1
		}
		if a.Binding.ManagementID > b.Binding.ManagementID {
			return 1
		}
		return 0
	})
	return out
}
func topologyBindings(nodes []TopologyNode) []Binding {
	out := []Binding{}
	for _, n := range nodes {
		out = append(out, n.Binding)
	}
	return out
}
func TopologyCreations(i StoredIntent, restoration bool) []Binding {
	if i.Topology == nil {
		return nil
	}
	out := []Binding{}
	if restoration {
		for _, n := range i.Topology.Before {
			if b, ok := i.Topology.Replacements[n.Binding.OVSUUID]; ok {
				out = append(out, b)
			}
		}
		return out
	}
	for _, n := range i.Topology.After {
		if !slices.ContainsFunc(i.Topology.Before, func(p TopologyNode) bool { return p.Binding == n.Binding }) {
			out = append(out, n.Binding)
		}
	}
	return out
}
func TopologyBindings(i StoredIntent) []Binding {
	if i.Topology == nil {
		return nil
	}
	out := topologyBindings(i.Topology.Before)
	for _, n := range append(append([]TopologyNode{}, i.Topology.After...), i.Topology.Restored...) {
		if !slices.Contains(out, n.Binding) {
			out = append(out, n.Binding)
		}
	}
	return out
}
func topologyProblem(i StoredIntent, s Snapshot) string {
	g := i.Topology
	t := s.Topology
	if !IsTopologyOperation(i.Operation) || g == nil || len(g.Before) == 0 || len(g.Before) > MaxTopologyNodes || len(g.After) > MaxTopologyNodes {
		return "INVALID_TOPOLOGY_INTENT"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if i.Object.Generation != s.Generation || g.Root != t.Root {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	if !t.Supported {
		return "TOPOLOGY_SCHEMA_UNSUPPORTED"
	}
	if g.RootDependency != t.RootDependency {
		return "TOPOLOGY_ROOT_POLICY_CHANGED"
	}
	before, after := g.Before, g.After
	if g.Compensating {
		before, after = g.After, g.Restored
	}
	if g.Observed {
		before, after = after, before
	}
	for _, n := range before {
		p, ok := t.Nodes[n.Binding.ManagementID]
		if !ok || p.Binding != n.Binding {
			return "OBJECT_BINDING_CHANGED"
		}
		if p.Digest != n.Digest {
			return "TOPOLOGY_CONFIGURATION_CHANGED"
		}
	}
	for id, allocation := range g.Allocations {
		if g.Observed && id == i.Object.ManagementID || g.Compensating && id == i.Object.ManagementID {
			continue
		}
		if t.Allocations[id] != allocation {
			return "OFPORT_ALLOCATION_CHANGED"
		}
	}
	for _, n := range after {
		if slices.ContainsFunc(before, func(p TopologyNode) bool { return p.Binding == n.Binding }) {
			continue
		}
		if g.Observed {
			if _, ok := t.Nodes[n.Binding.ManagementID]; ok {
				return "DELETED_OBJECT_STILL_PRESENT"
			}
			continue
		}
		if _, ok := t.Nodes[n.Binding.ManagementID]; ok || t.Retired[n.Binding.OVSUUID] {
			return "CREATION_IDENTITY_CONSUMED"
		}
		for _, p := range t.Nodes {
			if p.Binding.Table == n.Binding.Table && p.Name == n.Name {
				return "OBJECT_NAME_IN_USE"
			}
		}
	}
	return ""
}
func topologyChecks(i StoredIntent, s Snapshot) []Gate {
	out := []Gate{}
	if problem := topologyProblem(i, s); problem != "" {
		out = append(out, gate(problem, "blocked", i.ID))
	}
	if i.Topology == nil {
		return out
	}
	// A rollback uses the authority sealed at admission; generation, images,
	// root policy, marker and current==our_after are still checked independently.
	if !i.Topology.Compensating && !i.Topology.Observed {
		for _, n := range i.Topology.Before {
			if !s.Topology.Authority[n.Binding.ManagementID] {
				out = append(out, gate("TOPOLOGY_AUTHORITY_REQUIRED", "blocked", i.ID))
				break
			}
		}
		for _, b := range TopologyCreations(i, false) {
			n := slices.IndexFunc(i.Topology.After, func(n TopologyNode) bool { return n.Binding == b })
			bridgeID := ""
			child := b
			for _, p := range i.Topology.After {
				if p.Binding.Table == "Port" && slices.Contains(p.Links, b) {
					child = p.Binding
				}
			}
			for _, p := range i.Topology.After {
				if p.Binding.Table == "Bridge" && slices.Contains(p.Links, child) {
					bridgeID = p.Binding.ManagementID
				}
			}
			if n < 0 || !s.Topology.Creates[bridgeID+":"+i.Topology.After[n].Name] {
				out = append(out, gate("TOPOLOGY_CREATION_GRANT_REQUIRED", "blocked", i.ID))
				break
			}
		}
	}
	out = append(out, gate("HIGH_RISK_TOPOLOGY_SAFE_APPLY_REQUIRED", "allowed", i.ID), gate("MANAGEMENT_PATH_AND_SOLE_UPLINK_REVIEW_REQUIRED", "allowed", i.ID))
	return out
}
func topologyDiff(i StoredIntent, s Snapshot) Diff {
	g := i.Topology
	if g == nil {
		return Diff{Object: i.Object, Field: "native_topology", Operation: i.Operation, IntentID: i.ID, Conflict: true}
	}
	current := []TopologyNode{}
	for _, n := range g.Before {
		if p, ok := s.Topology.Nodes[n.Binding.ManagementID]; ok {
			current = append(current, p)
		}
	}
	return Diff{Object: i.Object, Field: "native_topology", Before: g.Before, After: g.After, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: topologyProblem(i, s) != ""}
}
func stageTopology(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	fail := func(code string) (Candidate, error) { return c, apitypes.Fail(409, code) }
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 {
		return fail("TOPOLOGY_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	r := in.Topology
	t := s.Topology
	if !IsTopologyOperation(in.Operation) || r == nil || !apitypes.ManagementID(in.ID) || Digest(in.Value) != Digest(VLAN{}) || in.Policing != nil || in.MTURequest != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || in.Name != "" || in.VLANID != 0 || len(in.Members) != 0 {
		return fail("INVALID_TOPOLOGY_INTENT")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return fail("GENERATION_RECONCILIATION_REQUIRED")
	}
	if len(c.Intents) == 1 {
		p := c.Intents[0]
		if p.ID != in.ID || p.Object != in.Object || p.Operation != in.Operation || p.Topology == nil {
			return fail("TOPOLOGY_RESTAGE_REQUIRED")
		}
		// Changing a graph draft requires explicit discard and recapture.
		return fail("TOPOLOGY_RESTAGE_REQUIRED")
	}
	n, ok := t.Nodes[in.Object.ManagementID]
	if !ok || n.Binding != in.Object {
		return fail("OBJECT_BINDING_CHANGED")
	}
	before := map[string]TopologyNode{}
	after := map[string]TopologyNode{}
	configs := map[string]map[string]any{}
	var problem string
	allocations := map[string]string{}
	load := func(b Binding) TopologyNode {
		p, exists := t.Nodes[b.ManagementID]
		if !exists || p.Binding != b {
			problem = "OBJECT_BINDING_CHANGED"
			return TopologyNode{}
		}
		before[b.ManagementID] = p
		after[b.ManagementID] = p
		configs[b.ManagementID] = CloneConfiguration(t.Configurations[b.ManagementID])
		return p
	}
	load(in.Object)
	parent := func(child Binding, table string) TopologyNode {
		matches := []TopologyNode{}
		for _, p := range t.Nodes {
			if p.Binding.Table == table && slices.Contains(p.Links, child) {
				matches = append(matches, p)
			}
		}
		if len(matches) != 1 {
			problem = "TOPOLOGY_PARENT_UNPROVEN"
			return TopologyNode{}
		}
		return load(matches[0].Binding)
	}
	checkBridge := func(p TopologyNode) {
		v := t.Configurations[p.Binding.ManagementID]
		if p.Binding.Table != "Bridge" || p.Type != "" && p.Type != "system" || v["stp_enable"] != false || v["rstp_enable"] != false {
			problem = "TOPOLOGY_BRIDGE_POLICY_UNSUPPORTED"
			return
		}
		if refs, ok := v["controller"].([]any); !ok || len(refs) != 0 {
			problem = "EXTERNALLY_CONTROLLED"
		}
	}
	portBridge := func(p TopologyNode) TopologyNode {
		b := parent(p.Binding, "Bridge")
		checkBridge(b)
		if p.Name == b.Name {
			problem = "LOCAL_INTERNAL_PORT_PROTECTED"
		}
		for _, f := range p.Links {
			load(f)
		}
		return b
	}
	setLinks := func(p TopologyNode, links []Binding) {
		p.Links = append([]Binding{}, links...)
		after[p.Binding.ManagementID] = p
		v := []any{}
		for _, b := range links {
			v = append(v, b.OVSUUID)
		}
		column := "interfaces"
		if p.Binding.Table == "Bridge" {
			column = "ports"
		}
		configs[p.Binding.ManagementID][column] = v
	}
	removeLink := func(p TopologyNode, b Binding) {
		setLinks(p, slices.DeleteFunc(append([]Binding{}, p.Links...), func(v Binding) bool { return v == b }))
	}
	add := func(table, name, typ string, links []Binding) TopologyNode {
		b := Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
		p := TopologyNode{Binding: b, Name: name, Type: typ, Links: append([]Binding{}, links...)}
		cfg := CloneConfiguration(t.Defaults[table])
		if len(cfg) == 0 {
			problem = "TOPOLOGY_SCHEMA_UNSUPPORTED"
		}
		cfg["name"] = name
		configs[b.ManagementID] = cfg
		after[b.ManagementID] = p
		if table == "Interface" {
			cfg["type"] = typ
		} else {
			setLinks(p, links)
		}
		return p
	}
	remove := func(p TopologyNode) { delete(after, p.Binding.ManagementID) }
	// Reject properties from other semantic operations, even on the private IPC.
	clean := *r
	switch in.Operation {
	case "port.create", "bond.create":
		clean.Name = ""
		clean.NativeType = ""
		clean.InterfaceNames = nil
		if n.Binding.Table != "Bridge" || !ValidBridgeName(r.Name) || !slices.Contains([]string{"internal", "system"}, r.NativeType) {
			return fail("INVALID_PORT_CREATION")
		}
		checkBridge(n)
		for _, p := range t.Nodes {
			if p.Name == r.Name {
				return fail("OBJECT_NAME_IN_USE")
			}
		}
		if in.Operation == "port.create" && (len(r.InterfaceNames) != 0 && !slices.Equal(r.InterfaceNames, []string{r.Name})) {
			return fail("INVALID_PORT_CREATION")
		}
		names := r.InterfaceNames
		if in.Operation == "port.create" {
			names = []string{r.Name}
		}
		if len(names) < 1 || len(names) > 8 || in.Operation == "bond.create" && (r.NativeType != "system" || len(names) < 2) {
			return fail("INVALID_BOND_MEMBERS")
		}
		members := []Binding{}
		seen := map[string]bool{}
		for _, name := range names {
			if !ValidBridgeName(name) || seen[name] {
				return fail("INVALID_INTERFACE_NAME")
			}
			seen[name] = true
			for _, p := range t.Nodes {
				if p.Name == name {
					return fail("OBJECT_NAME_IN_USE")
				}
			}
			members = append(members, add("Interface", name, r.NativeType, nil).Binding)
		}
		p := add("Port", r.Name, "", members)
		if in.Operation == "bond.create" {
			configs[p.Binding.ManagementID]["bond_mode"] = []any{"active-backup"}
			configs[p.Binding.ManagementID]["lacp"] = []any{"off"}
		}
		setLinks(n, append(append([]Binding{}, n.Links...), p.Binding))
	case "port.delete":
		if n.Binding.Table != "Port" {
			return fail("INVALID_PORT_DELETION")
		}
		b := portBridge(n)
		removeLink(b, n.Binding)
		remove(n)
		for _, f := range n.Links {
			remove(load(f))
		}
	case "bridge.delete-tree":
		if n.Binding.Table != "Bridge" {
			return fail("INVALID_BRIDGE_DELETION")
		}
		checkBridge(n)
		remove(n)
		for _, b := range n.Links {
			p := load(b)
			remove(p)
			for _, f := range p.Links {
				remove(load(f))
			}
		}
	case "port.move":
		clean.Destination = nil
		if n.Binding.Table != "Port" || r.Destination == nil {
			return fail("INVALID_PORT_MOVE")
		}
		from := portBridge(n)
		to := load(*r.Destination)
		checkBridge(to)
		if from.Binding == to.Binding {
			return fail("PORT_ALREADY_ATTACHED")
		}
		removeLink(from, n.Binding)
		setLinks(to, append(append([]Binding{}, to.Links...), n.Binding))
	case "bond.members.set":
		clean.Sources = nil
		clean.Members = nil
		if n.Binding.Table != "Port" || len(r.Members) < 1 || len(r.Members) > 8 || len(r.Sources) > 8 {
			return fail("INVALID_BOND_MEMBERS")
		}
		bridge := portBridge(n)
		available := map[string]Binding{}
		for _, b := range n.Links {
			available[b.ManagementID] = b
		}
		for _, src := range r.Sources {
			if _, exists := before[src.ManagementID]; exists {
				return fail("DUPLICATE_BOND_SOURCE")
			}
			if src == n.Binding {
				return fail("INVALID_BOND_SOURCE")
			}
			p := load(src)
			if p.Binding.Table != "Port" || len(p.Links) != 1 || portBridge(p).Binding != bridge.Binding {
				return fail("BOND_SOURCE_REQUIRES_STANDALONE_SAME_BRIDGE_PORT")
			}
			available[p.Links[0].ManagementID] = p.Links[0]
			for _, col := range []string{"vlan_mode", "tag", "trunks", "cvlans"} {
				if Digest(t.Configurations[src.ManagementID][col]) != Digest(t.Configurations[n.Binding.ManagementID][col]) {
					return fail("BOND_SOURCE_VLAN_MISMATCH")
				}
			}
			remove(p)
		}
		seen := map[string]bool{}
		for _, b := range r.Members {
			if seen[b.ManagementID] || available[b.ManagementID] != b {
				return fail("BOND_MEMBER_SOURCE_REQUIRED")
			}
			seen[b.ManagementID] = true
			f := load(b)
			if f.Type != "" && f.Type != "system" {
				return fail("BOND_MEMBER_TYPE_UNSUPPORTED")
			}
		}
		// Source Ports are consumed only when their Interface is explicitly selected.
		for _, src := range r.Sources {
			if !seen[t.Nodes[src.ManagementID].Links[0].ManagementID] {
				return fail("BOND_SOURCE_NOT_SELECTED")
			}
		}
		links := []Binding{}
		for _, b := range bridge.Links {
			if !slices.Contains(r.Sources, b) {
				links = append(links, b)
			}
		}
		for _, b := range n.Links {
			if !seen[b.ManagementID] {
				f := load(b)
				p := add("Port", f.Name, "", []Binding{b})
				// Split retains the VLAN and all unknown Port configuration. Bond-only
				// settings are cleared explicitly on the new standalone Port.
				cfg := CloneConfiguration(t.Configurations[n.Binding.ManagementID])
				cfg["name"] = f.Name
				cfg["interfaces"] = []any{b.OVSUUID}
				cfg["bond_mode"] = []any{}
				cfg["lacp"] = []any{}
				clearBondFallback(cfg)
				delete(cfg["external_ids"].(map[string]any), BridgeCreationMarker)
				configs[p.Binding.ManagementID] = cfg
				links = append(links, p.Binding)
			}
		}
		setLinks(bridge, links)
		setLinks(n, r.Members)
		if len(r.Members) == 1 {
			cfg := configs[n.Binding.ManagementID]
			cfg["bond_mode"] = []any{}
			cfg["lacp"] = []any{}
			clearBondFallback(cfg)
		}
		if len(r.Members) > 1 {
			cfg := configs[n.Binding.ManagementID]
			if len(cfg["bond_mode"].([]any)) == 0 {
				cfg["bond_mode"] = []any{"active-backup"}
			}
		}
	case "interface.ofport.set", "interface.ofport.clear":
		clean.Ofport = 0
		if n.Binding.Table != "Interface" || in.Operation == "interface.ofport.set" && (r.Ofport < 1 || r.Ofport > 65279) || in.Operation == "interface.ofport.clear" && r.Ofport != 0 {
			return fail("INVALID_OFPORT_REQUEST")
		}
		p := parent(n.Binding, "Port")
		b := portBridge(p)
		for _, pb := range b.Links {
			port := load(pb)
			for _, fb := range port.Links {
				load(fb)
				a, known := t.Allocations[fb.ManagementID]
				if !known || a == "" {
					return fail("OFPORT_ALLOCATION_UNPROVEN")
				}
				allocations[fb.ManagementID] = a
				if fb != n.Binding && r.Ofport != 0 && a == strconv.Itoa(r.Ofport) {
					return fail("OFPORT_ALLOCATION_IN_USE")
				}
			}
		}
		if r.Ofport != 0 {
			for _, port := range b.Links {
				for _, f := range t.Nodes[port.ManagementID].Links {
					if f == n.Binding {
						continue
					}
					v := t.Configurations[f.ManagementID]["ofport_request"]
					if Digest(v) == Digest([]any{strconv.Itoa(r.Ofport)}) {
						return fail("OFPORT_REQUEST_IN_USE")
					}
				}
			}
		}
		v := []any{}
		if r.Ofport != 0 {
			v = append(v, strconv.Itoa(r.Ofport))
		}
		configs[n.Binding.ManagementID]["ofport_request"] = v
	case "interface.patch.connect", "interface.patch.disconnect":
		clean.Peer = nil
		if n.Binding.Table != "Interface" || r.Peer == nil || *r.Peer == n.Binding {
			return fail("INVALID_PATCH_PAIR")
		}
		peer := load(*r.Peer)
		for _, f := range []TopologyNode{n, peer} {
			p := parent(f.Binding, "Port")
			portBridge(p)
			cfg := configs[f.Binding.ManagementID]
			options, _ := cfg["options"].(map[string]any)
			if len(p.Links) != 1 || in.Operation == "interface.patch.connect" && (f.Type != "internal" || len(options) != 0) || in.Operation == "interface.patch.disconnect" && (f.Type != "patch" || len(options) != 1) {
				return fail("PATCH_TYPE_TRANSITION_UNSUPPORTED")
			}
			for _, col := range []string{"mtu_request", "ofport_request"} {
				if a, ok := cfg[col].([]any); !ok || len(a) != 0 {
					return fail("PATCH_CONFIGURATION_DEPENDENCY")
				}
			}
			for _, col := range []string{"ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"} {
				if cfg[col] != "0" {
					return fail("PATCH_CONFIGURATION_DEPENDENCY")
				}
			}
		}
		if in.Operation == "interface.patch.disconnect" && (configs[n.Binding.ManagementID]["options"].(map[string]any)["peer"] != peer.Name || configs[peer.Binding.ManagementID]["options"].(map[string]any)["peer"] != n.Name) {
			return fail("PATCH_PEER_NOT_RECIPROCAL")
		}
		for _, pair := range [][2]TopologyNode{{n, peer}, {peer, n}} {
			f := pair[0]
			cfg := configs[f.Binding.ManagementID]
			f.Type = "internal"
			cfg["options"] = map[string]any{}
			if in.Operation == "interface.patch.connect" {
				f.Type = "patch"
				cfg["options"] = map[string]any{"peer": pair[1].Name}
			}
			cfg["type"] = f.Type
			after[f.Binding.ManagementID] = f
		}
	default:
		return fail("UNSUPPORTED_CONFIGURATION")
	}
	if Digest(clean) != Digest(TopologyRequest{}) {
		return fail("INVALID_TOPOLOGY_PROPERTIES")
	}
	if problem != "" {
		return fail(problem)
	}
	if len(before) > MaxTopologyNodes || len(after) > MaxTopologyNodes || !t.Capacity {
		return fail("TOPOLOGY_BUDGET_EXCEEDED")
	}
	union := len(before)
	for id := range after {
		if _, ok := before[id]; !ok {
			union++
		}
	}
	if union > MaxTopologyNodes {
		return fail("TOPOLOGY_BUDGET_EXCEEDED")
	}
	for id, p := range after {
		if p.Binding.Table == "Port" && len(p.Links) > 1 {
			cfg := configs[id]
			optional := func(v any) *string {
				values, ok := v.([]any)
				if !ok || len(values) != 1 {
					return nil
				}
				s, ok := values[0].(string)
				if !ok {
					return nil
				}
				return &s
			}
			bond := Bond{Mode: optional(cfg["bond_mode"]), LACP: optional(cfg["lacp"])}
			if other, ok := cfg["other_config"].(map[string]any); ok {
				if value, exists := other["lacp-fallback-ab"]; exists {
					s, ok := value.(string)
					if !ok {
						return fail("NATIVE_BOND_SEMANTICS_UNPROVEN")
					}
					bond.Fallback = &s
				}
			}
			if !bondNativeKnown(&bond) {
				return fail("NATIVE_BOND_SEMANTICS_UNPROVEN")
			}
			lacp, mode, fallback := bondModes(&bond)
			if mode == "balance-tcp" && lacp == "off" || fallback && lacp == "off" {
				return fail("LACP_COMPATIBILITY_REQUIRED")
			}
			if mode == "balance-slb" {
				for _, bridge := range after {
					if bridge.Binding.Table == "Bridge" && slices.Contains(bridge.Links, p.Binding) {
						if flood, ok := configs[bridge.Binding.ManagementID]["flood_vlans"].([]any); !ok || len(flood) != 0 {
							return fail("SLB_FLOOD_VLANS_INCOMPATIBLE")
						}
					}
				}
			}
		}
		p.Digest = ConfigurationDigest(configs[id])
		p.Summary = TopologySummary(p.Binding.Table, configs[id])
		after[id] = p
	}
	g := &TopologyChange{Allocations: allocations, Request: *r, Root: t.Root, RootDependency: t.RootDependency, Before: topologyNodes(before), After: topologyNodes(after), Replacements: map[string]Binding{}}
	// Reserve replacements before deletion and compute their complete expected
	// configuration digest without exposing private unknown fields in the draft.
	for id, p := range before {
		if _, exists := after[id]; !exists {
			g.Replacements[p.Binding.OVSUUID] = Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: p.Binding.Table, Generation: s.Generation}
		}
	}
	for id, p := range before {
		p.Links = append([]Binding{}, p.Links...)
		cfg := CloneConfiguration(t.Configurations[id])
		if b, ok := g.Replacements[p.Binding.OVSUUID]; ok {
			p.Binding = b
		}
		for k, b := range p.Links {
			if replacement, ok := g.Replacements[b.OVSUUID]; ok {
				p.Links[k] = replacement
			}
		}
		cfg = RemapConfiguration(cfg, g.Replacements)
		p.Digest = ConfigurationDigest(cfg)
		g.Restored = append(g.Restored, p)
	}
	slices.SortFunc(g.Restored, func(a, b TopologyNode) int {
		return slices.IndexFunc(g.Before, func(n TopologyNode) bool { return n.Name == a.Name && n.Binding.Table == a.Binding.Table }) - slices.IndexFunc(g.Before, func(n TopologyNode) bool { return n.Name == b.Name && n.Binding.Table == b.Binding.Table })
	})
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: t.RootDependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), Topology: g}
	for _, check := range topologyChecks(i, s) {
		if check.State != "allowed" {
			return fail(check.Code)
		}
	}
	c.Intents = []StoredIntent{i}
	c.Generation = &s.Generation
	c.BaseRevision = &s.Revision
	return c, nil
}

func TopologySummary(table string, cfg map[string]any) map[string]any {
	out := map[string]any{}
	columns := []string{"ofport_request", "mtu_request", "ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"}
	if table == "Port" {
		columns = []string{"vlan_mode", "tag", "trunks", "cvlans", "lacp", "bond_mode"}
	} else if table != "Interface" {
		return nil
	}
	for _, col := range columns {
		if v, ok := cfg[col]; ok {
			out[col] = v
		}
	}
	if table == "Port" {
		if other, ok := cfg["other_config"].(map[string]any); ok {
			if v, exists := other["lacp-fallback-ab"]; exists {
				out["lacp-fallback-ab"] = v
			}
		}
	} else if table == "Interface" && cfg["type"] == "patch" {
		if options, ok := cfg["options"].(map[string]any); ok {
			if v, exists := options["peer"]; exists {
				out["patch_peer"] = v
			}
		}
	}
	return CloneConfiguration(out)
}

func clearBondFallback(cfg map[string]any) {
	if other, ok := cfg["other_config"].(map[string]any); ok {
		delete(other, "lacp-fallback-ab")
	}
}

// Rebuild desired configuration from the semantic command and manager-captured
// images. Only the provider's durable private plan retains those images.
func TopologyConfigurations(i StoredIntent, t TopologySnapshot, original map[string]map[string]any) (map[string]map[string]any, error) {
	g := i.Topology
	out := map[string]map[string]any{}
	if g == nil {
		return nil, apitypes.Fail(422, "INVALID_TOPOLOGY_INTENT")
	}
	nodes := g.After
	if g.Compensating {
		nodes = g.Restored
	}
	for _, n := range nodes {
		var cfg map[string]any
		if g.Compensating {
			oldUUID := n.Binding.OVSUUID
			for id, b := range g.Replacements {
				if b == n.Binding {
					oldUUID = id
				}
			}
			cfg = RemapConfiguration(original[oldUUID], g.Replacements)
		} else {
			cfg = CloneConfiguration(t.Configurations[n.Binding.ManagementID])
			if len(cfg) == 0 {
				cfg = CloneConfiguration(t.Defaults[n.Binding.Table])
				if i.Operation == "bond.members.set" && n.Binding.Table == "Port" {
					cfg = CloneConfiguration(t.Configurations[i.Object.ManagementID])
					cfg["bond_mode"] = []any{}
					cfg["lacp"] = []any{}
					clearBondFallback(cfg)
					if ids, ok := cfg["external_ids"].(map[string]any); ok {
						delete(ids, BridgeCreationMarker)
					}
				}
			}
			cfg["name"] = n.Name
			if n.Binding.Table == "Interface" {
				cfg["type"] = n.Type
			}
			if n.Binding.Table == "Port" || n.Binding.Table == "Bridge" {
				links := []any{}
				for _, b := range n.Links {
					links = append(links, b.OVSUUID)
				}
				col := "ports"
				if n.Binding.Table == "Port" {
					col = "interfaces"
				}
				cfg[col] = links
			}
			if i.Operation == "bond.create" && n.Binding.Table == "Port" {
				cfg["bond_mode"] = []any{"active-backup"}
				cfg["lacp"] = []any{"off"}
			}
			if i.Operation == "bond.members.set" && n.Binding == i.Object && len(n.Links) > 1 {
				if v, ok := cfg["bond_mode"].([]any); ok && len(v) == 0 {
					cfg["bond_mode"] = []any{"active-backup"}
				}
			}
			if i.Operation == "bond.members.set" && n.Binding == i.Object && len(n.Links) == 1 {
				cfg["bond_mode"] = []any{}
				cfg["lacp"] = []any{}
				clearBondFallback(cfg)
			}
			if n.Binding == i.Object && (i.Operation == "interface.ofport.set" || i.Operation == "interface.ofport.clear") {
				v := []any{}
				if g.Request.Ofport != 0 {
					v = append(v, strconv.Itoa(g.Request.Ofport))
				}
				cfg["ofport_request"] = v
			}
			if n.Binding.Table == "Interface" && (i.Operation == "interface.patch.connect" || i.Operation == "interface.patch.disconnect") && (n.Binding == i.Object || g.Request.Peer != nil && n.Binding == *g.Request.Peer) {
				cfg["options"] = map[string]any{}
				if i.Operation == "interface.patch.connect" {
					peerName := ""
					for _, p := range nodes {
						if p.Binding.Table == "Interface" && p.Binding != n.Binding && (p.Binding == i.Object || g.Request.Peer != nil && p.Binding == *g.Request.Peer) {
							peerName = p.Name
						}
					}
					cfg["options"] = map[string]any{"peer": peerName}
				}
			}
		}
		if len(cfg) == 0 || ConfigurationDigest(cfg) != n.Digest {
			return nil, apitypes.Fail(409, "TOPOLOGY_PRIVATE_IMAGE_CHANGED")
		}
		out[n.Binding.OVSUUID] = cfg
	}
	return out, nil
}
func RemapConfiguration(cfg map[string]any, replacements map[string]Binding) map[string]any {
	var remap func(any) any
	remap = func(v any) any {
		switch x := v.(type) {
		case string:
			if b, ok := replacements[x]; ok {
				return b.OVSUUID
			}
			return x
		case []any:
			for k, y := range x {
				x[k] = remap(y)
			}
			return x
		case map[string]any:
			for k, y := range x {
				x[k] = remap(y)
			}
			return x
		}
		return v
	}
	out := CloneConfiguration(cfg)
	for _, column := range []string{"ports", "interfaces"} {
		if v, ok := out[column]; ok {
			out[column] = remap(v)
		}
	}
	return out
}
