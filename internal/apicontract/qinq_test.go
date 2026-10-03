package apicontract

import (
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestQinQInputCannotForgeContextOrWriteNativeMap(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"vlan_mode": "dot1q-tunnel", "tag": 200, "trunks": []any{}, "cvlans": []any{20, 30}}
	i := map[string]any{"intent_id": repository.NewID(), "operation": "port.vlan.set", "value": value, "object": map[string]any{"management_id": repository.NewID(), "ovs_uuid": repository.NewID(), "table": "Port", "instance_generation": repository.NewID()}}
	if err = c.Schemas["Intent"].Validate(i); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"qinq_context", "before", "dependency_revision", "other_config"} {
		i[field] = map[string]any{"ethertype": "802.1q"}
		if c.Schemas["Intent"].Validate(i) == nil {
			t.Fatal("forged context accepted", field)
		}
		delete(i, field)
	}
	value["qinq-ethtype"] = "802.1q"
	if c.Schemas["Intent"].Validate(i) == nil {
		t.Fatal("native key write accepted")
	}
}
