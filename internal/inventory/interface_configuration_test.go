package inventory

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
)

func TestInterfaceConfigurationPermissionAndRevisionSeparation(t *testing.T) {
	s, o, d, claims := fixture(t)
	var binding Binding
	for _, b := range d.Bindings {
		if b.Table == "Interface" {
			binding = b
			break
		}
	}
	row := o.Rows["Interface"][binding.UUID]
	for j := range o.Schema.Tables {
		if o.Schema.Tables[j].Name == "Interface" {
			o.Schema.Tables[j].Columns = append(o.Schema.Tables[j].Columns, Column{Name: "ofport", Monitored: true})
			for _, name := range InterfaceConfigurationColumns {
				o.Schema.Tables[j].Columns = append(o.Schema.Tables[j].Columns, Column{Name: name, Monitored: true, Mutable: true, InterfaceConfigCompatible: true})
				row.Values[name] = "0"
			}
		}
	}
	row.Values["ofport_request"] = []any{}
	row.Values["ingress_policing_kpkts_burst"] = "9223372036854775807"
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ := contract.Match("GET", "/api/v1/interfaces/"+binding.ManagementID)
	read := func() map[string]any {
		t.Helper()
		s.install(o, d)
		v, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": binding.ManagementID}, url.Values{}, claims)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(v)
		if err := op.ValidateResponse(200, data); err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)
	}
	item := read()
	revision := item["config_revision"]
	fields := item["fields"].(map[string]any)
	for _, name := range InterfaceConfigurationColumns {
		f := fields[name].(map[string]any)
		if f["availability"] != "known" || f["editable"] != false || f["ownership"] != "unknown" || f["source"].(map[string]any)["authority"] != "ovsdb-configuration" {
			t.Fatal(f)
		}
	}
	row.Values["ofport"] = []any{"24"}
	observed := read()
	if observed["config_revision"] != revision || Digest(observed["fields"].(map[string]any)["ofport"].(map[string]any)["value"]) != Digest([]any{"24"}) {
		t.Fatal("assignment changed request revision")
	}
	row.Values["ingress_policing_rate"] = "1000"
	if read()["config_revision"] == revision {
		t.Fatal("configuration change did not change revision")
	}
	claims.Capabilities = []string{"inventory.read", "state.read"}
	item = read()
	fields = item["fields"].(map[string]any)
	for _, name := range InterfaceConfigurationColumns {
		f := fields[name].(map[string]any)
		if f["availability"] != "withheld" || f["value"] != nil || f["reason"] != "CONFIGURATION_WITHHELD" {
			t.Fatal("configuration leaked", f)
		}
	}
}

func TestInterfaceConfigurationMissingInvalidAndStale(t *testing.T) {
	s, o, d, claims := fixture(t)
	var b Binding
	for _, binding := range d.Bindings {
		if binding.Table == "Interface" {
			b = binding
			break
		}
	}
	row := o.Rows["Interface"][b.UUID]
	read := func() map[string]any {
		t.Helper()
		s.install(o, d)
		v, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, url.Values{}, claims)
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)["fields"].(map[string]any)
	}
	for _, name := range InterfaceConfigurationColumns {
		f := read()[name].(map[string]any)
		if f["availability"] != "unsupported" || f["value"] != nil {
			t.Fatal("missing column invented a value", f)
		}
	}
	for j := range o.Schema.Tables {
		if o.Schema.Tables[j].Name == "Interface" {
			o.Schema.Tables[j].Columns = append(o.Schema.Tables[j].Columns,
				Column{Name: "ofport_request", Monitored: true, InterfaceConfigCompatible: true},
				Column{Name: "ingress_policing_rate", Monitored: true, InterfaceConfigCompatible: true},
				Column{Name: "ingress_policing_burst", Monitored: true})
		}
	}
	for _, bad := range []any{[]any{"0"}, []any{"65280"}, []any{"1", "2"}, "1", []any{1.0}} {
		row.Values["ofport_request"] = bad
		f := read()["ofport_request"].(map[string]any)
		if f["availability"] != "unknown" || f["value"] != nil {
			t.Fatal("invalid optional integer accepted", f)
		}
	}
	row.Values["ingress_policing_rate"] = "-1"
	row.Values["ingress_policing_burst"] = "0"
	fields := read()
	if fields["ingress_policing_rate"].(map[string]any)["reason"] != "NATIVE_VALUE_INVALID" || fields["ingress_policing_burst"].(map[string]any)["reason"] != "SCHEMA_TYPE_UNSUPPORTED" {
		t.Fatal(fields)
	}
	delete(row.Values, "ofport_request")
	if read()["ofport_request"].(map[string]any)["reason"] != "NATIVE_VALUE_UNAVAILABLE" {
		t.Fatal("unknown replaced by native empty")
	}
	row.Values["ofport_request"] = []any{}
	o.Evidence.ObservedAt = time.Now().Add(-10 * time.Second)
	f := read()["ofport_request"].(map[string]any)
	if f["availability"] != "known" || f["source"].(map[string]any)["freshness"] != "stale" {
		t.Fatal("old configuration presented as fresh", f)
	}
}
