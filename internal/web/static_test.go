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
		{"/bridges", 200}, {"/bridges?cursor=sealed-page", 200}, {"/bridges/not-an-identity", 404}, {"/bridges/extra/path", 404},
		{"/switching/tunnels", 200}, {"/switching/tunnels/new", 200}, {"/switching/tunnels/11111111-1111-4111-8111-111111111111", 200}, {"/switching/tunnels/not-an-identity", 404}, {"/switching/tunnels/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/switching/mirrors", 200}, {"/switching/mirrors?filter=span", 200}, {"/switching/mirrors/new", 200}, {"/switching/mirrors/11111111-1111-4111-8111-111111111111", 200}, {"/switching/mirrors/not-an-identity", 404}, {"/switching/mirrors/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/switching/qos", 200}, {"/switching/qos?cursor=sealed-page", 200}, {"/switching/qos/11111111-1111-4111-8111-111111111111", 200}, {"/switching/qos/new", 404}, {"/switching/qos/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/visibility/telemetry", 200}, {"/visibility/telemetry?cursor=sealed-page", 200}, {"/visibility/telemetry/11111111-1111-4111-8111-111111111111", 200}, {"/visibility/telemetry/new", 404}, {"/visibility/telemetry/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/visibility/statistics", 200}, {"/visibility/statistics?port_id=11111111-1111-4111-8111-111111111111&limit=25", 200},
		{"/visibility/statistics/11111111-1111-4111-8111-111111111111", 200}, {"/visibility/statistics/11111111-1111-4111-8111-111111111111?filter=synthetic", 200},
		{"/visibility/statistics/new", 404}, {"/visibility/statistics/not-an-identity", 404}, {"/visibility/statistics/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/visibility/neighbors", 200}, {"/visibility/neighbors?family=ipv6&limit=25", 200},
		{"/visibility/neighbors/11111111-1111-4111-8111-111111111111", 200}, {"/visibility/neighbors/not-an-identity", 404},
		{"/visibility/neighbors/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/visibility/lldp", 200}, {"/visibility/lldp?limit=25", 200},
		{"/visibility/lldp/11111111-1111-4111-8111-111111111111", 200}, {"/visibility/lldp/not-an-identity", 404},
		{"/visibility/lldp/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/visibility/fdb", 200}, {"/visibility/fdb?vlan=101&limit=25", 200},
		{"/visibility/fdb/11111111-1111-4111-8111-111111111111?cursor=sealed-page", 200},
		{"/visibility/fdb/not-an-identity", 404}, {"/visibility/fdb/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/visibility/endpoints", 200}, {"/visibility/endpoints?vlan=0&limit=25", 200},
		{"/visibility/endpoints/11111111-1111-4111-8111-111111111111?ip=2001%3Adb8%3A%3A2", 200},
		{"/visibility/endpoints/not-an-identity", 404}, {"/visibility/endpoints/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/switching/topology", 200}, {"/switching/spanning-tree", 200}, {"/switching/spanning-tree?filter=synthetic", 200},
		{"/switching/spanning-tree/11111111-1111-4111-8111-111111111111", 200}, {"/switching/spanning-tree/not-an-identity", 404}, {"/switching/spanning-tree/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/switching/multicast", 200}, {"/switching/multicast?filter=synthetic", 200}, {"/switching/multicast/11111111-1111-4111-8111-111111111111", 200}, {"/switching/multicast/not-an-identity", 404}, {"/switching/multicast/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/switching/isolation", 200}, {"/switching/isolation?filter=synthetic&limit=1", 200},
		{"/switching/isolation/11111111-1111-4111-8111-111111111111", 200}, {"/switching/isolation/not-an-identity", 404}, {"/switching/isolation/11111111-1111-4111-8111-111111111111/extra", 404},
		{"/interfaces", 200}, {"/interfaces/11111111-1111-4111-8111-111111111111", 200}, {"/interfaces/not-an-identity", 404}, {"/interfaces/11111111-1111-4111-8111-111111111111/mtu", 200}, {"/interfaces/not-an-identity/mtu", 404},
		{"/interfaces/11111111-1111-4111-8111-111111111111/policing", 200}, {"/interfaces/not-an-identity/policing", 404}, {"/interfaces/policing", 404}, {"/interfaces/11111111-1111-4111-8111-111111111111/policing/extra", 404},
		{"/changes/transactions/11111111-1111-4111-8111-111111111111", 200}, {"/operations/jobs/11111111-1111-4111-8111-111111111111", 200},
		{"/operations/health", 200}, {"/operations/health/extra", 404},
		{"/admin/capabilities", 200}, {"/admin/capabilities/11111111-1111-4111-8111-111111111111", 200},
		{"/admin/capabilities/not-an-identity", 404}, {"/admin/capabilities/11111111-1111-4111-8111-111111111111/extra", 404},
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
