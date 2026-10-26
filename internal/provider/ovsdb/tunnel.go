package ovsdb

func staticTunnelSchemaSupported(d discovered, raw map[string]any) bool {
	if !d.public.BridgeCreation || !d.public.InternalPortCreation {
		return false
	}
	iface := d.native.Tables["Interface"]
	typ := iface.Columns["type"]
	if typ == nil || !typ.Mutable() || typ.Ephemeral() {
		return false
	}
	value := multicastRawColumnType(raw, "Interface", "type")
	if value != "string" {
		m, ok := value.(map[string]any)
		if !ok || len(m) != 1 || m["type"] != "string" {
			return false
		}
	}
	if !multicastMapConstraint(iface.Columns["options"], multicastRawColumnType(raw, "Interface", "options")) {
		return false
	}
	for _, table := range d.public.Tables {
		if table.Name != "Open_vSwitch" {
			continue
		}
		for _, column := range table.Columns {
			if column.Name == "iface_types" && column.Monitored && column.Type == "set" {
				return true
			}
		}
	}
	return false
}
