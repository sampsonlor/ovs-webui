package inventory

import (
	"context"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestInterfaceMTUAdmissionUsesRawOptionsIndependentAuthorityAndExactParents(t *testing.T) {
	s, o, d, c := fixture(t)
	var iface, port, bridge Row
	for _, r := range o.Rows["Port"] {
		port = r
	}
	for _, r := range o.Rows["Bridge"] {
		bridge = r
	}
	iface = o.Rows["Interface"][refs(port.Values["interfaces"])[0]]
	empty := true
	iface.InterfaceOptionsEmpty = &empty
	iface.Values["type"] = "internal"
	iface.Values["mtu_request"] = []any{"1500"}
	iface.Values["external_ids"] = map[string]any{}
	port.Values["name"] = iface.Values["name"]
	port.Values["interfaces"] = []any{iface.UUID}
	bridge.Values["datapath_type"] = "system"
	root := repository.NewID()
	o.Evidence.Root = root
	o.Rows["Open_vSwitch"][root] = Row{UUID: root, Values: map[string]any{"bridges": []any{bridge.UUID}, "external_ids": map[string]any{}}}
	for n, table := range o.Schema.Tables {
		if table.Name == "Interface" {
			o.Schema.Tables[n].Columns = append(table.Columns, Column{Name: "mtu_request", Monitored: true, Mutable: true, MTUCompatible: true})
		}
	}
	o.Rows["Interface"][iface.UUID] = iface
	b := d.Bindings[Key("Interface", iface.UUID)]
	binding := candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Interface", Generation: d.Generation}
	read := func() candidate.InterfaceMTU {
		t.Helper()
		s.install(o, d)
		snap, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding})
		if err != nil {
			t.Fatal(err)
		}
		return snap.Interfaces[b.ManagementID]
	}
	if candidate.MTUEditable(read()) {
		t.Fatal("Port grants conferred MTU authority")
	}
	if err := s.SetLocalMTUInterfaces([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	p := read()
	if !candidate.MTUEditable(p) {
		t.Fatal(p)
	}
	iface.Values["mtu"] = []any{"1800"}
	iface.Values["external_ids"] = map[string]any{"unrelated": "keep"}
	o.Rows["Interface"][iface.UUID] = iface
	if read().Dependency != p.Dependency {
		t.Fatal("runtime or unrelated marker invalidated dependency")
	}
	c.Capabilities = append(c.Capabilities, "workspace.write", "ovs.interface.mtu.write")
	item, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	if item.(map[string]any)["mtu_editable"] != true {
		t.Fatal(item)
	}
	for _, kind := range []string{"unreported-options", "nonempty-options", "local", "bond", "external", "default", "missing-column"} {
		t.Run(kind, func(t *testing.T) {
			original := iface
			originalValues := map[string]any{}
			for k, v := range iface.Values {
				originalValues[k] = v
			}
			iface.Values = originalValues
			switch kind {
			case "unreported-options":
				iface.InterfaceOptionsEmpty = nil
			case "nonempty-options":
				nonempty := false
				iface.InterfaceOptionsEmpty = &nonempty // public safe map still empty
			case "local":
				port.Values["name"] = bridge.Values["name"]
				iface.Values["name"] = bridge.Values["name"]
			case "bond":
				port.Values["interfaces"] = []any{iface.UUID, repository.NewID()}
			case "external":
				iface.Values["external_ids"] = map[string]any{"iface-id": "synthetic"}
			case "default":
				iface.Values["mtu_request"] = []any{}
			case "missing-column":
				delete(iface.Values, "mtu_request")
			}
			o.Rows["Interface"][iface.UUID] = iface
			if candidate.MTUEditable(read()) {
				t.Fatal("unsupported interface editable")
			}
			iface = original
			o.Rows["Interface"][iface.UUID] = iface
			port.Values["name"] = iface.Values["name"]
			port.Values["interfaces"] = []any{iface.UUID}
		})
	}
	if err := s.SetLocalMTUInterfaces([]string{b.ManagementID, b.ManagementID}); err == nil {
		t.Fatal("duplicate grant accepted")
	}
}

func TestMTUDefaultProjectionSealsContributorsAndRejectsUnknownGraph(t *testing.T) {
	s, o, d, _ := fixture(t)
	var port, bridge Row
	for _, r := range o.Rows["Port"] {
		port = r
	}
	for _, r := range o.Rows["Bridge"] {
		bridge = r
	}
	iface := o.Rows["Interface"][refs(port.Values["interfaces"])[0]]
	empty := true
	iface.InterfaceOptionsEmpty = &empty
	iface.Values["type"] = "internal"
	iface.Values["mtu_request"] = []any{}
	iface.Values["mtu"] = []any{"1800"}
	iface.Values["external_ids"] = map[string]any{}
	port.Values["name"] = iface.Values["name"]
	port.Values["interfaces"] = []any{iface.UUID}
	port.Values["external_ids"] = map[string]any{}
	bridge.Values["datapath_type"] = "system"
	root := repository.NewID()
	o.Evidence.Root = root
	o.Rows["Open_vSwitch"][root] = Row{UUID: root, Values: map[string]any{"bridges": []any{bridge.UUID}, "external_ids": map[string]any{}}}
	for n, table := range o.Schema.Tables {
		if table.Name == "Interface" {
			o.Schema.Tables[n].Columns = append(table.Columns, Column{Name: "mtu_request", Monitored: true, Mutable: true, MTUCompatible: true})
		}
	}
	b := d.Bindings[Key("Interface", iface.UUID)]
	binding := candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Interface", Generation: d.Generation}
	peerID, peerPortID := repository.NewID(), repository.NewID()
	peer := Row{UUID: peerID, InterfaceOptionsEmpty: &empty, Values: map[string]any{"name": "peer", "type": "internal", "options": map[string]any{}, "mtu_request": []any{"1800"}, "mtu": []any{"1800"}, "ofport": []any{"2"}, "error": []any{}, "external_ids": map[string]any{}}}
	peerPort := Row{UUID: peerPortID, Values: map[string]any{"name": "peer", "interfaces": []any{peerID}, "external_ids": map[string]any{}}}
	ib := b
	ib.UUID = peerID
	ib.ManagementID = repository.NewID()
	d.Bindings[Key("Interface", peerID)] = ib
	pb := d.Bindings[Key("Port", port.UUID)]
	pb.UUID = peerPortID
	pb.ManagementID = repository.NewID()
	d.Bindings[Key("Port", peerPortID)] = pb
	bridge.Values["ports"] = []any{port.UUID, peerPortID}
	o.Rows["Interface"][iface.UUID] = iface
	o.Rows["Port"][port.UUID] = port
	o.Rows["Interface"][peerID] = peer
	o.Rows["Port"][peerPortID] = peerPort
	o.Rows["Bridge"][bridge.UUID] = bridge
	if err := s.SetLocalMTUInterfaces([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	read := func() candidate.InterfaceMTU {
		t.Helper()
		s.install(o, d)
		snap, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding})
		if err != nil {
			t.Fatal(err)
		}
		return snap.Interfaces[b.ManagementID]
	}
	p := read()
	if !candidate.MTUEditable(p) || p.Requested != nil || p.Default == nil || p.Default.MTU != 1800 {
		t.Fatal(p)
	}
	peer.Values["external_ids"] = map[string]any{"unrelated": "keep"}
	o.Rows["Interface"][peerID] = peer
	if read().Default.Dependency != p.Default.Dependency {
		t.Fatal("unrelated peer metadata changed dependency")
	}
	peer.Values["mtu_request"] = []any{"2400"}
	peer.Values["mtu"] = []any{"2400"}
	o.Rows["Interface"][peerID] = peer
	changed := read()
	if changed.Default == nil || changed.Default.MTU != 2400 || changed.Default.Dependency == p.Default.Dependency || candidate.MTUEditable(changed) {
		t.Fatal("default instability unproven", changed)
	}
	peer.Values["mtu_request"] = []any{"1800"}
	peer.Values["mtu"] = []any{"1800"}
	for _, kind := range []string{"empty-contributors", "unapplied-peer", "missing-mtu", "invalid-ofport", "error", "unknown-options", "external", "unsupported-type"} {
		t.Run(kind, func(t *testing.T) {
			current := peer
			current.Values = map[string]any{}
			for key, value := range peer.Values {
				current.Values[key] = value
			}
			switch kind {
			case "empty-contributors":
				current.Values["mtu_request"] = []any{}
			case "unapplied-peer":
				current.Values["mtu"] = []any{"1600"}
			case "missing-mtu":
				delete(current.Values, "mtu")
			case "invalid-ofport":
				current.Values["ofport"] = []any{"-1"}
			case "error":
				current.Values["error"] = []any{"synthetic"}
			case "unknown-options":
				current.InterfaceOptionsEmpty = nil
			case "external":
				current.Values["external_ids"] = map[string]any{"iface-id": "synthetic"}
			case "unsupported-type":
				current.Values["type"] = "patch"
			}
			o.Rows["Interface"][peerID] = current
			if read().Default != nil || candidate.MTUEditable(read()) {
				t.Fatal("unproven automatic MTU admitted", kind)
			}
		})
	}
	for _, value := range []any{nil, []any{"0"}, []any{"1500", "1800"}, []any{1500}, []any{"unknown"}} {
		if _, known := nativeMTURequest(value); known {
			t.Fatal("unknown value accepted", value)
		}
	}
}
