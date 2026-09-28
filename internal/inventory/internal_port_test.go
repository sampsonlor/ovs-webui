package inventory

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func TestInternalPortProjectionPinsParentAndExcludesStatus(t *testing.T) {
	s, o, d, _ := fixture(t)
	root, bid, pid, iid := repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID()
	o.Evidence.Root = root
	o.Schema.BridgeCreation = true
	o.Schema.InternalPortCreation = true
	o.Rows = Rows{
		"Open_vSwitch": {root: {UUID: root, Values: map[string]any{"bridges": []any{bid}, "external_ids": map[string]any{}}}},
		"Bridge":       {bid: {UUID: bid, Values: map[string]any{"name": "br-parent", "datapath_type": "system", "ports": []any{pid}, "controller": []any{}, "stp_enable": false, "rstp_enable": false, "external_ids": map[string]any{}}}},
		"Port":         {pid: {UUID: pid, Values: map[string]any{"name": "br-parent", "interfaces": []any{iid}, "external_ids": map[string]any{}}}},
		"Interface":    {iid: {UUID: iid, Values: map[string]any{"name": "br-parent", "type": "internal", "options": map[string]any{}, "external_ids": map[string]any{}}}},
	}
	d.Bindings = map[string]Binding{}
	for table, rows := range o.Rows {
		if table == "Open_vSwitch" {
			continue
		}
		for id := range rows {
			d.Bindings[Key(table, id)] = Binding{ManagementID: repository.NewID(), UUID: id, Table: table, State: "active"}
		}
	}
	mid := d.Bindings[Key("Bridge", bid)].ManagementID
	read := func() candidate.Snapshot {
		t.Helper()
		s.install(o, d)
		v, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{{Table: "Bridge"}})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := s.SetLocalInternalPortTargets([]string{mid + ":pi-new"}); err != nil {
		t.Fatal(err)
	}
	prior := read()
	p := prior.InternalPorts.Parents[mid]
	if !p.Eligible || !prior.InternalPorts.Supported || !prior.InternalPorts.Capacity || !prior.InternalPorts.Targets[mid+":pi-new"] || len(p.Members) != 1 {
		t.Fatal(prior.InternalPorts)
	}
	o.Rows["Interface"][iid].Values["ofport"] = "65534"
	o.Rows["Interface"][iid].Values["link_state"] = []any{"up"}
	if read().InternalPorts.Parents[mid].Dependency != p.Dependency {
		t.Fatal("status invalidated draft")
	}
	o.Rows["Port"][pid].Values["tag"] = []any{"20"}
	if read().InternalPorts.Parents[mid].Dependency == p.Dependency {
		t.Fatal("local-port change ignored")
	}
	o.Rows["Bridge"][bid].Values["controller"] = []any{repository.NewID()}
	if read().InternalPorts.Parents[mid].Eligible {
		t.Fatal("controlled parent allowed")
	}
	o.Rows["Bridge"][bid].Values["controller"] = []any{}
	b := d.Bindings[Key("Bridge", bid)]
	b.ManagementID = repository.NewID()
	d.Bindings[Key("Bridge", bid)] = b
	if read().InternalPorts.Targets[b.ManagementID+":pi-new"] {
		t.Fatal("new identity inherited grant")
	}
	for _, bad := range [][]string{{"br-parent:pi-new"}, {mid + ":br-parent:extra"}, {mid + ":pi-new", mid + ":pi-new"}} {
		if s.SetLocalInternalPortTargets(bad) == nil {
			t.Fatal("invalid root policy")
		}
	}
}
