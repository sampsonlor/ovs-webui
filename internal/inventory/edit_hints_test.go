package inventory

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func TestVLANEditHintsUseValidationAuthorityAndCurrentPermissions(t *testing.T) {
	s, o, d, claims := fixture(t)
	var target Binding
	for _, b := range d.Bindings {
		if b.Table == "Port" {
			target = b
		}
	}
	for n, table := range o.Schema.Tables {
		if table.Name == "Port" {
			for j, c := range table.Columns {
				if c.Name == "vlan_mode" || c.Name == "tag" || c.Name == "trunks" || c.Name == "cvlans" {
					o.Schema.Tables[n].Columns[j].VLANCompatible = true
				}
			}
		}
	}
	s.install(o, d)
	claims.Capabilities = append(claims.Capabilities, "workspace.write", "ovs.port.vlan.write")
	read := func(allowed bool, ownership string) {
		t.Helper()
		value, err := s.Read(context.Background(), "readPort", map[string]string{"port_id": target.ManagementID}, url.Values{}, claims)
		if err != nil {
			t.Fatal(err)
		}
		port := value.(map[string]any)
		if (len(port["allowed_operations"].([]string)) > 0) != allowed || port["vlan_ownership"] != ownership {
			t.Fatal("authority hint", port)
		}
		for _, name := range []string{"vlan_mode", "tag", "trunks", "cvlans"} {
			field := port["fields"].(map[string]any)[name].(map[string]any)
			if field["editable"] != allowed || field["ownership"] != ownership {
				t.Fatal("field scope mismatch", field)
			}
		}
	}
	read(false, "unknown")
	if err := s.SetLocalVLANPorts([]string{target.ManagementID}); err != nil {
		t.Fatal(err)
	}
	read(true, "local-managed")
	claims.Capabilities = claims.Capabilities[:len(claims.Capabilities)-1]
	read(false, "local-managed")
	claims.Capabilities = append(claims.Capabilities, "ovs.port.vlan.write")
	s.Unavailable("OVSDB_DISCONNECTED")
	read(false, "local-managed")
	s.install(o, d)
	o.Rows["Port"][target.UUID].Values["external_ids"] = map[string]any{"ovn-port": "synthetic-external"}
	s.install(o, d)
	read(false, "externally-controlled")
	s.now = func() time.Time { return o.Evidence.ObservedAt.Add(FreshFor + time.Second) }
	read(false, "externally-controlled")
}
