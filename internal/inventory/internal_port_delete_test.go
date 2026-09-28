package inventory

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"strings"
	"testing"
)

func TestInternalPortDeletionProjectionRequiresPrivateChildProvenance(t *testing.T) {
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
	child, ci := repository.NewID(), repository.NewID()
	marker := strings.Repeat("a", 64)
	labels := func() map[string]any { return map[string]any{candidate.BridgeCreationMarker: marker} }
	o.Rows["Bridge"][bid].Values["ports"] = []any{pid, child}
	o.Rows["Port"][child] = Row{UUID: child, Values: map[string]any{"name": "pi-owned", "interfaces": []any{ci}, "vlan_mode": []any{"access"}, "tag": []any{"20"}, "trunks": []any{}, "cvlans": []any{}, "external_ids": labels()}}
	o.Rows["Interface"][ci] = Row{UUID: ci, Values: map[string]any{"name": "pi-owned", "type": "internal", "options": map[string]any{}, "external_ids": labels()}}
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

	if err := s.SetLocalInternalPortDeleteTargets([]string{mid + ":pi-owned"}); err != nil {
		t.Fatal(err)
	}
	if len(read().PortDeletions.Graphs) != 0 {
		t.Fatal("forged native marker adopted external children")
	}
	for _, pair := range [][2]string{{"Port", child}, {"Interface", ci}} {
		key := Key(pair[0], pair[1])
		b := d.Bindings[key]
		b.CreationMarker = marker
		d.Bindings[key] = b
	}
	childMID := d.Bindings[Key("Port", child)].ManagementID
	owned := read().PortDeletions.Graphs[childMID]
	if owned.Graph.Port.OVSUUID != child || !owned.RestoreCapacity || len(owned.Graph.Configuration.Members) != 1 || owned.Graph.Configuration.Members[0].OVSUUID != pid || !read().PortDeletions.Targets[mid+":pi-owned"] {
		t.Fatal(owned)
	}
	o.Rows["Interface"][ci].Values["statistics"] = map[string]any{"rx_packets": "17"}
	if read().PortDeletions.Graphs[childMID].Dependency != owned.Dependency {
		t.Fatal("status caused drift")
	}
	o.Rows["Interface"][ci].Values["options"] = map[string]any{"changed": "true"}
	if read().PortDeletions.Graphs[childMID].Dependency == owned.Dependency {
		t.Fatal("configuration drift ignored")
	}
	b := d.Bindings[Key("Interface", ci)]
	b.CreationMarker = ""
	d.Bindings[Key("Interface", ci)] = b
	if len(read().PortDeletions.Graphs) != 0 {
		t.Fatal("partly owned pair accepted")
	}
}
