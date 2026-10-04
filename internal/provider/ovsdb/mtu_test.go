package ovsdb

import (
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestMTUCompilationAndAppliedProofAcrossNativeSchemas(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			d, view, e := executionFixture(t, version)
			prior := e.Candidate.Intents[0]
			port := view.Observation.Rows["Port"][prior.Object.OVSUUID]
			id := nativeRefs(port.Values["interfaces"])[0]
			iface := view.Observation.Rows["Interface"][id]
			empty := true
			iface.InterfaceOptionsEmpty = &empty
			iface.Values["mtu_request"] = []any{"1500"}
			iface.Values["mtu"] = []any{"1500"}
			iface.Values["error"] = []any{}
			view.Observation.Rows["Interface"][id] = iface
			i := candidate.StoredIntent{Operation: candidate.InterfaceMTUSet, Object: candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: "Interface", Generation: prior.Object.Generation}, MTU: &candidate.MTUChange{Before: candidate.MTUPointer(1500), After: candidate.MTUPointer(2000), Port: prior.Object}}
			for uuid := range view.Observation.Rows["Bridge"] {
				i.MTU.Bridge = candidate.Binding{OVSUUID: uuid}
			}
			e.Candidate.Intents = []candidate.StoredIntent{i}
			p, err := compileExecution(repository.NewID(), "marker", e, view, d)
			if err != nil {
				t.Fatal(err)
			}
			var n nativePlan
			if json.Unmarshal(p.Native, &n) != nil {
				t.Fatal("plan decode")
			}
			updates := 0
			for _, op := range n.Operations {
				if op["op"] == "update" {
					updates++
					if op["table"] != "Interface" || len(op["row"].(map[string]any)) != 1 || op["row"].(map[string]any)["mtu_request"] == nil {
						t.Fatal("unrelated field updated", op)
					}
				}
			}
			if updates != 1 {
				t.Fatal(n)
			}
			iface.Values["mtu_request"] = []any{"2000"}
			iface.Values["mtu"] = []any{"2000"}
			ops, err := appliedProofOperations(p, view, d)
			if err != nil {
				t.Fatal(err)
			}
			actual := false
			for _, op := range ops {
				o := op.(map[string]any)
				if o["op"] != "wait" && o["op"] != "select" {
					t.Fatal("proof writes live fields")
				}
				if columns, ok := o["columns"].([]string); ok {
					for _, col := range columns {
						actual = actual || col == "mtu"
					}
				}
			}
			if !actual {
				t.Fatal("actual MTU omitted from proof")
			}
			*iface.InterfaceOptionsEmpty = false
			if _, err = compileExecution(repository.NewID(), "marker", e, view, d); err == nil {
				t.Fatal("empty safe subset treated as raw empty map")
			}
			if !mtuConstraint(d.native.Tables["Interface"].Columns["mtu_request"]) {
				t.Fatal("supported native constraint")
			}
		})
	}
}

