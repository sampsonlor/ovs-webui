package inventory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func treePreparationFixture(t *testing.T) (*Service, Observation, Decision, authn.Claims, candidate.Binding) {
	t.Helper()
	s, o, d, c := fixture(t)
	root, br, port, iface := repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID()
	o.Rows = Rows{
		"Open_vSwitch": {root: {UUID: root, Values: map[string]any{"bridges": []any{br}, "other_config": map[string]any{}, "external_ids": map[string]any{}}}},
		"Bridge":       {br: {UUID: br, Values: map[string]any{"name": "tree-br", "ports": []any{port}, "datapath_type": "system", "stp_enable": false, "rstp_enable": false, "controller": []any{}, "mirrors": []any{}, "flow_tables": map[string]any{}, "other_config": map[string]any{"opaque": "private-tree-marker"}, "external_ids": map[string]any{}}}},
		"Port":         {port: {UUID: port, Values: map[string]any{"name": "tree-br", "interfaces": []any{iface}, "vlan_mode": []any{}, "tag": []any{}, "trunks": []any{}, "cvlans": []any{}, "qos": []any{}, "bond_mode": []any{}, "lacp": []any{}, "other_config": map[string]any{}, "external_ids": map[string]any{}}}},
		"Interface":    {iface: {UUID: iface, Values: map[string]any{"name": "tree-br", "type": "internal", "options": map[string]any{}, "external_ids": map[string]any{}}}},
	}
	d.Bindings = map[string]Binding{}
	for table, rows := range o.Rows {
		for id, row := range rows {
			row.Configuration = candidate.CloneConfiguration(row.Values)
			o.Rows[table][id] = row
			d.Bindings[Key(table, id)] = Binding{ManagementID: repository.NewID(), Table: table, UUID: id, State: "active"}
		}
	}
	o.Evidence.Root = root
	o.Schema.Tables = []Table{{Name: "Bridge", Columns: []Column{{Name: "stp_enable", Monitored: true, Mutable: true, SpanningTreeCompatible: true}, {Name: "rstp_enable", Monitored: true, Mutable: true, SpanningTreeCompatible: true}, {Name: "other_config", Monitored: true, Mutable: true, SpanningTreeCompatible: true}, {Name: "flow_tables", References: []Reference{{Table: "Flow_Table", Position: "value"}}}}}}
	b := d.Bindings[Key("Bridge", br)]
	binding := candidate.Binding{ManagementID: b.ManagementID, OVSUUID: br, Table: "Bridge", Generation: d.Generation}
	c.Capabilities = append(c.Capabilities, "workspace.write", candidate.SpanningTreePrepareCapability)
	s.install(o, d)
	return s, o, d, c, binding
}

