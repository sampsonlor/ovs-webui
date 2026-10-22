//go:build linux

package ovsdb

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"slices"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
)

type treeWitnessFence struct {
	Versions    map[string]string
	InputDigest string
	NextCfg     string
}

func treeWitnessOperations(fences []map[string]any, d discovered) ([]map[string]any, error) {
	if len(fences) != 8 {
		return nil, witnessUnproven
	}
	ops := slices.Clone(fences)
	registry := ovswitness.InputColumns()
	for _, table := range treeFenceTables {
		columns := append([]string{"_uuid"}, registry[table]...)
		if table == "Interface" {
			columns = append(columns, "ofport")
		}
		for _, name := range columns {
			if name != "_uuid" && d.native.Tables[table].Columns[name] == nil {
				return nil, witnessUnproven
			}
		}
		ops = append(ops, map[string]any{"op": "select", "table": table, "where": []any{}, "columns": columns})
	}
	data, err := json.Marshal(ops)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return nil, witnessUnproven
	}
	return ops, nil
}

func readTreeWitnessFence(conn net.Conn, r *bufio.Reader, ops []map[string]any, view inventory.ExecutionView, binding candidate.Binding) (treeWitnessFence, error) {
	empty := treeWitnessFence{}
	if len(ops) != 12 {
		return empty, witnessUnproven
	}
	params := []any{"Open_vSwitch"}
	for _, op := range ops {
		params = append(params, op)
	}
	if send(conn, map[string]any{"method": "transact", "params": params, "id": 5}) != nil {
		return empty, witnessUnproven
	}
	m, err := readMessage(r)
	if err != nil || m.Method != "" || string(m.ID) != "5" || len(m.Error) != 0 && string(m.Error) != "null" {
		return empty, witnessUnproven
	}
	return decodeTreeWitnessFence(m.Result, view, binding)
}

func decodeTreeWitnessFence(data []byte, view inventory.ExecutionView, binding candidate.Binding) (treeWitnessFence, error) {
	empty := treeWitnessFence{}
	var result []json.RawMessage
	if len(data) > 1<<20 || json.Unmarshal(data, &result) != nil || len(result) != 12 {
		return empty, witnessUnproven
	}
	versionBytes, err := json.Marshal(result[:8])
	if err != nil {
		return empty, witnessUnproven
	}
	versions, err := treeFenceVersions(versionBytes)
	if err != nil {
		return empty, err
	}
	count := 0
	for table, rows := range view.Observation.Rows {
		if !slices.Contains(treeFenceTables, table) {
			continue
		}
		for id := range rows {
			if versions[table+":"+id] == "" {
				return empty, witnessUnproven
			}
			count++
		}
	}
	if len(versions) != count {
		return empty, witnessUnproven
	}
	selected := map[string]map[string]bool{"Open_vSwitch": {view.Observation.Evidence.Root: true}, "Bridge": {binding.OVSUUID: true}, "Port": {}, "Interface": {}}
	for _, id := range nativeRefs(view.Observation.Rows["Bridge"][binding.OVSUUID].Values["ports"]) {
		selected["Port"][id] = true
		for _, iface := range nativeRefs(view.Observation.Rows["Port"][id].Values["interfaces"]) {
			selected["Interface"][iface] = true
		}
	}
	input := map[string][]map[string]any{}
	registry := ovswitness.InputColumns()
	for i, table := range treeFenceTables {
		var reply map[string]json.RawMessage
		var rows []map[string]any
		if json.Unmarshal(result[8+i], &reply) != nil || len(reply) != 1 || reply["rows"] == nil || string(reply["rows"]) == "null" {
			return empty, witnessUnproven
		}
		d := json.NewDecoder(bytes.NewReader(reply["rows"]))
		d.UseNumber()
		if d.Decode(&rows) != nil || len(rows) != len(view.Observation.Rows[table]) {
			return empty, witnessUnproven
		}
		seen := map[string]bool{}
		input[table] = nil
		for _, row := range rows {
			id, ok := row["_uuid"].([]any)
			if !ok || len(id) != 2 || id[0] != "uuid" {
				return empty, witnessUnproven
			}
			uuid, ok := id[1].(string)
			n := len(registry[table]) + 1
			if table == "Interface" {
				n++
			}
			if !ok || versions[table+":"+uuid] == "" || seen[uuid] || len(row) != n {
				return empty, witnessUnproven
			}
			for _, name := range registry[table] {
				if _, ok := row[name]; !ok {
					return empty, witnessUnproven
				}
			}
			seen[uuid] = true
			if selected[table][uuid] {
				input[table] = append(input[table], row)
			}
		}
		if len(input[table]) != len(selected[table]) {
			return empty, witnessUnproven
		}
	}
	hash, err := ovswitness.Digest(input)
	if err != nil {
		return empty, err
	}
	// next_cfg is deliberately outside the 52-column runtime input digest. Its
	// exact value is guarded, with cur_cfg, by the first wait in this transaction.
	root := view.Observation.Rows["Open_vSwitch"][view.Observation.Evidence.Root]
	next, ok := root.Values["next_cfg"].(string)
	n, err := number(next)
	if !ok || err != nil || strconv.FormatInt(n, 10) != next {
		return empty, witnessUnproven
	}
	return treeWitnessFence{Versions: versions, InputDigest: hash, NextCfg: next}, nil
}