func TestMTUClearPlanPreservesEmptySetAndGuardsPeerMTUWithoutWritingPeers(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		d, v, e := executionFixture(t, version)
		prior := e.Candidate.Intents[0]
		port := v.Observation.Rows["Port"][prior.Object.OVSUUID]
		id := nativeRefs(port.Values["interfaces"])[0]
		iface := v.Observation.Rows["Interface"][id]
		empty := true
		iface.InterfaceOptionsEmpty = &empty
		iface.Values["mtu_request"] = []any{"2400"}
		iface.Values["mtu"] = []any{"2400"}
		v.Observation.Rows["Interface"][id] = iface
		peerID, peerPortID := repository.NewID(), repository.NewID()
		v.Observation.Rows["Interface"][peerID] = inventory.Row{UUID: peerID, InterfaceOptionsEmpty: &empty, Values: map[string]any{"name": "peer", "type": "internal", "options": map[string]any{}, "mtu_request": []any{"1800"}, "mtu": []any{"1800"}, "ofport": []any{"2"}, "error": []any{}, "external_ids": map[string]any{}}}
		v.Observation.Rows["Port"][peerPortID] = inventory.Row{UUID: peerPortID, Values: map[string]any{"name": "peer", "interfaces": []any{peerID}, "external_ids": map[string]any{}}}
		binding := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: id, Table: "Interface", Generation: v.Candidate.Generation}
		ctx := &candidate.MTUDefault{MTU: 1800, Dependency: "captured", Bindings: []candidate.Binding{{ManagementID: repository.NewID(), OVSUUID: peerPortID, Table: "Port", Generation: v.Candidate.Generation}, {ManagementID: repository.NewID(), OVSUUID: peerID, Table: "Interface", Generation: v.Candidate.Generation}}}
		v.Candidate.Interfaces = map[string]candidate.InterfaceMTU{binding.ManagementID: {Default: ctx}}
		m := &candidate.MTUChange{Before: candidate.MTUPointer(2400), After: nil, Port: prior.Object, Default: ctx}
		for uuid := range v.Observation.Rows["Bridge"] {
			m.Bridge = candidate.Binding{OVSUUID: uuid}
		}
		e.Candidate.Intents = []candidate.StoredIntent{{Operation: candidate.InterfaceMTUClear, Object: binding, MTU: m}}
		p, err := compileExecution(repository.NewID(), "marker", e, v, d)
		if err != nil {
			t.Fatal(err)
		}
		var n nativePlan
		if json.Unmarshal(p.Native, &n) != nil {
			t.Fatal("native plan")
		}
		guarded, targetGuarded, emptyWrite := false, false, false
		for _, op := range n.Operations {
			if op["op"] == "update" {
				if op["table"] != "Interface" || len(op["row"].(map[string]any)) != 1 {
					t.Fatal("broad write", op)
				}
				value := op["row"].(map[string]any)["mtu_request"].([]any)
				emptyWrite = value[0] == "set" && len(value[1].([]any)) == 0
			}
			if op["op"] == "wait" && op["table"] == "Interface" {
				row := op["rows"].([]any)[0].(map[string]any)
				for _, col := range op["columns"].([]any) {
					guarded = guarded || col == "mtu"
					targetGuarded = targetGuarded || col == "mtu" && row["_uuid"].([]any)[1] == id
				}
			}
		}
		if !guarded || !targetGuarded || !emptyWrite {
			t.Fatal("empty request or runtime dependency missing")
		}
		iface.Values["mtu"] = []any{"1500"}
		if _, err = compileExecution(repository.NewID(), "marker", e, v, d); err == nil {
			t.Fatal("forward compile admitted unproven original device")
		}
		iface.Values["mtu_request"], iface.Values["mtu"], iface.Values["error"] = []any{}, []any{"1800"}, []any{}
		if _, err = appliedProofOperations(p, v, d); err != nil || *e.Candidate.Intents[0].MTU.Before != 2400 {
			t.Fatal("Applied proof required original device or mutated journal", err)
		}
		compensation := e
		compensation.Candidate.Intents = append([]candidate.StoredIntent{}, e.Candidate.Intents...)
		candidate.Reverse(&compensation.Candidate.Intents[0])
		iface.Values["mtu"] = []any{"2400"} // Clear has committed but the device has not applied it.
		rollback, err := compileExecution(repository.NewID(), "rollback", compensation, v, d)
		if err != nil || json.Unmarshal(rollback.Native, &n) != nil {
			t.Fatal("unapplied clear cannot restore original request", err)
		}
		for _, op := range n.Operations {
			if op["op"] == "wait" && op["table"] == "Interface" {
				row := op["rows"].([]any)[0].(map[string]any)
				if row["_uuid"].([]any)[1] == id {
					for _, col := range op["columns"].([]any) {
						if col == "mtu" {
							t.Fatal("compensation waits for unapplied after-device")
						}
					}
				}
			}
		}
		copyContext := *ctx
		copyContext.Dependency = "changed"
		v.Candidate.Interfaces[binding.ManagementID] = candidate.InterfaceMTU{Default: &copyContext}
		if _, err = compileExecution(repository.NewID(), "marker", e, v, d); err == nil {
			t.Fatal("recompiled with different default")
		}
	}
}
func TestMTUProviderDoesNotInferRawOptionEmptinessFromSafeSubset(t *testing.T) {
	d := schema(t, "3.3.9")
	id := repository.NewID()
	rows := inventory.Rows{}
	for n, options := range []any{[]any{"map", []any{[]any{"opaque-native", "synthetic"}}}, []any{"map", []any{}}} {
		change := map[string]any{"new": map[string]any{"options": options}}
		if n == 0 {
			change["new"].(map[string]any)["name"] = "synthetic"
		} else {
			change["old"] = map[string]any{"options": []any{"map", []any{[]any{"opaque-native", "synthetic"}}}}
		}
		data, _ := json.Marshal(map[string]any{"Interface": map[string]any{id: change}})
		if err := update(d, rows, data, n == 0); err != nil {
			t.Fatal(err)
		}
		r := rows["Interface"][id]
		if r.InterfaceOptionsEmpty == nil || *r.InterfaceOptionsEmpty != (n == 1) || len(r.Values["options"].(map[string]any)) != 0 {
			t.Fatal(r)
		}
	}
}
