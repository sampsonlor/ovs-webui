package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func treeFixture(t *testing.T) (*Service, Observation, Decision, authn.Claims, Binding, Binding) {
	t.Helper()
	s, o, d, c := fixture(t)
	var bridge, port Binding
	for _, b := range d.Bindings {
		if b.Table == "Bridge" {
			bridge = b
		}
		if b.Table == "Port" {
			port = b
		}
	}
	b := o.Rows["Bridge"][bridge.UUID]
	b.Values["stp_enable"], b.Values["rstp_enable"] = false, true
	b.Values["other_config"] = map[string]any{"rstp-priority": "4096", "unrelated-secret": "must-not-appear"}
	b.Values["status"], b.Values["rstp_status"] = map[string]any{}, map[string]any{"rstp_bridge_id": "8.000.000000000001", "rstp_root_id": "8.000.000000000001"}
	b.Values["mirrors"], b.Values["controller"], b.Values["external_ids"] = []any{}, []any{}, map[string]any{}
	p := o.Rows["Port"][port.UUID]
	p.Values["other_config"], p.Values["status"], p.Values["rstp_status"] = map[string]any{}, map[string]any{}, map[string]any{"rstp_port_state": "Forwarding", "rstp_port_role": "Designated"}
	for i := range o.Schema.Tables {
		table := &o.Schema.Tables[i]
		if table.Name != "Bridge" && table.Name != "Port" {
			continue
		}
		for _, name := range []string{"stp_enable", "rstp_enable", "other_config", "status", "rstp_status"} {
			found := false
			for j := range table.Columns {
				col := &table.Columns[j]
				if col.Name == name {
					col.SpanningTreeCompatible, found = true, true
				}
			}
			if !found {
				table.Columns = append(table.Columns, Column{Name: name, Monitored: true, Mutable: true, SpanningTreeCompatible: true})
			}
		}
	}
	s.install(o, d)
	return s, o, d, c, bridge, port
}

func treeRead(t *testing.T, s *Service, c authn.Claims, bridge Binding) map[string]any {
	t.Helper()
	x, err := s.Read(context.Background(), "readSpanningTreeObservation", map[string]string{"bridge_id": bridge.ManagementID}, url.Values{}, c)
	if err != nil {
		t.Fatal(err)
	}
	return x.(map[string]any)
}

func TestSpanningTreeCoherentNativeResourceAndContract(t *testing.T) {
	s, _, _, c, bridge, port := treeFixture(t)
	item := treeRead(t, s, c, bridge)
	b := item["spanning_tree"].(map[string]any)
	if b["protocol"] != "rstp" || b["editable"] != false || item["mode"] != "observe" {
		t.Fatal(b)
	}
	if b["configuration"].(map[string]any)["rstp-max-age"].(map[string]any)["availability"] != "unset" {
		t.Fatal("invented native default")
	}
	ports := item["ports"].([]map[string]any)
	if len(ports) != 1 || ports[0]["port_ref"].(map[string]any)["id"] != port.ManagementID {
		t.Fatal("wrong coherent Port")
	}
	p := ports[0]["spanning_tree"].(map[string]any)
	if p["rstp_participation"].(map[string]any)["value"] != "excluded-bond" || p["runtime"].(map[string]any)["rstp_port_state"].(map[string]any)["value"] != "Forwarding" {
		t.Fatal("configuration exclusion conflated with observed status")
	}
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	portList, path, _ := contract.Match("GET", "/api/v1/ports")
	query := url.Values{"filter": {"synthetic-absent-port"}}
	if err = portList.ValidateParameters(path, query, func(string) []string { return nil }); err != nil {
		t.Fatal("named Port lookup rejected by the HTTP contract", err)
	}
	filtered, err := s.Read(context.Background(), portList.ID, path, query, c)
	if err != nil || len(filtered.(map[string]any)["items"].([]map[string]any)) != 0 {
		t.Fatal("Port name filter did not constrain the result", err)
	}
	for _, path := range []string{"/inventory/spanning-tree", "/bridges/" + bridge.ManagementID + "/spanning-tree"} {
		op, p, _ := contract.Match("GET", "/api/v1"+path)
		if op == nil {
			t.Fatal("route missing")
		}
		value, e := s.Read(context.Background(), op.ID, p, url.Values{}, c)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(value)
		if strings.Contains(string(body), "must-not-appear") {
			t.Fatal("free-form native configuration leaked")
		}
		if e = op.ValidateResponse(200, body); e != nil {
			t.Fatalf("%v: %s", e, body)
		}
	}
	for _, op := range []string{"readBridge", "readPort"} {
		id := bridge.ManagementID
		if op == "readPort" {
			id = port.ManagementID
		}
		value, e := s.Read(context.Background(), op, map[string]string{"id": id}, url.Values{}, c)
		if e != nil {
			t.Fatal(e)
		}
		tree := value.(map[string]any)["spanning_tree"]
		expected := any(b)
		if op == "readPort" {
			expected = p
		}
		actual := tree.(map[string]any)
		full := expected.(map[string]any)
		for _, group := range []string{"configuration", "runtime"} {
			for name, field := range actual[group].(map[string]any) {
				if Digest(field) != Digest(full[group].(map[string]any)[name]) {
					t.Fatal("shared detail diverged")
				}
			}
		}
	}
}

