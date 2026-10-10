package ovsdb

import (
	"slices"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func spanningTreeColumn(table, name string) bool {
	return (table == "Bridge" || table == "Port") && slices.Contains([]string{"stp_enable", "rstp_enable", "other_config", "status", "rstp_status"}, name)
}

// Admit exact native boolean and string maps, independently of version labels.
// These checks only admit observation; they grant no configuration authority.
func spanningTreeConstraint(name string, c *native.ColumnSchema) bool {
	if c == nil || c.TypeObj == nil || c.TypeObj.Key == nil {
		return false
	}
	if name == "stp_enable" || name == "rstp_enable" {
		return c.Type == native.TypeBoolean && c.TypeObj.Key.Type == native.TypeBoolean
	}
	return c.Type == native.TypeMap && c.TypeObj.Key.Type == native.TypeString && c.TypeObj.Value != nil && c.TypeObj.Value.Type == native.TypeString
}

func spanningTreeMapKeys(table, name string) []string {
	if table != "Bridge" && table != "Port" {
		return nil
	}
	if name == "status" || name == "rstp_status" {
		return inventory.SpanningTreeRuntimeKeys(table, name)
	}
	// Bridge other_config is newly public: expose only approved spanning-tree
	// keys. Complete private configuration remains available to guarded writers.
	if table == "Bridge" && name == "other_config" {
		return inventory.SpanningTreeBridgeKeys
	}
	return nil
}
