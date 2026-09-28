package inventory

import (
	"context"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestDeletionProjectionRequiresPrivateProvenanceForEveryRow(t *testing.T) {
	s, o, d, _ := fixture(t)
	root := repository.NewID()
	o.Evidence.Root = root
	o.Schema.BridgeCreation = true
	bid, pid, iid := repository.NewID(), repository.NewID(), repository.NewID()
	marker := strings.Repeat("a", 64)
	labels := func() map[string]any { return map[string]any{candidate.BridgeCreationMarker: marker} }
	o.Rows = Rows{
		"Open_vSwitch": {root: {UUID: root, Values: map[string]any{"bridges": []any{bid}, "external_ids": map[string]any{}}}},
		"Bridge":       {bid: {UUID: bid, Values: map[string]any{"name": "br-owned", "datapath_type": "system", "ports": []any{pid}, "external_ids": labels()}}},
		"Port":         {pid: {UUID: pid, Values: map[string]any{"name": "br-owned", "interfaces": []any{iid}, "external_ids": labels()}}},
		"Interface":    {iid: {UUID: iid, Values: map[string]any{"name": "br-owned", "type": "internal", "external_ids": labels(), "options": map[string]any{}}}},
	}
	d.Bindings = map[string]Binding{}
	for table, rows := range o.Rows {
		if table == "Open_vSwitch" {
			continue
		}
		for id := range rows {
			d.Bindings[Key(table, id)] = Binding{ManagementID: repository.NewID(), Table: table, UUID: id, State: "active"}
		}
	}
	read := func() candidate.Snapshot {
		t.Helper()
		s.install(o, d)
		out, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{{Table: "Bridge"}})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if err := s.SetLocalBridgeDeleteNames([]string{"br-owned"}); err != nil {
		t.Fatal(err)
	}
	if len(read().Deletion.Graphs) != 0 {
		t.Fatal("forged native marker adopted an external bridge")
	}
	for key, b := range d.Bindings {
		b.CreationMarker = marker
		d.Bindings[key] = b
	}
	got := read()
	mid := d.Bindings[Key("Bridge", bid)].ManagementID
	owned := got.Deletion.Graphs[mid]
	if owned.Graph.Port.OVSUUID != pid || !owned.RestoreCapacity || !got.Deletion.AllowedNames["br-owned"] {
		t.Fatal(got.Deletion)
	}
	o.Rows["Interface"][iid].Values["statistics"] = map[string]any{"rx_packets": "17"}
	if read().Deletion.Graphs[mid].Dependency != owned.Dependency {
		t.Fatal("volatile statistics changed deletion dependency")
	}
	o.Rows["Port"][pid].Values["tag"] = []any{"20"}
	if read().Deletion.Graphs[mid].Dependency == owned.Dependency {
		t.Fatal("configuration drift invisible")
	}
	b := d.Bindings[Key("Interface", iid)]
	b.CreationMarker = ""
	d.Bindings[Key("Interface", iid)] = b
	if len(read().Deletion.Graphs) != 0 {
		t.Fatal("partly owned graph accepted")
	}
}
