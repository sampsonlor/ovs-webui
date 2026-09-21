package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedSPADeepLinksAndStaticSecurityBoundary(t *testing.T) {
	assets := fstest.MapFS{
		"assets/spa/index.html":          {Data: []byte(`<div id="app"></div>`)},
		"assets/spa/assets/index-abc.js": {Data: []byte(`/* static */`)},
	}
	h := staticHandler(assets)
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/", 200}, {"/ports", 200}, {"/changes/candidates/11111111-1111-4111-8111-111111111111", 200},
		{"/changes/transactions/11111111-1111-4111-8111-111111111111", 200}, {"/operations/jobs/11111111-1111-4111-8111-111111111111", 200},
		{"/assets/index-abc.js", 200}, {"/api/v1/unknown", 404}, {"/.env", 404}, {"/assets/../index.html", 404}, {"/assets/index.js.map", 404}, {"/unknown", 404},
	} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", test.path, nil))
		if r.Code != test.status {
			t.Fatalf("%s: got %d", test.path, r.Code)
		}
		csp := r.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-") || r.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("unsafe UI headers", csp)
		}
		if test.status == 200 && strings.HasPrefix(test.path, "/assets/") {
			if !strings.Contains(r.Header().Get("Cache-Control"), "immutable") {
				t.Fatal("hashed resource not cached")
			}
		} else if r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("shell or errors cached")
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("HEAD", "/ports", nil))
	if r.Code != 200 || r.Body.Len() != 0 {
		t.Fatal("HEAD not respected")
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/ports", strings.NewReader("{}")))
	if r.Code != 400 {
		t.Fatal("mutation treated as static navigation")
	}
	r = httptest.NewRecorder()
	staticHandler(fstest.MapFS{}).ServeHTTP(r, httptest.NewRequest("GET", "/ports", nil))
	if r.Code != 503 {
		t.Fatal("unbuilt UI advertised as ready")
	}
}
