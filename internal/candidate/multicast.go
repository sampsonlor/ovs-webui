package candidate

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/multicast"
)

const BridgeMulticastSet = "bridge.multicast.configure"
const BridgeMulticastCapability = "ovs.bridge.multicast.write"
const MulticastMarker = "ovs-webui.multicast-commit"

type MulticastBridge struct {
	Binding                     Binding
	Configuration               multicast.Configuration
	Known, Supported            bool
	Authority, Dependency, Gate string
	Bindings                    []Binding
}
type MulticastChange struct {
	Before multicast.Configuration `json:"before"`
	After  multicast.Configuration `json:"after"`
}

func MulticastEditable(b MulticastBridge) bool {
	return b.Known && b.Supported && b.Authority == "local-managed" && b.Gate == "" && multicast.Validate(b.Configuration) == nil
}
func multicastProblem(i StoredIntent, s Snapshot) string {
	if i.Operation != BridgeMulticastSet || i.Multicast == nil || i.Object.Table != "Bridge" || multicast.Validate(i.Multicast.Before) != nil || multicast.Validate(i.Multicast.After) != nil {
		return "INVALID_MULTICAST_REQUEST"
	}
	if i.Object.Generation != s.Generation {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	b, ok := s.Multicasts[i.Object.ManagementID]
	if !ok || b.Binding != i.Object {
		return "OBJECT_BINDING_CHANGED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if !b.Known {
		return "NATIVE_CONFIGURATION_UNKNOWN"
	}
	if i.Dependency != b.Dependency {
		return "MULTICAST_DEPENDENCY_CHANGED"
	}
	if Digest(i.Multicast.Before) != Digest(b.Configuration) {
		return "FIELD_CONFLICT"
	}
	return ""
}
func multicastChecks(i StoredIntent, s Snapshot) []Gate {
	b := s.Multicasts[i.Object.ManagementID]
	out := []Gate{}
	if !b.Supported {
		out = append(out, gate("MULTICAST_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if b.Authority != "local-managed" {
		out = append(out, gate("MULTICAST_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	if b.Gate != "" {
		out = append(out, gate(b.Gate, "blocked", i.ID))
	}
	out = append(out, gate("MULTICAST_NATIVE_FORWARDING_REVIEW_REQUIRED", "allowed", i.ID))
	return out
}
func multicastDiff(i StoredIntent, s Snapshot) []Diff {
	if i.Multicast == nil {
		return []Diff{}
	}
	b := s.Multicasts[i.Object.ManagementID]
	out := []Diff{}
	add := func(field string, before, after, current any) {
		out = append(out, Diff{Object: i.Object, Field: field, Before: before, After: after, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: multicastProblem(i, s) != ""})
	}
	var enabled any
	if b.Known {
		enabled = b.Configuration.Enabled
	}
	add("mcast_snooping_enable", i.Multicast.Before.Enabled, i.Multicast.After.Enabled, enabled)
	for _, key := range multicast.Keys {
		var current any
		if b.Known {
			current = b.Configuration.Parameters[key]
		}
		add("other_config."+key, i.Multicast.Before.Parameters[key], i.Multicast.After.Parameters[key], current)
	}
	return out
}
func stageMulticast(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 {
		return c, apitypes.Fail(422, "MULTICAST_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if in.Operation != BridgeMulticastSet || !apitypes.ManagementID(in.ID) || in.Object.Table != "Bridge" || in.Multicast == nil || in.Protected != nil || in.SpanningTree != nil || in.Topology != nil || in.Policing != nil || in.Name != "" || in.VLANID != 0 || in.MTURequest != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_MULTICAST_REQUEST")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	b, ok := s.Multicasts[in.Object.ManagementID]
	if !ok || b.Binding != in.Object {
		return c, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if !MulticastEditable(b) {
		return c, apitypes.Fail(409, "MULTICAST_AUTHORITY_OR_GRAPH_REQUIRED")
	}
	before := multicast.Clone(b.Configuration)
	i := StoredIntent{ID: in.ID, Object: in.Object, Operation: in.Operation, Schema: s.Schema, Dependency: b.Dependency, Before: normalize(VLAN{}), Value: normalize(VLAN{})}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != in.Operation || prior.ID != in.ID || prior.Object != in.Object || prior.Multicast == nil {
			return c, apitypes.Fail(409, "MULTICAST_RESTAGE_REQUIRED")
		}
		i = prior
		before = multicast.Clone(prior.Multicast.Before)
	}
	after, err := multicast.Merge(before, *in.Multicast)
	if err != nil {
		return c, apitypes.Fail(422, err.Error())
	}
	i.Multicast = &MulticastChange{Before: before, After: after}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