func TestSpanningTreeWithholdingPreservesIndependentRuntime(t *testing.T) {
	s, _, _, c, bridge, _ := treeFixture(t)
	c.Capabilities = []string{"inventory.read", "state.read"}
	item := treeRead(t, s, c, bridge)
	b := item["spanning_tree"].(map[string]any)
	if b["protocol"] != "unknown" || b["availability"] != "withheld" || b["ownership"] != "withheld" {
		t.Fatal(b)
	}
	for _, f := range b["configuration"].(map[string]any) {
		if f.(map[string]any)["availability"] != "withheld" || f.(map[string]any)["value"] != nil {
			t.Fatal("configuration disclosure")
		}
	}
	if b["runtime"].(map[string]any)["rstp_bridge_id"].(map[string]any)["availability"] != "known" {
		t.Fatal("runtime incorrectly withheld")
	}
	p := item["ports"].([]map[string]any)[0]["spanning_tree"].(map[string]any)
	if p["rstp_participation"].(map[string]any)["availability"] != "withheld" {
		t.Fatal("type participation leak")
	}
	c.Capabilities = []string{"state.read"}
	if _, err := s.Read(context.Background(), "listSpanningTreeObservations", nil, url.Values{}, c); err == nil {
		t.Fatal("inventory permission bypass")
	}
}

func TestSpanningTreeConflictUnknownUnsupportedAndStale(t *testing.T) {
	for _, scenario := range []string{"both", "missing", "schema", "stale", "external"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, d, c, bridge, _ := treeFixture(t)
			b := o.Rows["Bridge"][bridge.UUID]
			switch scenario {
			case "both":
				b.Values["stp_enable"] = true
			case "missing":
				delete(b.Values, "rstp_enable")
			case "schema":
				for i := range o.Schema.Tables {
					if o.Schema.Tables[i].Name == "Bridge" {
						for j := range o.Schema.Tables[i].Columns {
							if o.Schema.Tables[i].Columns[j].Name == "rstp_enable" {
								o.Schema.Tables[i].Columns[j].SpanningTreeCompatible = false
							}
						}
					}
				}
			case "stale":
				o.Evidence.ObservedAt = time.Now().Add(-FreshFor - time.Second)
			case "external":
				b.Values["external_ids"] = map[string]any{"ovn-owner": "synthetic"}
			}
			s.install(o, d)
			item := treeRead(t, s, c, bridge)
			tree := item["spanning_tree"].(map[string]any)
			switch scenario {
			case "both":
				if tree["protocol"] != "invalid-both-enabled" {
					t.Fatal(tree)
				}
			case "missing":
				if tree["availability"] != "unknown" {
					t.Fatal(tree)
				}
			case "schema":
				if tree["availability"] != "unsupported" {
					t.Fatal(tree)
				}
			case "stale":
				if item["source"].(map[string]any)["freshness"] != "stale" {
					t.Fatal(item)
				}
				p := item["ports"].([]map[string]any)[0]["spanning_tree"].(map[string]any)
				if p["rstp_participation"].(map[string]any)["value"] != nil {
					t.Fatal("stale participation promoted")
				}
			case "external":
				if tree["ownership"] != "externally-controlled" {
					t.Fatal(tree)
				}
			}
			if item["editable"] != false || len(item["allowed_operations"].([]string)) != 0 {
				t.Fatal("write authority inferred")
			}
		})
	}
}

func TestSpanningTreeRuntimeDoesNotChangeConfigurationRevision(t *testing.T) {
	s, o, d, c, bridge, _ := treeFixture(t)
	before := treeRead(t, s, c, bridge)
	o.Rows["Bridge"][bridge.UUID].Values["rstp_status"].(map[string]any)["rstp_root_id"] = "8.000.000000000002"
	o.Evidence.ObservedAt = o.Evidence.ObservedAt.Add(time.Millisecond)
	s.install(o, d)
	after := treeRead(t, s, c, bridge)
	if before["config_revision"] != after["config_revision"] || before["snapshot_id"] == after["snapshot_id"] {
		t.Fatal("runtime/config revision conflated")
	}
}

