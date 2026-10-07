package candidate

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const InterfacePolicingSet = "interface.policing.set"

// This release supports one rate domain and preserves native default bursts.
// A separate root policy declares exclusive ingress management on this identity.
type PolicingRequest struct {
	Mode string `json:"mode"`
	Rate int    `json:"rate"`
}
type PolicingConfig struct {
	Rate        int `json:"ingress_policing_rate"`
	Burst       int `json:"ingress_policing_burst"`
	PacketRate  int `json:"ingress_policing_kpkts_rate"`
	PacketBurst int `json:"ingress_policing_kpkts_burst"`
}
type InterfacePolicing struct {
	Binding, Port, Bridge      Binding
	Configuration              PolicingConfig
	Name, Type                 string
	IfIndex                    int
	Known, Supported, Eligible bool
	Authority, Dependency      string
}
type PolicingChange struct {
	Before       PolicingConfig `json:"before"`
	After        PolicingConfig `json:"after"`
	Port         Binding        `json:"port"`
	Bridge       Binding        `json:"bridge"`
	Name         string         `json:"name"`
	IfIndex      int            `json:"ifindex"`
	Compensating bool           `json:"compensating,omitempty"`
}

func ValidPolicing(p PolicingConfig) bool {
	return p.Rate >= 0 && p.Rate <= 1000000 && p.PacketRate >= 0 && p.PacketRate <= 1000 &&
		(p.Rate == 0 || p.PacketRate == 0) && p.Burst == 0 && p.PacketBurst == 0
}
func PolicingEditable(p InterfacePolicing) bool {
	return p.Known && p.Supported && p.Eligible && p.Authority == "local-exclusive" && ValidPolicing(p.Configuration)
}
func requestedPolicing(p *PolicingRequest) (PolicingConfig, bool) {
	var out PolicingConfig
	if p == nil {
		return out, false
	}
	switch p.Mode {
	case "disabled":
		return out, p.Rate == 0
	case "bandwidth":
		out.Rate = p.Rate
	case "packets":
		out.PacketRate = p.Rate
	default:
		return out, false
	}
	return out, p.Rate > 0 && ValidPolicing(out)
}
func policingProblem(i StoredIntent, s Snapshot) string {
	m := i.Policing
	if i.Operation != InterfacePolicingSet || m == nil || i.Object.Table != "Interface" || !ValidPolicing(m.Before) || !ValidPolicing(m.After) || m.Name == "" || m.IfIndex <= 0 {
		return "INVALID_INTERFACE_POLICING"
	}
	if i.Object.Generation != s.Generation {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	p, ok := s.Policings[i.Object.ManagementID]
	if !ok || p.Binding != i.Object || p.Port != m.Port || p.Bridge != m.Bridge || p.Name != m.Name || p.IfIndex != m.IfIndex {
		return "OBJECT_BINDING_CHANGED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if !p.Known {
		return "NATIVE_CONFIGURATION_UNKNOWN"
	}
	if i.Dependency != p.Dependency {
		return "POLICING_DEPENDENCY_CHANGED"
	}
	if m.Before != p.Configuration {
		return "FIELD_CONFLICT"
	}
	return ""
}
func policingChecks(i StoredIntent, s Snapshot) []Gate {
	p := s.Policings[i.Object.ManagementID]
	out := []Gate{}
	if !p.Supported {
		out = append(out, gate("POLICING_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if !p.Eligible {
		out = append(out, gate("STANDALONE_INTERNAL_POLICING_REQUIRED", "blocked", i.ID))
	}
	if p.Authority != "local-exclusive" {
		out = append(out, gate("EXCLUSIVE_INGRESS_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	return append(out, gate("POLICING_KERNEL_CHECK_AT_DISPATCH", "allowed", i.ID), gate("POLICING_TRAFFIC_AND_MANAGEMENT_PATH_REVIEW_REQUIRED", "allowed", i.ID))
}
func policingDiff(i StoredIntent, s Snapshot) []Diff {
	fields := []string{"ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"}
	values := func(p PolicingConfig) []int { return []int{p.Rate, p.Burst, p.PacketRate, p.PacketBurst} }
	before, after := []int{0, 0, 0, 0}, []int{0, 0, 0, 0}
	if i.Policing != nil {
		before, after = values(i.Policing.Before), values(i.Policing.After)
	}
	p := s.Policings[i.Object.ManagementID]
	now := values(p.Configuration)
	out := []Diff{}
	for n, field := range fields {
		var current any
		if p.Known {
			current = now[n]
		}
		out = append(out, Diff{Object: i.Object, Field: field, Before: before[n], After: after[n], Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: policingProblem(i, s) != ""})
	}
	return out
}
func stagePolicing(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != InterfacePolicingSet {
		return c, apitypes.Fail(422, "POLICING_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	after, valid := requestedPolicing(in.Policing)
	if !valid || !apitypes.ManagementID(in.ID) || in.Object.Table != "Interface" || in.MTURequest != 0 || in.Name != "" || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_INTERFACE_POLICING")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	p, ok := s.Policings[in.Object.ManagementID]
	if !ok || p.Binding != in.Object {
		return c, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if !PolicingEditable(p) {
		return c, apitypes.Fail(409, "EXCLUSIVE_INTERNAL_POLICING_AUTHORITY_REQUIRED")
	}
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), Policing: &PolicingChange{Before: p.Configuration, After: after, Port: p.Port, Bridge: p.Bridge, Name: p.Name, IfIndex: p.IfIndex}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != InterfacePolicingSet || prior.ID != in.ID || prior.Object != in.Object || prior.Policing == nil {
			return c, apitypes.Fail(409, "POLICING_RESTAGE_REQUIRED")
		}
		i = prior
		m := *prior.Policing
		m.After = after
		i.Policing = &m
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
func hasPolicing(intents []StoredIntent) bool {
	return slices.ContainsFunc(intents, func(i StoredIntent) bool { return i.Operation == InterfacePolicingSet })
}
