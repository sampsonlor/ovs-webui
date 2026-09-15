package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyTrustRequiresImmediatePeerAndExplicitHeaders(t *testing.T) {
	p, err := NewProxyPolicy("https://console.example", "10.0.0.0/24", "X-Forwarded-Proto,X-Forwarded-Host")
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "console.example" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-User") != "" || r.Header.Get("Origin") != "https://hostile.example" {
			t.Error("proxy altered origin or leaked assertions")
		}
		w.WriteHeader(204)
	})
	for _, tc := range []struct {
		name, peer, host, proto, forwardedHost string
		want                                   int
	}{
		{"untrusted ignored", "192.0.2.1:42", "console.example", "http", "hostile.example", 204},
		{"trusted", "10.0.0.1:42", "internal.example", "https", "console.example", 204},
		{"untrusted cannot fix host", "192.0.2.1:42", "internal.example", "https", "console.example", 421},
		{"plain proxy rejected", "10.0.0.1:42", "console.example", "http", "console.example", 421},
		{"chain rejected", "10.0.0.1:42", "console.example", "https, http", "console.example", 421},
		{"forged hostname", "10.0.0.1:42", "console.example", "https", "hostile.example", 421},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://"+tc.host+"/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			r.Header.Set("X-Forwarded-User", "admin")
			r.Header.Set("Origin", "https://hostile.example")
			w := httptest.NewRecorder()
			p.Handler(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code)
			}
		})
	}
	for _, headers := range []string{"Authorization", "Origin", "X-Forwarded-For", "X-Forwarded-Host,X-Forwarded-Host"} {
		if _, err := NewProxyPolicy("https://console.example", "10.0.0.0/24", headers); err == nil {
			t.Fatal("unsupported trust configuration")
		}
	}
}
