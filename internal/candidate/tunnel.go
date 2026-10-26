package candidate

import (
	"errors"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/tunnel"
)

const TunnelCreate = "tunnel.static.create"
const TunnelConfigure = "tunnel.static.configure"
const TunnelDelete = "tunnel.static.delete"
const TunnelCapability = "ovs.tunnel.static.write"

func IsTunnelOperation(op string) bool {
	return slices.Contains([]string{TunnelCreate, TunnelConfigure, TunnelDelete}, op)
}

type TunnelChange struct {
	Interface Binding               `json:"interface"`
	Before    *tunnel.Configuration `json:"before"`
	After     *tunnel.Configuration `json:"after"`
}

func tunnelOptions(c tunnel.Configuration) map[string]any {
	out := map[string]any{}
	for key, value := range c.Options {
		out[key] = value
	}
	return out
}

func tunnelIdentityAvailable(t TopologySnapshot, target Binding, desired tunnel.Configuration) error {
	identity, err := tunnel.NativeIdentity(desired)
	if err != nil {
		return err
	}
	for _, n := range t.Nodes {
		if n.Binding.Table != "Interface" || n.Binding == target || n.Type != desired.Type {
			continue
		}
		cfg := t.Configurations[n.Binding.ManagementID]
		other, err := tunnel.Decode(n.Type, cfg["options"])
		if err != nil {
			// A dynamic/advanced same-type tunnel could overlap this endpoint.
			// Do not guess its receive identity or adopt controller-owned state.
			return errors.New("TUNNEL_EXISTING_RECEIVE_IDENTITY_UNPROVEN")
		}
		value, err := tunnel.NativeIdentity(other)
		if err != nil || value == identity {
			return errors.New("TUNNEL_RECEIVE_IDENTITY_IN_USE")
		}
	}
	return nil
}

func tunnelTopologyProblem(i StoredIntent, t TopologySnapshot) string {
	g := i.Topology
	if g == nil || g.Tunnel == nil || g.Tunnel.Interface.Table != "Interface" {
		return "INVALID_TUNNEL_REQUEST"
	}
	c := g.Tunnel
	if g.TunnelDependency == "" || g.TunnelDependency != tunnelDependency(g, t) {
		return "TUNNEL_DEPENDENCY_CHANGED"
	}
	if i.Operation == TunnelCreate && (c.Before != nil || c.After == nil) || i.Operation == TunnelConfigure && (c.Before == nil || c.After == nil) || i.Operation == TunnelDelete && (c.Before == nil || c.After != nil) {
		return "INVALID_TUNNEL_REQUEST"
	}
	for _, value := range []*tunnel.Configuration{c.Before, c.After} {
		if value != nil && tunnel.Validate(*value) != nil {
			return "INVALID_TUNNEL_REQUEST"
		}
	}
	desired, target := c.After, c.Interface
	if g.Compensating {
		desired = c.Before
		if replacement, ok := g.Replacements[c.Interface.OVSUUID]; ok {
			target = replacement
		}
	}
	if desired != nil {
		if !slices.Contains(t.TunnelTypes, desired.Type) {
			return "TUNNEL_RUNTIME_TYPE_UNAVAILABLE"
		}
		if err := tunnelIdentityAvailable(t, target, *desired); err != nil {
			return err.Error()
		}
	}
	return ""
}

// Preserve the captured read set through dispatch, Applied and compensation.
// Only our explicit original/target/restoration nodes can change; unrelated
// native configuration and fresh incoming objects never become new originals.
func tunnelDependency(g *TopologyChange, t TopologySnapshot) string {
	owned := map[string]bool{}
	for _, nodes := range [][]TopologyNode{g.Before, g.After, g.Restored} {
		for _, node := range nodes {
			owned[node.Binding.ManagementID] = true
		}
	}
	unchanged := map[string]TopologyNode{}
	for id, node := range t.Nodes {
		if !owned[id] {
			unchanged[id] = node
		}
	}
	return Digest(unchanged)
}
