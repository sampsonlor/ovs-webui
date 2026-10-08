package inventory

import (
	"context"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"net/url"
	"strings"
	"testing"
)

func TestTopologyPublicViewsNeverExposePrivateConfiguration(t *testing.T) {
	s, o, d, c := fixture(t)
	for table, rows := range o.Rows {
		for id, row := range rows {
			row.Configuration = candidate.CloneConfiguration(row.Values)
			row.Configuration["synthetic_unknown_column"] = "private-configuration-marker"
			row.Configuration["options"] = map[string]any{"psk": "private-credential-marker"}
			o.Rows[table][id] = row
		}
	}
	s.install(o, d)
	for _, op := range []string{"readInventoryTopology", "readInventory", "readInventorySchema", "listPorts", "listBridges", "listInterfaces", "listBonds"} {
		v, err := s.Read(context.Background(), op, nil, url.Values{}, c)
		if err != nil {
			t.Fatal(op, err)
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), "private-configuration-marker") || strings.Contains(string(b), "private-credential-marker") || strings.Contains(string(b), "private_configuration") {
			t.Fatal("private native images reached public response", op)
		}
	}
	if _, err := s.Read(context.Background(), "readInventoryTopology", nil, url.Values{}, authn.Claims{Capabilities: []string{"inventory.read"}}); err == nil {
		t.Fatal("configuration privilege bypassed")
	}
}
