package ovsdb

import (
	"slices"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Recognition grants read semantics only, never write authority or Applied.
func interfaceConfigurationConstraint(name string, c *native.ColumnSchema) bool {
	if !slices.Contains(inventory.InterfaceConfigurationColumns, name) || c == nil || c.TypeObj == nil {
		return false
	}
	t := c.TypeObj
	if t.Key == nil || t.Key.Type != native.TypeInteger || t.Value != nil || len(t.Key.Enum) != 0 || t.Max() != 1 {
		return false
	}
	minimum, e1 := t.Key.MinInteger()
	maximum, e2 := t.Key.MaxInteger()
	if e1 != nil || e2 != nil {
		return false
	}
	if name == "ofport_request" {
		return t.Min() == 0 && minimum == 1 && maximum == 65279
	}
	return c.Type == native.TypeInteger && t.Min() == 1 && minimum == 0
}

func monitoredColumn(d discovered, table, name string) bool {
	c := d.native.Tables[table].Columns[name]
	return c != nil && slices.Contains(selected[table], name) &&
		(table != "Interface" || !slices.Contains(inventory.InterfaceConfigurationColumns, name) || interfaceConfigurationConstraint(name, c))
}
