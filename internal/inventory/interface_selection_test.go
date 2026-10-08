package inventory

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func selectionFixture(t *testing.T) (*Service, Observation, Decision, authn.Claims, string) {
	t.Helper()
	s, o, d, claims := fixture(t)
	var port Row
	var bridgeID string
	for _, r := range o.Rows["Port"] {
		port = r
	}
	for _, b := range d.Bindings {
		if b.Table == "Bridge" {
			bridgeID = b.ManagementID
		}
		if b.Table == "Interface" {
			delete(d.Bindings, Key(b.Table, b.UUID))
		}
	}
	o.Rows["Interface"] = map[string]Row{}
	members := []any{}
	for _, name := range []string{"p10", "p2", "p1", "p02", "p90071992547409930", "p9007199254740993", "P2", "p2a"} {
		uuid := repository.NewID()
		o.Rows["Interface"][uuid] = Row{UUID: uuid, Values: map[string]any{"name": name, "type": "dummy", "link_state": []any{"up"}, "options": map[string]any{}}}
		d.Bindings[Key("Interface", uuid)] = Binding{Table: "Interface", UUID: uuid, State: "active", ManagementID: repository.NewID()}
		members = append(members, uuid)
	}
	port.Values["interfaces"] = members
	otherBridge, otherPort, otherInterface := repository.NewID(), repository.NewID(), repository.NewID()
	o.Rows["Bridge"][otherBridge] = Row{UUID: otherBridge, Values: map[string]any{"name": "separate-bridge", "ports": []any{otherPort}, "datapath_type": "dummy"}}
	o.Rows["Port"][otherPort] = Row{UUID: otherPort, Values: map[string]any{"name": "outside", "interfaces": []any{otherInterface}}}
	o.Rows["Interface"][otherInterface] = Row{UUID: otherInterface, Values: map[string]any{"name": "outside", "type": "dummy", "link_state": []any{"up"}, "options": map[string]any{}}}
	for table, uuid := range map[string]string{"Bridge": otherBridge, "Port": otherPort, "Interface": otherInterface} {
		d.Bindings[Key(table, uuid)] = Binding{Table: table, UUID: uuid, State: "active", ManagementID: repository.NewID()}
	}
	s.install(o, d)
	return s, o, d, claims, bridgeID
}

