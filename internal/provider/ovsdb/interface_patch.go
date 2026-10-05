package ovsdb

import native "github.com/ovn-kubernetes/libovsdb/ovsdb"

// A recognized configuration shape permits association observation only.
func patchColumnConstraint(table, name string, c *native.ColumnSchema) bool {
	if c == nil || c.TypeObj == nil || c.TypeObj.Key == nil {
		return false
	}
	t := c.TypeObj
	if t.Key.Type != native.TypeString || len(t.Key.Enum) != 0 {
		return false
	}
	if table == "Interface" && name == "options" {
		return c.Type == native.TypeMap && t.Value != nil && t.Value.Type == native.TypeString && len(t.Value.Enum) == 0 && t.Min() == 0 && t.Max() == native.Unlimited
	}
	return (table == "Interface" && name == "type" || table == "Bridge" && name == "datapath_type") && c.Type == native.TypeString && t.Value == nil && t.Min() == 1 && t.Max() == 1
}
