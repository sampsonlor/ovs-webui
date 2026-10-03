package candidate

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const InterfaceMTUSet = "interface.mtu.set"

// This first MTU slice changes an explicit request on a standalone, non-local
// internal Interface. Empty requests and type/options changes need separate
// restoration semantics and cannot be normalized into this operation.
type InterfaceMTU struct {
	Binding                    Binding
	Port, Bridge               Binding
	Requested                  int
	Known, Supported, Eligible bool
	Authority, Dependency      string
}
type MTUChange struct {
	Before int     `json:"before"`
	After  int     `json:"after"`
	Port   Binding `json:"port"`
	Bridge Binding `json:"bridge"`
}

func ValidMTU(n int) bool { return n >= 576 && n <= 65535 }
func MTUEditable(p InterfaceMTU) bool {
	return p.Known && p.Supported && p.Eligible && p.Authority == "local-managed" && ValidMTU(p.Requested)
}
func mtuProblem(i StoredIntent, s Snapshot) string {
	if i.Operation != InterfaceMTUSet || i.MTU == nil || i.Object.Table != "Interface" || !ValidMTU(i.MTU.Before) || !ValidMTU(i.MTU.After) {
		return "INVALID_INTERFACE_MTU"
	}
	if i.Object.Generation != s.Generation {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	p, ok := s.Interfaces[i.Object.ManagementID]
	if !ok || p.Binding != i.Object || p.Port != i.MTU.Port || p.Bridge != i.MTU.Bridge {
		return "OBJECT_BINDING_CHANGED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if !p.Known {
		return "NATIVE_CONFIGURATION_UNKNOWN"
	}
	if i.Dependency != p.Dependency {
		return "MTU_DEPENDENCY_CHANGED"
	}
	if i.MTU.Before != p.Requested {
		return "FIELD_CONFLICT"
	}
	return ""
}
func mtuChecks(i StoredIntent, s Snapshot) []Gate {
	p := s.Interfaces[i.Object.ManagementID]
	out := []Gate{}
	if !p.Supported {
		out = append(out, gate("MTU_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if !p.Eligible {
		out = append(out, gate("INTERNAL_EXPLICIT_MTU_REQUIRED", "blocked", i.ID))
	}
	if p.Authority != "local-managed" {
		code := "MTU_AUTHORITY_REQUIRED"
		if p.Authority == "externally-controlled" {
			code = "EXTERNALLY_CONTROLLED"
		}
		out = append(out, gate(code, "blocked", i.ID))
	}
	return append(out, gate("MTU_DEVICE_AND_MANAGEMENT_PATH_REVIEW_REQUIRED", "allowed", i.ID))
}
func mtuDiff(i StoredIntent, s Snapshot) Diff {
	var before, after, current any
	if i.MTU != nil {
		before, after = i.MTU.Before, i.MTU.After
	}
	if p := s.Interfaces[i.Object.ManagementID]; p.Known {
		current = p.Requested
	}
	return Diff{Object: i.Object, Field: "mtu_request", Before: before, After: after, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: mtuProblem(i, s) != ""}
}
func stageMTU(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != InterfaceMTUSet {
		return c, apitypes.Fail(422, "MTU_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if !apitypes.ManagementID(in.ID) || in.Object.Table != "Interface" || !ValidMTU(in.MTURequest) || in.Name != "" || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_INTERFACE_MTU")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	p, ok := s.Interfaces[in.Object.ManagementID]
	if !ok || p.Binding != in.Object {
		return c, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if !MTUEditable(p) {
		return c, apitypes.Fail(409, "INTERNAL_EXPLICIT_MTU_AUTHORITY_REQUIRED")
	}
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), MTU: &MTUChange{Before: p.Requested, After: in.MTURequest, Port: p.Port, Bridge: p.Bridge}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != in.Operation || prior.ID != in.ID || prior.Object != in.Object || prior.MTU == nil {
			return c, apitypes.Fail(409, "MTU_RESTAGE_REQUIRED")
		}
		i = prior
		m := *prior.MTU
		m.After = in.MTURequest
		i.MTU = &m
	}
	// Editing the draft preserves the sealed original, including on conflict.
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
func hasMTU(intents []StoredIntent) bool {
	return slices.ContainsFunc(intents, func(i StoredIntent) bool { return i.Operation == InterfaceMTUSet })
}
