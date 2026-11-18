package ovsdb

import (
	"encoding/json"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
)

const flowSampleCollectorTable = "Flow_Sample_Collector_Set"

// A write requires all persistent columns needed to restore a garbage-collected
// exporter, plus a closed set of incoming references that can be guarded.
func discoverTelemetryWrites(d *discovered, raw map[string]any) {
	d.public.TelemetryWrite = map[string]bool{}
	d.telemetryPrivate = map[string]map[string]bool{}
	root := d.native.Tables["Open_vSwitch"]
	bridge := d.native.Tables["Bridge"]
	rootTable, err := d.native.IsRoot("Open_vSwitch")
	bridgeRoot, bridgeErr := d.native.IsRoot("Bridge")
	if err != nil || !rootTable || bridgeErr != nil || bridgeRoot ||
		!mirrorReference(root.Columns["bridges"], multicastRawColumnType(raw, "Open_vSwitch", "bridges"), "Bridge", "strong", native.Unlimited) ||
		!multicastMapConstraint(root.Columns["external_ids"], multicastRawColumnType(raw, "Open_vSwitch", "external_ids")) ||
		!multicastMapConstraint(root.Columns["other_config"], multicastRawColumnType(raw, "Open_vSwitch", "other_config")) ||
		!multicastMapConstraint(bridge.Columns["external_ids"], multicastRawColumnType(raw, "Bridge", "external_ids")) {
		return
	}
	for _, name := range []string{"next_cfg", "cur_cfg"} {
		column := root.Columns[name]
		if column == nil || !column.Mutable() || !telemetryColumnShape(column, multicastRawColumnType(raw, "Open_vSwitch", name), telemetryShape{atom: native.TypeInteger, min: 1, max: 1}) {
			return
		}
	}
	for protocol, table := range telemetryTables {
		if !d.public.Telemetry[protocol] || !telemetryIncomingSchema(*d, table, protocol) {
			continue
		}
		exporter := d.native.Tables[table]
		read := d.telemetryColumns[table]
		externalIDs := exporter.Columns["external_ids"]
		expectedReadColumns := 1 + len(telemetryFields[table])
		if table == "IPFIX" {
			expectedReadColumns++
		}
		if len(exporter.Indexes) != 0 || len(exporter.Columns) != len(read)+1 ||
			len(read) != expectedReadColumns ||
			!multicastMapConstraint(externalIDs, multicastRawColumnType(raw, table, "external_ids")) {
			continue
		}
		complete := map[string]bool{}
		for name, col := range exporter.Columns {
			if col != nil && col.Mutable() && !col.Ephemeral() && (name == "external_ids" || read[name]) {
				complete[name] = true
			}
		}
		if len(complete) != len(exporter.Columns) {
			continue
		}
		if protocol == "ipfix" {
			flow := d.native.Tables[flowSampleCollectorTable]
			sourceRoot, err := d.native.IsRoot(flowSampleCollectorTable)
			if err != nil || !sourceRoot || !mirrorReference(flow.Columns["ipfix"], multicastRawColumnType(raw, flowSampleCollectorTable, "ipfix"), table, "strong", 1) {
				continue
			}
			d.telemetryPrivate[flowSampleCollectorTable] = map[string]bool{"ipfix": true}
		}
		d.public.TelemetryWrite[protocol] = true
		d.telemetryPrivate[table] = complete
	}
}

func telemetryIncomingSchema(d discovered, table, protocol string) bool {
	for source, columns := range d.native.Tables {
		for name, column := range columns.Columns {
			if column == nil || column.TypeObj == nil {
				return false
			}
			for _, base := range []*native.BaseType{column.TypeObj.Key, column.TypeObj.Value} {
				if base == nil || base.Type != native.TypeUUID {
					continue
				}
				target, _ := base.RefTable()
				if target == table && !(source == "Bridge" && name == protocol) &&
					!(protocol == "ipfix" && source == flowSampleCollectorTable && name == "ipfix") {
					return false
				}
			}
		}
	}
	return true
}

// Unbounded metadata is still readable through the existing safe projection.
// An oversized full value makes that exporter ineligible for compensation.
func boundedPrivateTelemetry(table string, value any) any {
	if telemetryTable(table) {
		data, err := json.Marshal(value)
		if err != nil || len(data) > 2048 {
			return nil
		}
	}
	return value
}
