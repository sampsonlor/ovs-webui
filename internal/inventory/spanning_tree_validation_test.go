package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

func TestSpanningTreeParameterValidationPermissionFreshnessAndSchema(t *testing.T) {
	for _, tc := range []struct{ scenario, state, code string }{
		{"defaults", "valid", ""},
		{"rounding", "invalid", "RSTP_PRIORITY_MULTIPLE_4096"},
		{"inactive-timers", "invalid", "STP_MAX_AGE_HELLO_RELATION"},
		{"mutual-exclusion", "invalid", "STP_RSTP_MUTUALLY_EXCLUSIVE"},
		{"withheld-invalid", "withheld", ""},
		{"stale-invalid", "stale", ""},
		{"missing", "unknown", ""},
		{"invalid-presence", "unknown", ""},
		{"unsupported", "unsupported", ""},
		{"external-valid", "valid", ""},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			s, o, d, c, bridge, _ := treeFixture(t)
			b := o.Rows["Bridge"][bridge.UUID]
			params := b.Values["other_config"].(map[string]any)
			switch tc.scenario {
			case "rounding", "withheld-invalid", "stale-invalid":
				params["rstp-priority"] = "4097"
			case "inactive-timers":
				params["stp-hello-time"] = "10"
			case "mutual-exclusion":
				b.Values["stp_enable"] = true
			case "missing":
				delete(b.Values, "other_config")
			case "invalid-presence":
				params["rstp-priority"] = nil
			case "unsupported":
				for i := range o.Schema.Tables {
					if o.Schema.Tables[i].Name == "Bridge" {
						for j := range o.Schema.Tables[i].Columns {
							if o.Schema.Tables[i].Columns[j].Name == "other_config" {
								o.Schema.Tables[i].Columns[j].SpanningTreeCompatible = false
							}
						}
					}
				}
			case "external-valid":
				b.Values["external_ids"] = map[string]any{"ovn-owner": "synthetic"}
			}
			if tc.scenario == "withheld-invalid" {
				c.Capabilities = []string{"inventory.read", "state.read"}
			}
			if tc.scenario == "stale-invalid" {
				o.Evidence.ObservedAt = time.Now().Add(-FreshFor - time.Second)
			}
			s.install(o, d)
			item := treeRead(t, s, c, bridge)
			tree := item["spanning_tree"].(map[string]any)
			v := tree["parameter_validation"].(spanningtree.Validation)
			if v.State != tc.state || v.Scope != spanningtree.Scope || v.Version != spanningtree.Version {
				t.Fatal(v)
			}
			if tc.code != "" {
				expectedCount := 1
				if tc.scenario == "inactive-timers" {
					expectedCount = 2
				}
				if len(v.Checks) != expectedCount || v.Checks[0].Code != tc.code {
					t.Fatal(v)
				}
			} else if len(v.Checks) != 0 {
				t.Fatal("withheld/unavailable inputs became a validity oracle", v)
			}
			if item["editable"] != false || tree["editable"] != false || tree["write_reason"] != "SPANNING_TREE_WRITE_GATE_PENDING" || len(item["allowed_operations"].([]string)) != 0 {
				t.Fatal("parameter checks granted writes", tree)
			}
			encoded, _ := json.Marshal(v)
			if strings.Contains(string(encoded), "4097") || strings.Contains(string(encoded), "must-not-appear") {
				t.Fatal("raw native values leaked through checks")
			}
		})
	}
}

func TestSpanningTreeParameterChecksReuseBridgeResourceWithoutChangingRawDefaults(t *testing.T) {
	s, _, _, c, bridge, _ := treeFixture(t)
	item := treeRead(t, s, c, bridge)
	tree := item["spanning_tree"].(map[string]any)
	bridgeItem, err := s.Read(context.Background(), "readBridge", map[string]string{"bridge_id": bridge.ManagementID}, url.Values{}, c)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(tree["parameter_validation"])
	b, _ := json.Marshal(bridgeItem.(map[string]any)["spanning_tree"].(map[string]any)["parameter_validation"])
	if string(a) != string(b) {
		t.Fatal("Bridge detail uses different parameter rules")
	}
	for _, key := range []string{"rstp-max-age", "rstp-forward-delay", "stp-hello-time"} {
		f := tree["configuration"].(map[string]any)[key].(map[string]any)
		if f["value"] != nil || f["availability"] != "unset" {
			t.Fatal("validator invented an observed default", key, f)
		}
	}
}