func TestSpanningTreePreparationAuthorityIsIndependentAndHintsAreNotExecution(t *testing.T) {
	s, _, _, c, b := treePreparationFixture(t)
	ctx := context.Background()
	read := func() candidate.Snapshot {
		out, err := s.CandidateSnapshot(ctx, []candidate.Binding{b})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := read()
	if candidate.TreePreparationProblem(before.SpanningTrees[b.ManagementID]) != "SPANNING_TREE_BRIDGE_AUTHORITY_REQUIRED" {
		t.Fatal(before.SpanningTrees)
	}
	if err := s.SetLocalTopologyObjects([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	if read().SpanningTrees[b.ManagementID].Authority != "unknown" {
		t.Fatal("topology grant leaked into spanning tree")
	}
	if err := s.SetLocalSpanningTreeBridges([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	authorized := read()
	if candidate.TreePreparationProblem(authorized.SpanningTrees[b.ManagementID]) != "" || authorized.Policy == before.Policy {
		t.Fatal(authorized.SpanningTrees)
	}
	item, err := s.Read(ctx, "readSpanningTreeObservation", map[string]string{"bridge_id": b.ManagementID}, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	value := item.(map[string]any)
	if value["preparation"].(map[string]any)["available"] != true || value["editable"] != false || len(value["allowed_operations"].([]string)) != 0 {
		t.Fatal(value)
	}
	if err := s.SetLocalSpanningTreeBridges(nil); err != nil {
		t.Fatal(err)
	}
	if read().Policy == authorized.Policy || read().SpanningTrees[b.ManagementID].Authority != "unknown" {
		t.Fatal("revocation lost")
	}
	for _, ids := range [][]string{{b.ManagementID, b.ManagementID}, {"not-an-id"}} {
		if s.SetLocalSpanningTreeBridges(ids) == nil {
			t.Fatal("invalid root policy accepted")
		}
	}
}

func TestSpanningTreePreparationGraphAndPermissionGates(t *testing.T) {
	for _, scenario := range []string{"withheld", "capability", "stale", "external-root", "external-child", "controller", "mirror", "datapath", "interface-type", "options", "bond", "qinq", "advanced", "port-override", "missing-private", "immutable-schema", "unknown-value", "unknown-datapath", "unknown-external-ids", "referenced-domain", "unknown-name", "unknown-member", "duplicate-port"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, d, c, b := treePreparationFixture(t)
			if err := s.SetLocalSpanningTreeBridges([]string{b.ManagementID}); err != nil {
				t.Fatal(err)
			}
			br := o.Rows["Bridge"][b.OVSUUID]
			root := o.Rows["Open_vSwitch"][o.Evidence.Root]
			port := o.Rows["Port"][refs(br.Values["ports"])[0]]
			iface := o.Rows["Interface"][refs(port.Values["interfaces"])[0]]
			switch scenario {
			case "withheld":
				c.Capabilities = []string{"inventory.read", "state.read"}
			case "capability":
				c.Capabilities = []string{"inventory.read", "state.read", "configuration.read", "workspace.write", "ovs.topology.write", "ovs.port.vlan.write"}
			case "stale":
				o.Evidence.ObservedAt = time.Now().Add(-FreshFor - time.Second)
			case "external-root":
				root.Values["external_ids"] = map[string]any{"ovn-owner": "synthetic"}
			case "external-child":
				iface.Values["external_ids"] = map[string]any{"ovn-owner": "synthetic"}
			case "controller":
				br.Values["controller"] = []any{repository.NewID()}
			case "mirror":
				br.Values["mirrors"] = []any{repository.NewID()}
			case "datapath":
				br.Values["datapath_type"] = "netdev"
			case "interface-type":
				iface.Values["type"] = "patch"
			case "options":
				iface.Values["options"] = map[string]any{"peer": "unverified"}
			case "bond":
				port.Values["interfaces"] = append(refsAny(port.Values["interfaces"]), repository.NewID())
			case "qinq":
				port.Values["vlan_mode"] = []any{"dot1q-tunnel"}
				port.Values["tag"] = []any{"200"}
			case "advanced":
				br.Values["other_config"].(map[string]any)["rstp-address"] = "00:00:00:00:00:01"
			case "port-override":
				port.Values["other_config"].(map[string]any)["stp-enable"] = "false"
			case "immutable-schema":
				o.Schema.Tables[0].Columns[0].Mutable = false
			case "unknown-datapath":
				delete(br.Values, "datapath_type")
			case "unknown-external-ids":
				delete(iface.Values, "external_ids")
			case "referenced-domain":
				o.Schema.Tables[0].Columns = append(o.Schema.Tables[0].Columns, Column{Name: "flow_tables", References: []Reference{{Table: "Flow_Table", Position: "value"}}})
				br.Values["flow_tables"] = map[string]any{"1": repository.NewID()}
			case "unknown-name":
				delete(iface.Values, "name")
			case "unknown-member":
				port.Values["interfaces"] = append(refsAny(port.Values["interfaces"]), 3)
			case "duplicate-port":
				br.Values["ports"] = []any{port.UUID, port.UUID}
			case "unknown-value":
				br.Values["stp_enable"] = "invalid"
			}
			for _, item := range []struct {
				table string
				row   Row
			}{{"Open_vSwitch", root}, {"Bridge", br}, {"Port", port}, {"Interface", iface}} {
				r := item.row
				r.Configuration = candidate.CloneConfiguration(r.Values)
				if scenario == "missing-private" && item.table == "Interface" {
					r.Configuration = nil
				}
				o.Rows[item.table][r.UUID] = r
			}
			s.install(o, d)
			v, err := s.Read(context.Background(), "readSpanningTreeObservation", map[string]string{"bridge_id": b.ManagementID}, nil, c)
			if err != nil {
				t.Fatal(err)
			}
			prep := v.(map[string]any)["preparation"].(map[string]any)
			if prep["available"] != false || prep["reason"] == "" {
				t.Fatal(scenario, prep)
			}
			if scenario == "withheld" && prep["authority"] != "withheld" {
				t.Fatal("authority leaked", prep)
			}
			encoded, _ := json.Marshal(v)
			if strings.Contains(string(encoded), "private-tree-marker") {
				t.Fatal("private graph image exposed")
			}
		})
	}
}

func TestSpanningTreeDraftDependencyExcludesRuntimeButRetainsForeignConfiguration(t *testing.T) {
	s, o, d, _, b := treePreparationFixture(t)
	if err := s.SetLocalSpanningTreeBridges([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	read := func() candidate.SpanningTreeBridge {
		v, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{b})
		if err != nil {
			t.Fatal(err)
		}
		return v.SpanningTrees[b.ManagementID]
	}
	before := read()
	br := o.Rows["Bridge"][b.OVSUUID]
	br.Values["rstp_status"] = map[string]any{"rstp_bridge_id": "synthetic-runtime"}
	s.install(o, d)
	if read().Dependency != before.Dependency || candidate.Digest(read().Configuration) != candidate.Digest(before.Configuration) {
		t.Fatal("runtime changed desired configuration")
	}
	br.Values["other_config"].(map[string]any)["rstp-priority"] = "4096"
	br.Configuration = candidate.CloneConfiguration(br.Values)
	delete(br.Configuration, "rstp_status")
	o.Rows["Bridge"][b.OVSUUID] = br
	s.install(o, d)
	if read().Dependency != before.Dependency || candidate.Digest(read().Configuration) == candidate.Digest(before.Configuration) {
		t.Fatal("basic field conflict lost its independent comparison")
	}
	br.Configuration["other_config"].(map[string]any)["opaque"] = "changed-foreign-setting"
	s.install(o, d)
	if read().Dependency == before.Dependency {
		t.Fatal("unrelated private setting disappeared from graph guard")
	}
}

func refsAny(v any) []any { return append([]any{}, v.([]any)...) }
