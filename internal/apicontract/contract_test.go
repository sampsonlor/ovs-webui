package apicontract

import (
	"net/http"
	"net/url"
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
}
