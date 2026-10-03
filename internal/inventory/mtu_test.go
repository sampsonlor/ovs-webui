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
