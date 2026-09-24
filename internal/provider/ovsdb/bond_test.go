package ovsdb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestBondCompilerPreservesOtherColumnsMapKeysAndNativeDefaults(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, view, envelope := executionFixture(t, version)
			i := &envelope.Candidate.Intents[0]
			lacp, mode, fallback := "active", "balance-tcp", "true"
			i.Operation, i.BeforeBond, i.Bond = "bond.configure", &candidate.Bond{}, &candidate.Bond{LACP: &lacp, Mode: &mode, Fallback: &fallback}
			port := view.Observation.Rows["Port"][i.Object.OVSUUID]
			port.Values["lacp"], port.Values["bond_mode"] = []any{}, []any{}
			for _, b := range view.Observation.Rows["Bridge"] {
				b.Values["stp_enable"], b.Values["rstp_enable"], b.Values["flood_vlans"] = false, false, []any{}
			}
			plan, err := compileExecution(repository.NewID(), strings.Repeat("b", 64), envelope, view, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if json.Unmarshal(plan.Native, &n) != nil {
				t.Fatal("plan")
			}
			updates, fieldMutations, uniqueMemberGuards := 0, 0, 0
			for _, op := range n.Operations {
				if op["op"] == "update" {
					updates++
					row := op["row"].(map[string]any)
					if len(row) != 2 || row["lacp"] == nil || row["bond_mode"] == nil {
						t.Fatal("broad Port replacement", row)
					}
				}
				if op["op"] == "mutate" {
					for _, raw := range op["mutations"].([]any) {
						mutation := raw.([]any)
						if mutation[0] == "other_config" {
							fieldMutations++
							b, _ := json.Marshal(mutation)
							if !strings.Contains(string(b), "lacp-fallback-ab") || strings.Contains(string(b), "unchanged") {
								t.Fatal("unrelated key touched", mutation)
							}
						}
					}
				}
				if op["op"] == "wait" && op["table"] == "Port" && len(op["columns"].([]any)) == 1 && op["columns"].([]any)[0] == "_uuid" {
					uniqueMemberGuards++
				}
			}
			if updates != 1 || fieldMutations != 2 || uniqueMemberGuards != 1 {
				t.Fatal(updates, fieldMutations, uniqueMemberGuards)
			}
			candidate.Reverse(i)
			reverse, err := compileExecution(repository.NewID(), strings.Repeat("c", 64), envelope, view, d)
			if err != nil {
				t.Fatal(err)
			}
			if json.Unmarshal(reverse.Native, &n) != nil {
				t.Fatal("reverse plan")
			}
			for _, op := range n.Operations {
				if op["op"] == "update" {
					b, _ := json.Marshal(op["row"])
					if string(b) != `{"bond_mode":["set",[]],"lacp":["set",[]]}` {
						t.Fatal("absent native values normalized", string(b))
					}
				}
			}
			proof, err := appliedProofOperations(plan, view, d)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range proof {
				if name := op.(map[string]any)["op"]; name != "wait" && name != "select" {
					t.Fatal("Applied proof mutates", name)
				}
			}
		})
	}
}

func TestBondSchemaRejectsUnsupportedTypesAndReadOnlyColumns(t *testing.T) {
	d := schema(t, "3.3.9")
	for _, name := range []string{"lacp", "bond_mode", "other_config"} {
		column := d.native.Tables["Port"].Columns[name]
		if !bondConstraint(name, column) {
			t.Fatal("native schema rejected", name)
		}
		copy := *column
		typ, key := *column.TypeObj, *column.TypeObj.Key
		key.Type = "boolean"
		typ.Key, copy.TypeObj = &key, &typ
		if bondConstraint(name, &copy) {
			t.Fatal("unsupported native type accepted", name)
		}
		data, err := json.Marshal(column)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if json.Unmarshal(data, &object) != nil {
			t.Fatal("column schema")
		}
		object["mutable"] = false
		data, _ = json.Marshal(object)
		if json.Unmarshal(data, &copy) != nil || bondConstraint(name, &copy) {
			t.Fatal("read-only column accepted", name)
		}
	}
}
