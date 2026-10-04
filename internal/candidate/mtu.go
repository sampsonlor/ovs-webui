package candidate

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const InterfaceMTUSet = "interface.mtu.set"

const InterfaceMTUClear = "interface.mtu.clear"

func IsMTUOperation(op string) bool { return op == InterfaceMTUSet || op == InterfaceMTUClear }

// A nil request is the native empty optional set, never zero or an inferred
// explicit value. Default context is captured only for transitions involving it.
type InterfaceMTU struct {
	Binding                    Binding
	Port, Bridge               Binding
	Requested                  *int
	Observed                   int
	Default                    *MTUDefault
	Known, Supported, Eligible bool
	Authority, Dependency      string
}
type MTUChange struct {
	Before  *int        `json:"before"`
	After   *int        `json:"after"`
	Port    Binding     `json:"port"`
	Bridge  Binding     `json:"bridge"`
	Default *MTUDefault `json:"default_context,omitempty"`
	// Only Reverse derives this private journal state; public intents cannot set it.
	Compensating bool `json:"compensating,omitempty"`
}
type MTUDefault struct {
	MTU        int       `json:"mtu"`
	Dependency string    `json:"dependency"`
	Bindings   []Binding `json:"bindings"`
}

func MTUPointer(n int) *int       { return &n }
func SameMTU(a, b *int) bool      { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func validMTURequest(n *int) bool { return n == nil || ValidMTU(*n) }
func ExpectedMTU(m *MTUChange) int {
	if m.After != nil {
		return *m.After
	}
	if m.Default != nil {
		return m.Default.MTU
	}
	return 0
}

func MTUOriginalDeviceRequired(m *MTUChange) bool {
	return m != nil && m.Default != nil && !m.Compensating && !SameMTU(m.Before, m.After) && (m.Before == nil || m.After == nil)
}
func OriginalMTU(m *MTUChange) int {
	if m.Before != nil {
		return *m.Before
	}
	if m.Default != nil {
		return m.Default.MTU
	}
	return 0
}

func ValidMTU(n int) bool { return n >= 576 && n <= 65535 }
func MTUEditable(p InterfaceMTU) bool {
	return p.Known && p.Supported && p.Eligible && p.Authority == "local-managed" && validMTURequest(p.Requested) && (p.Requested != nil || p.Default != nil && p.Observed == p.Default.MTU)
}
func MTUClearable(p InterfaceMTU) bool {
	return MTUEditable(p) && p.Requested != nil && p.Default != nil && p.Observed == *p.Requested
}
func mtuProblem(i StoredIntent, s Snapshot) string {
	if !IsMTUOperation(i.Operation) || i.MTU == nil || i.Object.Table != "Interface" || !validMTURequest(i.MTU.Before) || !validMTURequest(i.MTU.After) || i.MTU.After == nil && i.Operation != InterfaceMTUClear || i.MTU.After != nil && i.Operation != InterfaceMTUSet || (i.MTU.Before == nil || i.MTU.After == nil) && (i.MTU.Default == nil || !ValidMTU(i.MTU.Default.MTU)) {
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
	if !SameMTU(i.MTU.Before, p.Requested) {
		return "FIELD_CONFLICT"
	}
	if i.MTU.Default != nil && (p.Default == nil || Digest(i.MTU.Default) != Digest(p.Default)) {
		return "MTU_DEFAULT_DEPENDENCY_CHANGED"
	}
	if MTUOriginalDeviceRequired(i.MTU) && p.Observed != OriginalMTU(i.MTU) {
		return "MTU_ORIGINAL_DEVICE_UNPROVEN"
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
		out = append(out, gate("STANDALONE_INTERNAL_MTU_REQUIRED", "blocked", i.ID))
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
		if i.MTU.Before != nil {
			before = *i.MTU.Before
		}
		if i.MTU.After != nil {
			after = *i.MTU.After
		}
	}
	if p := s.Interfaces[i.Object.ManagementID]; p.Known {
		if p.Requested != nil {
			current = *p.Requested
		}
	}
	return Diff{Object: i.Object, Field: "mtu_request", Before: before, After: after, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: mtuProblem(i, s) != ""}
}
func mtuDefaultDiff(i StoredIntent, s Snapshot) Diff {
	var current any
	if d := s.Interfaces[i.Object.ManagementID].Default; d != nil {
		current = d.MTU
	}
	return Diff{Object: i.Object, Field: "mtu_default_dependency", Before: i.MTU.Default.MTU, After: i.MTU.Default.MTU, Current: current, Authority: "ovsdb-operational", Operation: i.Operation, IntentID: i.ID, Conflict: mtuProblem(i, s) != ""}
}
func stageMTU(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || !IsMTUOperation(cmd.Intents[0].Operation) {
		return c, apitypes.Fail(422, "MTU_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	if !apitypes.ManagementID(in.ID) || in.Object.Table != "Interface" || in.Operation == InterfaceMTUSet && !ValidMTU(in.MTURequest) || in.Operation == InterfaceMTUClear && in.MTURequest != 0 || in.Name != "" || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
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
		return c, apitypes.Fail(409, "INTERNAL_MTU_AUTHORITY_REQUIRED")
	}
	var after *int
	if in.Operation == InterfaceMTUSet {
		after = MTUPointer(in.MTURequest)
	}
	if in.Operation == InterfaceMTUClear && !MTUClearable(p) {
		return c, apitypes.Fail(409, "MTU_DEFAULT_UNPROVEN")
	}
	m := &MTUChange{Before: p.Requested, After: after, Port: p.Port, Bridge: p.Bridge}
	if p.Requested == nil || after == nil {
		m.Default = p.Default
	}
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), MTU: m}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if !IsMTUOperation(prior.Operation) || prior.ID != in.ID || prior.Object != in.Object || prior.MTU == nil || (prior.MTU.Default == nil && after == nil) {
			return c, apitypes.Fail(409, "MTU_RESTAGE_REQUIRED")
		}
		i = prior
		m := *prior.MTU
		m.After = after
		i.MTU = &m
		i.Operation = in.Operation
	}
	// Editing the draft preserves the sealed original, including on conflict.
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
func hasMTU(intents []StoredIntent) bool {
	return slices.ContainsFunc(intents, func(i StoredIntent) bool { return IsMTUOperation(i.Operation) })
}
