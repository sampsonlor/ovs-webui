package ovsdb

import native "github.com/ovn-kubernetes/libovsdb/ovsdb"

// Only the provider adapter interprets libovsdb schema types. Unknown/new
// constraints fail closed instead of being inferred from a product version.
func vlanConstraint(name string, c *native.ColumnSchema) (bool, []string) {
	t := c.TypeObj
	if !c.Mutable() || c.Ephemeral() || t == nil || t.Key == nil || t.Value != nil || t.Min() != 0 {
		return false, nil
	}
	if name == "vlan_mode" {
		if t.Key.Type != native.TypeString || t.Max() != 1 || len(t.Key.Enum) == 0 {
			return false, nil
		}
		modes := []string{}
		for _, v := range t.Key.Enum {
			s, ok := v.(string)
			if !ok {
				return false, nil
			}
			modes = append(modes, s)
		}
		return true, modes
	}
	if name != "tag" && name != "trunks" && name != "cvlans" {
		return false, nil
	}
	if t.Key.Type != native.TypeInteger || len(t.Key.Enum) != 0 {
		return false, nil
	}
	min, e1 := t.Key.MinInteger()
	max, e2 := t.Key.MaxInteger()
	if e1 != nil || e2 != nil || min > 1 || max < 4094 {
		return false, nil
	}
	if name == "tag" {
		return t.Max() == 1, nil
	}
	return t.Max() == native.Unlimited || t.Max() >= 4094, nil
}
