package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestInterfaceNativeFieldsPermissionsAndRelationships(t *testing.T) {
	s, o, d, c := fixture(t)
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	var b Binding
	for _, binding := range d.Bindings {
		if binding.Table == "Interface" {
			b = binding
			break
		}
	}
	row := o.Rows["Interface"][b.UUID]
	row.Values["type"] = ""
	row.Values["mtu"] = "1500"
	row.Values["mtu_request"] = []any{}
	row.Values["ofport"] = "-1"
	row.Values["status"] = map[string]any{"driver_name": "synthetic-driver", "bus_info": "0000:01:00.0"}
	for j := range o.Schema.Tables {
		if o.Schema.Tables[j].Name == "Interface" {
			for _, name := range []string{"mtu", "mtu_request", "ofport", "status"} {
				o.Schema.Tables[j].Columns = append(o.Schema.Tables[j].Columns, Column{Name: name, Monitored: true, Mutable: true})
			}
		}
	}
	read := func() map[string]any {
		t.Helper()
		s.install(o, d)
		result, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c)
		if err != nil {
			t.Fatal(err)
		}
		item := result.(map[string]any)
		body, _ := json.Marshal(item)
		op, _, _ := contract.Match("GET", "/api/v1/interfaces/"+b.ManagementID)
		if err := op.ValidateResponse(200, body); err != nil {
			t.Fatal(err)
		}
		return item
	}
	item := read()
	fields := item["fields"].(map[string]any)
	if item["interface_type"] != "default" || item["port_kind"] != "bond" || item["local_interface"] != false || item["bridge_ref"].(map[string]any)["kind"] != "bridge" {
		t.Fatal(item)
	}
	for _, name := range []string{"mtu", "status", "ofport"} {
		f := fields[name].(map[string]any)
		if f["editable"] != false || f["source"].(map[string]any)["authority"] != "ovs-vswitchd-observation" {
			t.Fatal(f)
		}
	}
	if fields["mtu_request"].(map[string]any)["source"].(map[string]any)["authority"] != "ovsdb-configuration" {
		t.Fatal(fields)
	}
	revision := item["config_revision"]
	row.Values["mtu"] = "9000"
	row.Values["status"] = map[string]any{}
	if read()["config_revision"] != revision {
		t.Fatal("operational change changed configuration revision")
	}
	row.Values["mtu_request"] = "9000"
	if read()["config_revision"] == revision {
		t.Fatal("request change did not change revision")
	}
	c.Capabilities = []string{"inventory.read", "state.read"}
	item = read()
	fields = item["fields"].(map[string]any)
	if item["internal"] != nil || item["local_interface"] != nil || item["interface_type"] != "unknown" || fields["type"].(map[string]any)["availability"] != "withheld" || fields["mtu_request"].(map[string]any)["value"] != nil || fields["mtu"].(map[string]any)["value"] != "9000" {
		t.Fatal("configuration permission leak", item)
	}
	row.Values["external_ids"] = map[string]any{"iface-id": "synthetic-owned"}
	if read()["ownership"] != "externally-controlled" {
		t.Fatal("external control hidden")
	}
}

func TestInterfaceAmbiguousParentsCannotFabricateLinks(t *testing.T) {
	s, o, d, c := fixture(t)
	var b Binding
	for _, binding := range d.Bindings {
		if binding.Table == "Interface" {
			b = binding
			break
		}
	}
	original, ok := parent(s.current, "Port", "interfaces", b.UUID)
	if !ok {
		t.Fatal("fixture")
	}
	duplicate := repository.NewID()
	o.Rows["Port"][duplicate] = Row{UUID: duplicate, Values: map[string]any{"interfaces": []any{b.UUID}}}
	s.install(o, d)
	if _, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c); err == nil {
		t.Fatal("ambiguous Port accepted")
	}
	delete(o.Rows["Port"], duplicate)
	o.Rows["Bridge"][duplicate] = Row{UUID: duplicate, Values: map[string]any{"ports": []any{original.UUID}}}
	s.install(o, d)
	if _, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, c); err == nil {
		t.Fatal("ambiguous Bridge accepted")
	}
}
