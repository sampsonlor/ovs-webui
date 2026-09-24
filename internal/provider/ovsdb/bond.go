package ovsdb

import (
	"slices"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func bondConstraint(name string, c *native.ColumnSchema) bool {
	t := c.TypeObj
	if !c.Mutable() || c.Ephemeral() || t == nil || t.Key == nil || t.Min() != 0 {
		return false
	}
	if name == "other_config" {
		return c.Type == native.TypeMap && t.Key.Type == native.TypeString && t.Value != nil && t.Value.Type == native.TypeString && len(t.Key.Enum) == 0 && len(t.Value.Enum) == 0 && t.Max() == native.Unlimited
	}
	if name != "lacp" && name != "bond_mode" || t.Key.Type != native.TypeString || t.Value != nil || t.Max() != 1 {
		return false
	}
	wanted := []string{"off", "active", "passive"}
	if name == "bond_mode" {
		wanted = []string{"active-backup", "balance-slb", "balance-tcp"}
	}
	for _, value := range wanted {
		if !slices.Contains(t.Key.Enum, any(value)) {
			return false
		}
	}
	return true
}

func bondRow(b *candidate.Bond) map[string]any {
	optional := func(s *string) []any {
		values := []any{}
		if s != nil {
			values = append(values, *s)
		}
		return []any{"set", values}
	}
	return map[string]any{"lacp": optional(b.LACP), "bond_mode": optional(b.Mode)}
}

func fallbackMutations(b *candidate.Bond) []any {
	changes := []any{[]any{"other_config", "delete", []any{"set", []any{"lacp-fallback-ab"}}}}
	if b.Fallback != nil {
		changes = append(changes, []any{"other_config", "insert", []any{"map", []any{[]any{"lacp-fallback-ab", *b.Fallback}}}})
	}
	return changes
}
