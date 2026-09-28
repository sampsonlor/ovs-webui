package apicontract

import (
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestDeletionIntentRejectsClientSuppliedRecoveryGraphs(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	intent := map[string]any{"intent_id": repository.NewID(), "operation": "bridge.delete-isolated", "object": map[string]any{
		"management_id": repository.NewID(), "ovs_uuid": repository.NewID(), "table": "Bridge", "instance_generation": repository.NewID(),
	}}
	if err = c.Schemas["Intent"].Validate(intent); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"bridge_deletion", "source", "replacement", "source_marker", "restoring", "name", "value"} {
		intent[field] = "forged"
		if c.Schemas["Intent"].Validate(intent) == nil {
			t.Fatal("accepted private field", field)
		}
		delete(intent, field)
	}
}
