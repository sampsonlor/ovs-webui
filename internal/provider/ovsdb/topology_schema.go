package ovsdb

import (
	"bytes"
	"encoding/json"
	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"slices"
)

func graphConfigurationColumn(table, name string, c *native.ColumnSchema) bool {
	if c == nil || c.Ephemeral() || bridgeStatusColumn(table, name) {
		return false
	}
	if !slices.Contains([]string{"Open_vSwitch", "Bridge", "Port", "Interface"}, table) {
		return false
	}
	// OVS persists this daemon-reported active-member MAC. It is runtime status,
	// not a desired Bond setting, and changes after creation/member updates.
	if table == "Port" && name == "bond_active_slave" {
		return false
	}
	if table == "Open_vSwitch" && slices.Contains([]string{"next_cfg", "cur_cfg", "ovs_version", "db_version", "system_type", "system_version", "datapaths", "dpdk_version", "dpdk_initialized", "statistics", "iface_types", "datapath_types"}, name) {
		return false
	}
	return true
}
func graphDefaults(d discovered) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, table := range []string{"Port", "Interface"} {
		out[table] = map[string]any{}
		for name, c := range d.native.Tables[table].Columns {
			if !graphConfigurationColumn(table, name, c) {
				continue
			}
			if name == "interfaces" {
				out[table][name] = []any{}
				continue
			}
			v, err := emptyNative(c)
			if err != nil {
				return nil
			}
			// Round-trip through the same exact native codec used by the monitor.
			b, _ := json.Marshal(v)
			var value any
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.UseNumber()
			if dec.Decode(&value) != nil {
				return nil
			}
			n, err := normalize(value, c)
			if err != nil {
				return nil
			}
			out[table][name] = n
		}
	}
	return out
}
