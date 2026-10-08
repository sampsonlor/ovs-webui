package apicontract

import (
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
)

func TestTopologySemanticInputCannotSupplyNativeImagesOrRecoveryEvidence(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	i := map[string]any{"intent_id": repository.NewID(), "operation": "port.create", "object": map[string]any{"management_id": repository.NewID(), "ovs_uuid": repository.NewID(), "table": "Bridge", "instance_generation": repository.NewID()}, "topology": map[string]any{"name": "new-port", "native_type": "internal"}}
	if err = c.Schemas["Intent"].Validate(i); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"before", "after", "topology_change", "replacements", "private_configuration", "row", "transaction", "marker", "compensating", "observed"} {
		i[key] = "forged"
		if c.Schemas["Intent"].Validate(i) == nil {
			t.Fatal("forged authority accepted", key)
		}
		delete(i, key)
		r := i["topology"].(map[string]any)
		r[key] = "forged"
		if c.Schemas["Intent"].Validate(i) == nil {
			t.Fatal("forged topology property accepted", key)
		}
		delete(r, key)
	}
}
