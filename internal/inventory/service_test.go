package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func fixture(t *testing.T) (*Service, Observation, Decision, authn.Claims) {
	t.Helper()
	bridge, port, i1, i2 := repository.NewID(), repository.NewID(), repository.NewID(), repository.NewID()
	o := Observation{Rows: Rows{
		"Open_vSwitch": {},
		"Bridge":       {bridge: {UUID: bridge, Values: map[string]any{"name": "synthetic-br", "ports": []any{port}, "datapath_type": "dummy"}}},
		"Port":         {port: {UUID: port, Values: map[string]any{"name": "synthetic-bond", "interfaces": []any{i1, i2}, "vlan_mode": []any{"native-untagged"}, "tag": []any{"4095"}, "trunks": []any{}, "cvlans": []any{}, "lacp": "active", "bond_mode": "balance-tcp"}}},
		"Interface":    {i1: {UUID: i1, Values: map[string]any{"name": "synthetic-1", "type": "dummy", "link_state": []any{"up"}, "options": map[string]any{}}}, i2: {UUID: i2, Values: map[string]any{"name": "synthetic-2", "type": "dummy", "link_state": []any{}, "options": map[string]any{}}}},
	}, Evidence: Evidence{ObservedAt: time.Now().UTC()}}
	d := Decision{Generation: repository.NewID(), State: "confirmed", Bindings: map[string]Binding{}}
	for table, rows := range o.Rows {
		tt := Table{Name: table, Columns: []Column{}, Indexes: [][]string{}}
		cols := map[string]bool{}
		for uuid, row := range rows {
			d.Bindings[Key(table, uuid)] = Binding{ManagementID: repository.NewID(), Table: table, UUID: uuid, State: "active"}
			for col := range row.Values {
				cols[col] = true
			}
		}
		for c := range cols {
			tt.Columns = append(tt.Columns, Column{Name: c, Monitored: true, Mutable: true, Type: "string", NativeType: json.RawMessage(`"string"`), References: []Reference{}})
		}
		o.Schema.Tables = append(o.Schema.Tables, tt)
	}
	o.Schema.Name = "Open_vSwitch"
	o.Schema.Digest = Digest(o.Schema.Tables)
	s := New(nil)
	s.install(o, d)
	c := authn.Claims{PrincipalID: repository.NewID(), Revision: repository.NewID(), Capabilities: []string{"inventory.read", "state.read", "configuration.read"}}
	return s, o, d, c
}
func TestSharedNativeInventoryAndRuntimeContract(t *testing.T) {
	s, _, d, c := fixture(t)
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/inventory", "/inventory/schema", "/ports", "/bridges", "/interfaces", "/bonds"}
	for _, b := range d.Bindings {
		kind := kindFor(b.Table)
		if kind != "" {
			paths = append(paths, "/"+kind+"s/"+b.ManagementID)
		}
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			op, p, _ := contract.Match("GET", "/api/v1"+path)
			if op == nil {
				t.Fatal("missing operation")
			}
			v, err := s.Read(context.Background(), op.ID, p, url.Values{}, c)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(v)
			if err = op.ValidateResponse(200, b); err != nil {
				t.Fatalf("%v\n%s", err, b)
			}
		})
	}
	result, _ := s.Read(context.Background(), "listPorts", nil, url.Values{}, c)
	page := result.(map[string]any)
	items := page["items"].([]map[string]any)
	if len(items) != 1 || items[0]["kind"] != "bond" {
		t.Fatal("bond not a port")
	}
	vlan := items[0]["vlan"].(map[string]any)["native"].(map[string]any)
	if vlan["tag"] != 4095 || len(vlan["trunks"].([]int)) != 0 {
		t.Fatal("native VLAN was normalized")
	}
	if items[0]["linux_carrier"].(map[string]any)["value"] != nil || items[0]["ovs_link_state"].(map[string]any)["value"] != nil {
		t.Fatal("fabricated aggregate/provider state")
	}
}
func TestCursorScopePermissionsAndSnapshotInvalidation(t *testing.T) {
	s, o, d, c := fixture(t)
	q := url.Values{"limit": {"1"}}
	v, err := s.Read(context.Background(), "listInterfaces", nil, q, c)
	if err != nil {
		t.Fatal(err)
	}
	first := v.(map[string]any)
	token, ok := first["next_cursor"].(string)
	if !ok {
		t.Fatal("pagination missing")
	}
	q.Set("cursor", token)
	next, err := s.Read(context.Background(), "listInterfaces", nil, q, c)
	if err != nil {
		t.Fatal(err)
	}
	if next.(map[string]any)["items"].([]map[string]any)[0]["id"] == first["items"].([]map[string]any)[0]["id"] {
		t.Fatal("page repeated")
	}
	c2 := c
	c2.Revision = "changed"
	if _, err = s.Read(context.Background(), "listInterfaces", nil, q, c2); err == nil {
		t.Fatal("old permission cursor accepted")
	}
	c2 = c
	c2.PrincipalID = repository.NewID()
	if _, err = s.Read(context.Background(), "listInterfaces", nil, q, c2); err == nil {
		t.Fatal("cross-principal cursor")
	}
	q.Set("limit", "2")
	if _, err = s.Read(context.Background(), "listInterfaces", nil, q, c); err == nil {
		t.Fatal("changed query cursor")
	}
	q.Set("limit", "1")
	// Same data plus heartbeat retains the snapshot; an external row change does not.
	o.Evidence.ObservedAt = o.Evidence.ObservedAt.Add(time.Second)
	s.install(o, d)
	if _, err = s.Read(context.Background(), "listInterfaces", nil, q, c); err != nil {
		t.Fatal("heartbeat invalidated cursor", err)
	}
	for id, r := range o.Rows["Interface"] {
		r.Values["name"] = "changed"
		o.Rows["Interface"][id] = r
		break
	}
	s.install(o, d)
	if _, err = s.Read(context.Background(), "listInterfaces", nil, q, c); err == nil {
		t.Fatal("mixed snapshots")
	}
}
func TestStaleUnknownReconciliationAndFieldFiltering(t *testing.T) {
	s, o, d, c := fixture(t)
	c.Capabilities = []string{"inventory.read", "state.read"}
	v, err := s.Read(context.Background(), "listPorts", nil, url.Values{}, c)
	if err != nil {
		t.Fatal(err)
	}
	item := v.(map[string]any)["items"].([]map[string]any)[0]
	if item["vlan"].(map[string]any)["native"] != nil || item["fields"].(map[string]any)["tag"].(map[string]any)["availability"] != "withheld" {
		t.Fatal("configuration leaked")
	}
	s.Unavailable("OVSDB_UNAVAILABLE")
	v, err = s.Read(context.Background(), "listPorts", nil, url.Values{}, c)
	if err != nil || v.(map[string]any)["source"].(map[string]any)["freshness"] != "stale" {
		t.Fatal("stale cache claimed fresh", err)
	}
	d.State = "reconciliation-required"
	d.Reason = "database-file-replaced-or-rewound"
	s.install(o, d)
	if _, err = s.Read(context.Background(), "listPorts", nil, url.Values{}, c); err == nil {
		t.Fatal("uncertain identity published")
	}
	if _, err = s.Read(context.Background(), "readInventory", nil, url.Values{}, c); err != nil {
		t.Fatal("reconciliation status hidden")
	}
	empty := New(nil)
	status, _ := empty.Read(context.Background(), "readInventory", nil, url.Values{}, c)
	if status.(map[string]any)["instance_generation"] != nil {
		t.Fatal("fabricated default generation")
	}
	if _, err = empty.Read(context.Background(), "listPorts", nil, url.Values{}, c); err == nil {
		t.Fatal("unavailable became empty list")
	}
}
func TestObservationDoesNotChangeConfigRevision(t *testing.T) {
	s, o, d, c := fixture(t)
	var b Binding
	for _, binding := range d.Bindings {
		if binding.Table == "Interface" {
			b = binding
			break
		}
	}
	p := map[string]string{"interface_id": b.ManagementID}
	v, _ := s.Read(context.Background(), "readInterface", p, url.Values{}, c)
	rev := v.(map[string]any)["config_revision"]
	row := o.Rows["Interface"][b.UUID]
	row.Values["link_state"] = []any{"down"}
	s.install(o, d)
	v, _ = s.Read(context.Background(), "readInterface", p, url.Values{}, c)
	if rev != v.(map[string]any)["config_revision"] {
		t.Fatal("observation became configuration revision")
	}
}