func selectionCode(err error) string {
	var problem *apitypes.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

func selectionPage(t *testing.T, s *Service, q url.Values, c authn.Claims) map[string]any {
	t.Helper()
	v, err := s.Read(context.Background(), "listInterfaces", nil, q, c)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}
func selectionNames(page map[string]any) []string {
	out := []string{}
	for _, item := range page["items"].([]map[string]any) {
		out = append(out, item["name"].(string))
	}
	return out
}

func TestInterfaceSelectionNaturalOrderAcrossBoundedPages(t *testing.T) {
	s, _, _, c, bridgeID := selectionFixture(t)
	q := url.Values{"limit": {"2"}, "bridge_id": {bridgeID}, "native_type": {"dummy"}, "link_state": {"up"}}
	names, identities := []string{}, map[string]bool{}
	for n := 0; ; n++ {
		if n > 8 {
			t.Fatal("unbounded pagination")
		}
		page := selectionPage(t, s, q, c)
		names = append(names, selectionNames(page)...)
		for _, item := range page["items"].([]map[string]any) {
			id := item["management_id"].(string)
			if identities[id] {
				t.Fatal("duplicate identity across pages")
			}
			identities[id] = true
			if item["bridge_ref"].(map[string]any)["id"] != bridgeID {
				t.Fatal("wrong Bridge scope")
			}
			if _, ok := item["linux_device"]; ok {
				t.Fatal("list sampled host devices")
			}
		}
		if page["next_cursor"] == nil {
			break
		}
		q.Set("cursor", page["next_cursor"].(string))
	}
	want := []string{"p1", "P2", "p2", "p2a", "p02", "p10", "p9007199254740993", "p90071992547409930"}
	if !slices.Equal(names, want) {
		t.Fatal(names, want)
	}
}

func TestInterfaceSelectionBindsEveryFilterAndPermission(t *testing.T) {
	s, _, _, c, bridgeID := selectionFixture(t)
	base := url.Values{"limit": {"1"}, "bridge_id": {bridgeID}, "native_type": {"dummy"}, "link_state": {"up"}}
	token := selectionPage(t, s, base, c)["next_cursor"].(string)
	for key, value := range map[string]string{"filter": "p", "native_type": "", "link_state": "down", "limit": "2", "bridge_id": ""} {
		q := url.Values{}
		for k, v := range base {
			q[k] = slices.Clone(v)
		}
		q.Set("cursor", token)
		if value == "" && key == "bridge_id" {
			q.Del(key)
		} else {
			q.Set(key, value)
		}
		_, err := s.Read(context.Background(), "listInterfaces", nil, q, c)
		if selectionCode(err) != "CURSOR_EXPIRED" {
			t.Fatalf("%s cursor accepted: %v", key, err)
		}
	}
	q := url.Values{"limit": {"1"}, "cursor": {token}, "bridge_id": {bridgeID}, "native_type": {"dummy"}, "link_state": {"up"}}
	for _, typ := range []string{"dummy", "", "missing-native"} {
		q.Set("native_type", typ)
		limited := c
		limited.Capabilities = []string{"inventory.read", "state.read"}
		_, err := s.Read(context.Background(), "listInterfaces", nil, q, limited)
		if selectionCode(err) != "CAPABILITY_DENIED" {
			t.Fatal("type oracle", err)
		}
	}
}

func TestInterfaceSelectionNativeDefaultsUnknownAndRetiredBridge(t *testing.T) {
	s, o, d, c, bridgeID := selectionFixture(t)
	for _, row := range o.Rows["Interface"] {
		switch row.Values["name"] {
		case "p1":
			row.Values["type"], row.Values["link_state"] = "", []any{}
		case "p2":
			delete(row.Values, "type")
			delete(row.Values, "link_state")
		case "p10":
			row.Values["type"], row.Values["link_state"] = "future-native", []any{"not-a-link-state"}
		case "p02":
			row.Values["link_state"] = []any{"down"}
		}
	}
	s.install(o, d)
	for _, tc := range []struct {
		q    url.Values
		want []string
	}{
		{url.Values{"native_type": {""}}, []string{"p1"}},
		{url.Values{"native_type": {"future-native"}}, []string{"p10"}},
		{url.Values{"link_state": {"unknown"}}, []string{"p1", "p2", "p10"}},
		{url.Values{"link_state": {"down"}}, []string{"p02"}},
		{url.Values{"native_type": {"absent-native"}}, []string{}},
	} {
		if got := selectionNames(selectionPage(t, s, tc.q, c)); !slices.Equal(got, tc.want) {
			t.Fatal(tc.q, got, tc.want)
		}
	}
	s.Unavailable("OVSDB_UNAVAILABLE")
	stale := selectionPage(t, s, url.Values{"bridge_id": {bridgeID}, "link_state": {"down"}}, c)
	if stale["source"].(map[string]any)["freshness"] != "stale" || stale["availability"] != "degraded" || !slices.Equal(selectionNames(stale), []string{"p02"}) {
		t.Fatal("stale selection lost its evidence boundary", stale)
	}
	// Same native Bridge and name, but a retired management identity cannot
	// silently follow its replacement. The active replacement can be selected.
	replacement := repository.NewID()
	for key, b := range d.Bindings {
		if b.Table == "Bridge" && b.ManagementID == bridgeID {
			b.ManagementID = replacement
			d.Bindings[key] = b
		}
	}
	s.install(o, d)
	_, err := s.Read(context.Background(), "listInterfaces", nil, url.Values{"bridge_id": {bridgeID}}, c)
	if selectionCode(err) != "BRIDGE_SCOPE_NOT_FOUND" {
		t.Fatal(err)
	}
	if len(selectionNames(selectionPage(t, s, url.Values{"bridge_id": {replacement}}, c))) != 8 {
		t.Fatal("active Bridge lost")
	}
	// Schema coverage and malformed inputs cannot become fabricated empty pages.
	for j := range o.Schema.Tables {
		if o.Schema.Tables[j].Name == "Interface" {
			for k := range o.Schema.Tables[j].Columns {
				if o.Schema.Tables[j].Columns[k].Name == "type" {
					o.Schema.Tables[j].Columns[k].Monitored = false
				}
			}
		}
	}
	s.install(o, d)
	_, err = s.Read(context.Background(), "listInterfaces", nil, url.Values{"native_type": {"dummy"}}, c)
	if selectionCode(err) != "INTERFACE_FILTER_UNSUPPORTED" {
		t.Fatal(err)
	}
	for _, q := range []url.Values{{"bridge_id": {"same-name"}}, {"link_state": {"UP"}}, {"native_type": {strings.Repeat("x", 65)}}, {"native_type": {"x", "y"}}} {
		if _, err := s.Read(context.Background(), "listInterfaces", nil, q, c); err == nil {
			t.Fatal("invalid selection accepted", q)
		}
	}
}

func TestInterfaceSelectionContractAndAmbiguousParents(t *testing.T) {
	s, o, d, c, bridgeID := selectionFixture(t)
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ := contract.Match("GET", "/api/v1/interfaces")
	q := url.Values{"bridge_id": {bridgeID}, "native_type": {""}, "link_state": {"unknown"}}
	if err := op.ValidateParameters(nil, q, func(string) []string { return nil }); err != nil {
		t.Fatal(err)
	}
	q.Set("link_state", "carrier")
	if err := op.ValidateParameters(nil, q, func(string) []string { return nil }); err == nil {
		t.Fatal("non-OVS state accepted")
	}
	var original Row
	for _, row := range o.Rows["Port"] {
		original = row
	}
	uuid := repository.NewID()
	o.Rows["Port"][uuid] = Row{UUID: uuid, Values: map[string]any{"name": "ambiguous", "interfaces": original.Values["interfaces"]}}
	d.Bindings[Key("Port", uuid)] = Binding{Table: "Port", UUID: uuid, State: "active", ManagementID: repository.NewID()}
	s.install(o, d)
	_, err = s.Read(context.Background(), "listInterfaces", nil, url.Values{"bridge_id": {bridgeID}}, c)
	if selectionCode(err) != "INVENTORY_RELATION_UNKNOWN" {
		t.Fatal("ambiguous scope became empty/known", err)
	}
}
