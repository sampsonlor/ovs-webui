package apicontract

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCompiledContract(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Operations) < 120 {
		t.Fatalf("incomplete catalog: %d", len(c.Operations))
	}
	if c.Schemas["Id"].Validate("01994000-0000-7000-8000-000000000001") == nil {
		t.Fatal("request identity accepted as management identity")
	}
	op, path, _ := c.Match("PATCH", "/api/v1/candidate")
	if op == nil {
		t.Fatal("missing candidate")
	}
	for _, body := range []string{`{}`, `{"request_id":"bad"}`, `{"request_id":"01994000-0000-7000-8000-000000000001","extra":true}`, `{"request_id":"x","request_id":"y"}`} {
		if _, _, err := op.Decode([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if err := op.ValidateParameters(path, url.Values{}, http.Header{}.Values); err == nil {
		t.Fatal("missing idempotency headers accepted")
	}
	logout, _, _ := c.Match("DELETE", "/api/v1/session")
	if logout == nil {
		t.Fatal("logout missing")
	}
	if err := logout.ValidateResponse(204, nil); err != nil {
		t.Fatal(err)
	}
	if err := logout.ValidateResponse(204, []byte(`{}`)); err == nil {
		t.Fatal("204 body accepted")
	}
	capture, _, _ := c.Match("POST", "/api/v1/captures")
	for _, number := range []string{"1e1000000000", "1e-1000000000", strings.Repeat("9", 65)} {
		body := fmt.Sprintf(`{"request_id":"01994000-0000-7000-8000-000000000001","interface_id":"00000000-0000-4000-8000-000000000001","duration_seconds":1,"max_bytes":%s,"filter":""}`, number)
		if _, _, err := capture.Decode([]byte(body)); err == nil {
			t.Fatal("unbounded numeric spelling accepted")
		}
	}
	valid := []byte(`{"request_id":"01994000-0000-7000-8000-000000000001","interface_id":"00000000-0000-4000-8000-000000000001","duration_seconds":1,"max_bytes":1e2,"filter":""}`)
	if _, _, err := capture.Decode(valid); err != nil {
		t.Fatal("valid exact number rejected", err)
	}
}
