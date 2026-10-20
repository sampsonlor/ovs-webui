package candidate

import (
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

// Preparation is independently authorized. No execution adapter accepts this
// operation; the legacy spanning_tree.configure intent remains unsupported.
const SpanningTreePrepare = "bridge.spanning-tree.prepare"
const SpanningTreePrepareCapability = "ovs.bridge.spanning-tree.prepare"

var SpanningTreeBasicKeys = []string{"stp-priority", "stp-hello-time", "stp-max-age", "stp-forward-delay", "rstp-priority", "rstp-max-age", "rstp-forward-delay"}

// Missing request keys preserve the captured value; explicit null removes a
// native map key. Both protocols are represented so inactive settings stay
// visible and validated. No raw transaction, original or dependency is input.
type SpanningTreeRequest struct {
	Protocol   string             `json:"protocol"`
	Parameters map[string]*string `json:"parameters"`
}
type SpanningTreeConfig struct {
	STP        bool               `json:"stp_enable"`
	RSTP       bool               `json:"rstp_enable"`
	Parameters map[string]*string `json:"parameters"`
}
type SpanningTreeBridge struct {
	Binding                          Binding
	Configuration                    SpanningTreeConfig
	Known, Supported                 bool
	Authority, Dependency, GraphGate string
}
type SpanningTreeChange struct {
	Before SpanningTreeConfig `json:"before"`
	After  SpanningTreeConfig `json:"after"`
}

func cloneTreeConfig(c SpanningTreeConfig) SpanningTreeConfig {
	m := map[string]*string{}
	for k, v := range c.Parameters {
		m[k] = copyString(v)
	}
	c.Parameters = m
	return c
}
func treeConfigValid(c SpanningTreeConfig) bool {
	if len(c.Parameters) != len(SpanningTreeBasicKeys) {
		return false
	}
	for _, k := range SpanningTreeBasicKeys {
		v, ok := c.Parameters[k]
		if !ok || v != nil && len(*v) > 128 {
			return false
		}
	}
	return true
}
func treeAssessment(c SpanningTreeConfig) spanningtree.Validation {
	m := map[string]any{}
	for k, v := range c.Parameters {
		if v != nil {
			m[k] = *v
		}
	}
	return spanningtree.Validate(&c.STP, &c.RSTP, m)
}
func TreePreparationProblem(p SpanningTreeBridge) string {
	if !p.Known {
		return "SPANNING_TREE_CONFIGURATION_UNKNOWN"
	}
	if !p.Supported {
		return "SPANNING_TREE_SCHEMA_UNSUPPORTED"
	}
	if p.Authority != "local-managed" {
		return "SPANNING_TREE_BRIDGE_AUTHORITY_REQUIRED"
	}
	return p.GraphGate
}
func spanningTreeProblem(i StoredIntent, s Snapshot) string {
	if i.Operation != SpanningTreePrepare || i.SpanningTree == nil || i.Object.Table != "Bridge" || !treeConfigValid(i.SpanningTree.Before) || !treeConfigValid(i.SpanningTree.After) {
		return "INVALID_SPANNING_TREE_DRAFT"
	}
	if i.Object.Generation != s.Generation {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	p, ok := s.SpanningTrees[i.Object.ManagementID]
	if !ok || p.Binding != i.Object {
		return "OBJECT_BINDING_CHANGED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if code := TreePreparationProblem(p); code != "" {
		return code
	}
	if Digest(i.SpanningTree.Before) != Digest(p.Configuration) {
		return "FIELD_CONFLICT"
	}
	if i.Dependency != p.Dependency {
		return "SPANNING_TREE_DEPENDENCY_CHANGED"
	}
	return ""
}
func spanningTreeChecks(i StoredIntent, s Snapshot) []Gate {
	out := []Gate{}
	if i.SpanningTree != nil {
		v := treeAssessment(i.SpanningTree.After)
		for _, c := range v.Checks {
			out = append(out, gate(c.Code, "blocked", i.ID))
		}
	}
	// This blocker is unconditional, including forged images and future readers
	// with an enabled Safe Apply service. A successful review is not executable.
	return append(out, gate("SPANNING_TREE_EXECUTION_PENDING", "blocked", i.ID))
}
func spanningTreeDiff(i StoredIntent, s Snapshot) []Diff {
	if i.SpanningTree == nil {
		return nil
	}
	p := s.SpanningTrees[i.Object.ManagementID]
	before, after := i.SpanningTree.Before, i.SpanningTree.After
	conflict := spanningTreeProblem(i, s) != ""
	out := []Diff{}
	add := func(field string, b, a, current any) {
		if !p.Known {
			current = nil
		}
		out = append(out, Diff{Object: i.Object, Field: field, Before: b, After: a, Current: current, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: conflict})
	}
	add("stp_enable", before.STP, after.STP, p.Configuration.STP)
	add("rstp_enable", before.RSTP, after.RSTP, p.Configuration.RSTP)
	for _, k := range SpanningTreeBasicKeys {
		add("other_config."+k, before.Parameters[k], after.Parameters[k], p.Configuration.Parameters[k])
	}
	return out
}
func hasSpanningTree(intents []StoredIntent) bool {
	return slices.ContainsFunc(intents, func(i StoredIntent) bool { return i.Operation == SpanningTreePrepare })
}
func stageSpanningTree(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != SpanningTreePrepare {
		return c, apitypes.Fail(422, "SPANNING_TREE_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	r := in.SpanningTree
	if r == nil || !slices.Contains([]string{"disabled", "stp", "rstp"}, r.Protocol) || r.Parameters == nil || len(r.Parameters) > 7 || !apitypes.ManagementID(in.ID) || in.Object.Table != "Bridge" || in.Topology != nil || in.Policing != nil || in.MTURequest != 0 || in.Name != "" || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_SPANNING_TREE_DRAFT")
	}
	if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	p, ok := s.SpanningTrees[in.Object.ManagementID]
	if !ok || p.Binding != in.Object {
		return c, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
	}
	if code := TreePreparationProblem(p); code != "" {
		return c, apitypes.Fail(409, code)
	}
	base := cloneTreeConfig(p.Configuration)
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency, Value: normalize(VLAN{}), Before: normalize(VLAN{}), SpanningTree: &SpanningTreeChange{Before: cloneTreeConfig(base)}}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != SpanningTreePrepare || prior.ID != in.ID || prior.Object != in.Object || spanningTreeProblem(prior, s) != "" {
			return c, apitypes.Fail(409, "SPANNING_TREE_RESTAGE_REQUIRED")
		}
		i = prior
		base = cloneTreeConfig(prior.SpanningTree.Before)
	}
	base.STP, base.RSTP = r.Protocol == "stp", r.Protocol == "rstp"
	for k, v := range r.Parameters {
		// A disabled protocol change preserves all parameters. An active change
		// may edit only its own basic keys, leaving inactive values untouched.
		if !slices.Contains(SpanningTreeBasicKeys, k) || r.Protocol == "disabled" || !strings.HasPrefix(k, r.Protocol+"-") || v != nil && (len(*v) == 0 || len(*v) > 128 || strings.IndexFunc(*v, func(c rune) bool { return c < '0' || c > '9' }) >= 0) {
			return c, apitypes.Fail(422, "INVALID_SPANNING_TREE_PARAMETER")
		}
		base.Parameters[k] = copyString(v)
	}
	i.SpanningTree = &SpanningTreeChange{Before: cloneTreeConfig(i.SpanningTree.Before), After: base}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}
