package inventory

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestCreationProjectionReservesInventoryCapacityAndKeepsFieldPolicyLocal(t *testing.T) {
	s, o, d, _ := fixture(t)
	root := repository.NewID()
	o.Evidence.Root = root
	o.Schema.BridgeCreation = true
	o.Rows["Open_vSwitch"][root] = Row{UUID: root, Values: map[string]any{"external_ids": map[string]any{}}}
	s.install(o, d)
	read := func(bindings []candidate.Binding) candidate.Snapshot {
		t.Helper()
		v, err := s.CandidateSnapshot(context.Background(), bindings)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := read(nil)
	if err := s.SetLocalBridgeNames([]string{"br-new"}); err != nil {
		t.Fatal(err)
	}
	after := read(nil)
	if before.Policy != after.Policy || before.Revision != after.Revision {
		t.Fatal("creation policy invalidated unrelated fields")
	}
	binding := []candidate.Binding{{Table: "Bridge"}}
	got := read(binding)
	if !got.Creation.Supported || !got.Creation.Capacity || !got.Creation.AllowedNames["br-new"] {
		t.Fatal(got.Creation)
	}
	for len(o.Rows["Interface"]) < MaxRows-5 {
		id := repository.NewID()
		o.Rows["Interface"][id] = Row{UUID: id, Values: map[string]any{"name": id}}
	}
	s.install(o, d)
	if read(binding).Creation.Capacity {
		t.Fatal("three rows would exceed monitor limit")
	}
	// Row bytes, not just row count, also need recovery headroom.
	o.Rows["Interface"] = map[string]Row{}
	o.Rows["Bridge"] = map[string]Row{"large": {UUID: "large", Values: map[string]any{"name": "large", "synthetic": strings.Repeat("x", MaxSnapshotBytes-2*MaxRowBytes)}}}
	s.install(o, d)
	if read(binding).Creation.Capacity {
		t.Fatal("byte budget has no recovery headroom")
	}
}
