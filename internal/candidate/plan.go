package candidate

import (
	"slices"
	"sort"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func normalize(v VLAN) VLAN {
	v.Trunks = append([]int{}, v.Trunks...)
	v.CVLANs = append([]int{}, v.CVLANs...)
	sort.Ints(v.Trunks)
	sort.Ints(v.CVLANs)
	return v
}
func inputValid(v VLAN) bool {
	if v.Mode == nil || !slices.Contains([]string{"access", "trunk", "native-tagged", "native-untagged", "dot1q-tunnel"}, *v.Mode) {
		return false
	}
	if (*v.Mode == "trunk") != (v.Tag == nil) {
		return false
	}
	if v.Tag != nil && (*v.Tag < 1 || *v.Tag > 4094) {
		return false
	}
	for _, list := range [][]int{v.Trunks, v.CVLANs} {
		seen := map[int]bool{}
		for _, n := range list {
			if n < 1 || n > 4094 || seen[n] {
				return false
			}
			seen[n] = true
		}
	}
	if (*v.Mode == "access" || *v.Mode == "dot1q-tunnel") && len(v.Trunks) != 0 {
		return false
	}
	return *v.Mode == "dot1q-tunnel" || len(v.CVLANs) == 0
}
func equal(a, b VLAN) bool { return Digest(normalize(a)) == Digest(normalize(b)) }
func ConflictID(c Candidate, s Snapshot) string {
	h := Digest([]any{c.ID, c.Revision, s.Generation, s.Revision})
	return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
}
func gate(code, state, id string) Gate {
	return Gate{Code: code, State: state, Reason: code, IntentID: id}
}
func comparison(i StoredIntent, s Snapshot) (Port, string) {
	if i.Object.Generation != s.Generation {
		return Port{}, "GENERATION_RECONCILIATION_REQUIRED"
	}
	p, ok := s.Ports[i.Object.ManagementID]
	if !ok || p.Binding != i.Object {
		return Port{}, "OBJECT_BINDING_CHANGED"
	}
	if !p.Known {
		return p, "NATIVE_CONFIGURATION_UNKNOWN"
	}
	if i.Schema != s.Schema {
		return p, "SCHEMA_CHANGED"
	}
	if p.Dependency != i.Dependency {
		return p, "DEPENDENCY_CHANGED"
	}
	if !equal(p.VLAN, i.Before) {
		return p, "FIELD_CONFLICT"
	}
	return p, ""
}
func Compare(c Candidate, s Snapshot) View {
	v := View{Candidate: c, CurrentGeneration: &s.Generation, CurrentRevision: &s.Revision, Diff: []Diff{}, Checks: []Gate{}}
	id := ConflictID(c, s)
	v.ConflictSnapshot = &id
	for _, i := range c.Intents {
		p, problem := comparison(i, s)
		if problem != "" {
			v.Checks = append(v.Checks, gate(problem, "blocked", i.ID))
			v.State = "conflict"
			if problem == "GENERATION_RECONCILIATION_REQUIRED" || problem == "OBJECT_BINDING_CHANGED" {
				v.State = "reconciliation-required"
			}
		}
		before := []any{i.Before.Mode, i.Before.Tag, i.Before.Trunks, i.Before.CVLANs}
		after := []any{i.Value.Mode, i.Value.Tag, i.Value.Trunks, i.Value.CVLANs}
		current := []any{p.VLAN.Mode, p.VLAN.Tag, p.VLAN.Trunks, p.VLAN.CVLANs}
		for n, field := range []string{"vlan_mode", "tag", "trunks", "cvlans"} {
			var now any
			if p.Known {
				now = current[n]
			}
			v.Diff = append(v.Diff, Diff{Object: i.Object, Field: field, Before: before[n], After: after[n], Current: now, Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: problem != ""})
		}
	}
	return v
}

// Prepare preserves original values when editing an already-staged intent.
// Rebase is the only operation that replaces a captured original; every changed
// field group/dependency requires an explicit, snapshot-bound user choice.
func Prepare(e Envelope, cmd Command, s Snapshot) (Envelope, error) {
	c := e.Candidate
	c.Intents = append([]StoredIntent{}, c.Intents...)
	if c.Consumed != nil {
		return e, apitypes.Fail(409, "CANDIDATE_CONSUMED")
	}
	switch cmd.Operation {
	case "stage":
		if len(cmd.Intents) == 0 || len(cmd.Intents) > MaxIntents {
			return e, apitypes.Fail(422, "INVALID_INTENT")
		}
		seen := map[string]bool{}
		for _, in := range cmd.Intents {
			if seen[in.ID] || !apitypes.ManagementID(in.ID) || in.Operation != "port.vlan.set" || in.Object.Table != "Port" || !inputValid(in.Value) {
				return e, apitypes.Fail(422, "UNSUPPORTED_CONFIGURATION")
			}
			seen[in.ID] = true
			p, ok := s.Ports[in.Object.ManagementID]
			if in.Object.Generation != s.Generation || c.Generation != nil && *c.Generation != s.Generation {
				return e, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
			}
			if !ok || p.Binding != in.Object {
				return e, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
			}
			if !p.Known {
				return e, apitypes.Fail(409, "NATIVE_CONFIGURATION_UNKNOWN")
			}
			in.Value = normalize(in.Value)
			index := -1
			for n, prior := range c.Intents {
				if prior.ID == in.ID {
					index = n
					if prior.Object != in.Object {
						return e, apitypes.Fail(422, "INTENT_BINDING_MISMATCH")
					}
				} else if prior.Object.ManagementID == in.Object.ManagementID {
					return e, apitypes.Fail(409, "DUPLICATE_FIELD_INTENT")
				}
			}
			if index >= 0 {
				c.Intents[index].Intent = in
			} else {
				c.Intents = append(c.Intents, StoredIntent{Intent: in, Before: normalize(p.VLAN), Dependency: p.Dependency, Schema: s.Schema})
			}
		}
		if len(c.Intents) > MaxIntents {
			return e, apitypes.Fail(429, "CANDIDATE_INTENT_LIMIT")
		}
		if c.Generation == nil {
			c.Generation = &s.Generation
			c.BaseRevision = &s.Revision
		}
	case "remove":
		seen := map[string]bool{}
		for _, id := range cmd.IntentIDs {
			if seen[id] {
				return e, apitypes.Fail(422, "INVALID_INTENT")
			}
			seen[id] = true
			index := slices.IndexFunc(c.Intents, func(i StoredIntent) bool { return i.ID == id })
			if index < 0 {
				return e, apitypes.Fail(404, "INTENT_NOT_FOUND")
			}
			c.Intents = slices.Delete(c.Intents, index, index+1)
		}
	case "discard":
		c.Intents = []StoredIntent{}
	case "rebase":
		if c.Generation == nil || *c.Generation != s.Generation || cmd.Generation != s.Generation {
			return e, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
		}
		if cmd.ConfigRevision != s.Revision || cmd.ConflictSnapshot == nil || *cmd.ConflictSnapshot != ConflictID(c, s) {
			return e, apitypes.Fail(409, "CONFLICT_SNAPSHOT_CHANGED")
		}
		resolutions := map[string]string{}
		for _, r := range cmd.Resolutions {
			if resolutions[r.IntentID] != "" || r.Choice != "keep-current" && r.Choice != "keep-mine" {
				return e, apitypes.Fail(422, "INVALID_RESOLUTION")
			}
			resolutions[r.IntentID] = r.Choice
		}
		next := []StoredIntent{}
		for _, i := range c.Intents {
			p, reason := comparison(i, s)
			if reason == "GENERATION_RECONCILIATION_REQUIRED" || reason == "OBJECT_BINDING_CHANGED" || reason == "NATIVE_CONFIGURATION_UNKNOWN" || reason == "SCHEMA_CHANGED" {
				return e, apitypes.Fail(409, reason)
			}
			choice := resolutions[i.ID]
			delete(resolutions, i.ID)
			if reason != "" && choice == "" {
				return e, apitypes.Fail(409, "RESOLUTION_REQUIRED")
			}
			if choice == "keep-current" {
				continue
			}
			i.Before = normalize(p.VLAN)
			i.Dependency = p.Dependency
			next = append(next, i)
		}
		if len(resolutions) != 0 {
			return e, apitypes.Fail(422, "INVALID_RESOLUTION")
		}
		c.Intents = next
		c.BaseRevision = &s.Revision
	default:
		return e, apitypes.Fail(422, "UNSUPPORTED_CONFIGURATION")
	}
	c.Revision = repository.NewID()
	c.State = "dirty"
	if len(c.Intents) == 0 {
		c.State = "empty"
		c.Generation = nil
		c.BaseRevision = nil
	}
	e.Candidate = c
	e.Sequence++
	e.Seal = ""
	if e.Sequence <= 0 {
		return e, apitypes.Fail(429, "CANDIDATE_REVISION_LIMIT")
	}
	return e, Budget(e)
}

func Checks(c Candidate, s Snapshot) ([]Gate, []Diff) {
	v := Compare(c, s)
	checks := append([]Gate{}, v.Checks...)
	if len(c.Intents) == 0 {
		checks = append(checks, gate("EMPTY_CANDIDATE", "blocked", ""))
	}
	for _, i := range c.Intents {
		p, ok := s.Ports[i.Object.ManagementID]
		if !ok {
			continue
		}
		if !p.SchemaSupported || i.Value.Mode == nil || !slices.Contains(p.Modes, *i.Value.Mode) {
			checks = append(checks, gate("SCHEMA_UNSUPPORTED", "blocked", i.ID))
		}
		if p.Authority != "local-managed" {
			code := "OWNERSHIP_UNKNOWN"
			if p.Authority == "externally-controlled" {
				code = "EXTERNALLY_CONTROLLED"
			}
			checks = append(checks, gate(code, "blocked", i.ID))
		}
		if i.Before.Mode != nil && !slices.Contains([]string{"access", "trunk", "native-tagged", "native-untagged"}, *i.Before.Mode) || len(i.Before.CVLANs) != 0 {
			checks = append(checks, gate("NATIVE_SEMANTICS_UNPROVEN", "blocked", i.ID))
		}
		if i.Before.Mode == nil {
			checks = append(checks, gate("NATIVE_DEFAULT_MODE_PRESERVED", "allowed", i.ID))
		}
		if i.Value.Mode != nil && *i.Value.Mode == "dot1q-tunnel" {
			checks = append(checks, gate("QINQ_VALIDATOR_UNAVAILABLE", "blocked", i.ID))
		}
		for _, n := range append(append([]int{}, i.Before.Trunks...), i.Before.CVLANs...) {
			if n == 0 || n == 4095 {
				checks = append(checks, gate("RESERVED_VLAN_OBSERVED", "blocked", i.ID))
				break
			}
		}
		if i.Before.Tag != nil && (*i.Before.Tag == 0 || *i.Before.Tag == 4095) {
			checks = append(checks, gate("RESERVED_VLAN_OBSERVED", "blocked", i.ID))
		}
		if len(i.Value.Trunks) == 0 && i.Value.Mode != nil && *i.Value.Mode != "access" && *i.Value.Mode != "dot1q-tunnel" {
			checks = append(checks, gate("EMPTY_TRUNKS_MEANS_ALL_VLANS", "allowed", i.ID))
		}
	}
	checks = append(checks, gate("SAFE_APPLY_REQUIRED", "allowed", ""))
	return checks, v.Diff
}
func Passed(checks []Gate) bool {
	for _, g := range checks {
		if g.State != "allowed" {
			return false
		}
	}
	return true
}