func TestSpanningTreeParticipationNativeExclusionsAndUnknownMirror(t *testing.T) {
	for _, scenario := range []string{"internal", "system-default", "disabled", "mirror", "unverified-type", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, d, c, bridge, port := treeFixture(t)
			p := o.Rows["Port"][port.UUID]
			id := refs(p.Values["interfaces"])[0]
			p.Values["interfaces"] = []any{id}
			o.Rows["Interface"][id].Values["type"] = "system"
			want := "enabled-by-default"
			reason := "NATIVE_DEFAULT"
			switch scenario {
			case "internal":
				o.Rows["Interface"][id].Values["type"] = "internal"
				want, reason = "excluded-internal", "NATIVE_INTERNAL_EXCLUDED"
			case "system-default":
				o.Rows["Interface"][id].Values["type"] = ""
			case "disabled":
				p.Values["other_config"].(map[string]any)["rstp-enable"] = "false"
				want, reason = "disabled-on-port", ""
			case "mirror":
				o.Rows["Bridge"][bridge.UUID].Values["mirrors"] = []any{repository.NewID()}
				want, reason = "", "MIRROR_OUTPUT_NOT_OBSERVED"
			case "unverified-type":
				o.Rows["Interface"][id].Values["type"] = "future-device"
				want, reason = "", "INTERFACE_TYPE_UNVERIFIED"
			case "malformed":
				p.Values["other_config"].(map[string]any)["rstp-enable"] = "maybe"
				want, reason = "", "NATIVE_VALUE_INVALID"
			}
			s.install(o, d)
			f := treeRead(t, s, c, bridge)["ports"].([]map[string]any)[0]["spanning_tree"].(map[string]any)["rstp_participation"].(map[string]any)
			if f["reason"] != reason || want != "" && f["value"] != want || want == "" && f["value"] != nil {
				t.Fatal(f)
			}
		})
	}
}

func TestSpanningTreeBoundedDetailAndRetiredIdentity(t *testing.T) {
	s, o, d, c, bridge, port := treeFixture(t)
	for n := 0; n < 25; n++ {
		id := repository.NewID()
		o.Rows["Port"][id] = Row{UUID: id, Values: map[string]any{"name": "synthetic-extra", "interfaces": o.Rows["Port"][port.UUID].Values["interfaces"]}}
		d.Bindings[Key("Port", id)] = Binding{ManagementID: repository.NewID(), Table: "Port", UUID: id, State: "active"}
		o.Rows["Bridge"][bridge.UUID].Values["ports"] = append(o.Rows["Bridge"][bridge.UUID].Values["ports"].([]any), id)
	}
	s.install(o, d)
	item := treeRead(t, s, c, bridge)
	body, _ := json.Marshal(item)
	if item["ports_truncated"] != true || len(body) > 52<<10 || len(item["ports"].([]map[string]any)) == 0 {
		t.Fatal("unbounded or silently incomplete detail")
	}
	delete(d.Bindings, Key("Bridge", bridge.UUID))
	s.install(o, d)
	if _, err := s.Read(context.Background(), "readSpanningTreeObservation", map[string]string{"bridge_id": bridge.ManagementID}, url.Values{}, c); err == nil {
		t.Fatal("retired identity adopted by name")
	}
}

func TestSpanningTreeUnrecognizedRuntimeAndInvalidPresenceRemainUnknown(t *testing.T) {
	s, o, d, c, bridge, port := treeFixture(t)
	o.Rows["Port"][port.UUID].Values["rstp_status"].(map[string]any)["rstp_port_state"] = "Future-state"
	o.Rows["Bridge"][bridge.UUID].Values["other_config"].(map[string]any)["rstp-max-age"] = nil
	s.install(o, d)
	item := treeRead(t, s, c, bridge)
	p := item["ports"].([]map[string]any)[0]["spanning_tree"].(map[string]any)["runtime"].(map[string]any)["rstp_port_state"].(map[string]any)
	if p["value"] != nil || p["reason"] != "RUNTIME_VALUE_UNRECOGNIZED" {
		t.Fatal(p)
	}
	f := item["spanning_tree"].(map[string]any)["configuration"].(map[string]any)["rstp-max-age"].(map[string]any)
	if f["availability"] != "unknown" || f["reason"] != "NATIVE_VALUE_INVALID" {
		t.Fatal("invalid configured value treated as unset")
	}
}
