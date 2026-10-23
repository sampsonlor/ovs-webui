package candidate

import "github.com/sampsonlor/ovs-webui/internal/apitypes"

const PortIsolationSet = "port.isolation.set"
const PortIsolationCapability = "ovs.port.isolation.write"

// Protected ports cannot exchange traffic with other protected ports on the
// same native Bridge. This is not an ACL, tenant firewall, or general PVLAN.
type IsolationPort struct {
	Binding                     Binding
	Protected                   bool
	Known, Supported            bool
	Authority, Dependency, Gate string
	Bindings                    []Binding
}
type IsolationChange struct {
	Before bool `json:"before"`
	After  bool `json:"after"`
}

func IsolationEditable(p IsolationPort) bool {
	return p.Known && p.Supported && p.Authority == "local-managed" && p.Gate == ""
}
func isolationProblem(i StoredIntent, s Snapshot) string {
	if i.Operation != PortIsolationSet || i.Isolation == nil || i.Object.Table != "Port" {
		return "INVALID_PORT_ISOLATION"
	}
	if i.Object.Generation != s.Generation {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	p, ok := s.Isolations[i.Object.ManagementID]
	if !ok || p.Binding != i.Object {
		return "OBJECT_BINDING_CHANGED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if !p.Known {
		return "NATIVE_CONFIGURATION_UNKNOWN"
	}
	if i.Dependency != p.Dependency {
		return "ISOLATION_DEPENDENCY_CHANGED"
	}
	if i.Isolation.Before != p.Protected {
		return "FIELD_CONFLICT"
	}
	return ""
}
func isolationChecks(i StoredIntent, s Snapshot) []Gate {
	p := s.Isolations[i.Object.ManagementID]
	out := []Gate{}
	if !p.Supported {
		out = append(out, gate("ISOLATION_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if p.Authority != "local-managed" {
		out = append(out, gate("ISOLATION_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	if p.Gate != "" {
		out = append(out, gate(p.Gate, "blocked", i.ID))
	}
	return append(out, gate("PROTECTED_PORT_NATIVE_SEMANTICS_REVIEW_REQUIRED", "allowed", i.ID))
}
func isolationDiff(i StoredIntent, s Snapshot) Diff {
	var before, after, current any
	if i.Isolation != nil {
		before, after = i.Isolation.Before, i.Isolation.After
	}
	if p := s.Isolations[i.Object.ManagementID]; p.Known {
		current = p.Protected
	}
	return Diff{Object: i.Object, Field: "protected", Before: before, After: after, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: isolationProblem(i, s) != ""}
}
func stageIsolation(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 {
		return c, apitypes.Fail(422, "ISOLATION_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if in.Operation != PortIsolationSet || !apitypes.ManagementID(in.ID) || in.Object.Table != "Port" || in.Protected == nil || in.SpanningTree != nil || in.Topology != nil || in.Policing != nil || in.Name != "" || in.VLANID != 0 || in.MTURequest != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_PORT_ISOLATION")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	p, ok := s.Isolations[in.Object.ManagementID]
	if !ok || p.Binding != in.Object {
		return c, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if !IsolationEditable(p) {
		return c, apitypes.Fail(409, "ISOLATION_AUTHORITY_OR_GRAPH_REQUIRED")
	}
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), Isolation: &IsolationChange{Before: p.Protected, After: *in.Protected}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != in.Operation || prior.ID != in.ID || prior.Object != in.Object || prior.Isolation == nil {
			return c, apitypes.Fail(409, "ISOLATION_RESTAGE_REQUIRED")
		}
		i = prior
		m := *prior.Isolation
		m.After = *in.Protected
		i.Isolation = &m
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
