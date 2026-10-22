//go:build linux

package ovsdb

import (
	"bufio"
	"encoding/json"
	"net"
	"slices"
)

// A connection-scoped monitor detects persistent changes, including ABA,
// between the atomic fences. Row _version also advances for routine statistics
// updates and therefore cannot be used as a configuration revision. Legacy
// prepare preflights retain their original conservative version checks.
func treeExecutionMonitorRequests(d discovered) map[string]any {
	requests := map[string]any{}
	for _, table := range treeFenceTables {
		columns := []string{}
		for name, c := range d.native.Tables[table].Columns {
			if graphConfigurationColumn(table, name, c) || table == "Open_vSwitch" && (name == "next_cfg" || name == "cur_cfg") || table == "Interface" && name == "ofport" {
				columns = append(columns, name)
			}
		}
		slices.Sort(columns)
		requests[table] = []any{map[string]any{"columns": columns, "select": map[string]bool{"initial": false, "insert": true, "delete": true, "modify": true}}}
	}
	return requests
}

func startTreeExecutionMonitor(conn net.Conn, r *bufio.Reader, d discovered) error {
	// No initial image is requested: the following whole-table waits establish
	// the exact baseline. Every later notification rejects this attempt, even
	// when a second transaction restores the original configuration.
	data, err := call(conn, r, 6, "monitor", []any{"Open_vSwitch", "tree-execution-v1", treeExecutionMonitorRequests(d)})
	var result map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &result) != nil || result == nil || len(result) != 0 {
		return treeFenceChanged
	}
	return nil
}

func sameTreeExecutionFence(a, b treeWitnessFence) bool {
	return a.InputDigest == b.InputDigest && a.NextCfg == b.NextCfg
}
