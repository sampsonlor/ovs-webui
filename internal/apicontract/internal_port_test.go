package apicontract

import (
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func TestInternalPortRequestRejectsPrivateGraphsAndInvalidVLANs(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	i := map[string]any{"intent_id": repository.NewID(), "operation": "port.create-internal", "name": "pi-new", "vlan_id": 20, "object": map[string]any{"management_id": repository.NewID(), "ovs_uuid": repository.NewID(), "table": "Bridge", "instance_generation": repository.NewID()}}
	if err = c.Schemas["Intent"].Validate(i); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"internal_port_creation", "management_id", "interfaces", "members", "before_present", "value", "marker"} {
		i[field] = "forged"
		if c.Schemas["Intent"].Validate(i) == nil {
			t.Fatal("accepted", field)
		}
		delete(i, field)
	}
	for _, v := range []any{0, 4095, 20.5, "20", nil} {
		i["vlan_id"] = v
		if c.Schemas["Intent"].Validate(i) == nil {
			t.Fatal("accepted VLAN", v)
		}
	}
}
