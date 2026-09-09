package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/ipc"
)

//go:embed assets/index.html
var landing []byte

type ManagerProbe interface {
	Probe(context.Context) (ipc.Health, error)
}

func BootstrapHandler(manager ManagerProbe) http.Handler {
	slots := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			r.Close = true
			w.Header().Set("Connection", "close")
			write(w, 400, ipc.Problem{Code: "INVALID_REQUEST"})
			return
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(landing)
		case "/healthz":
			write(w, 200, map[string]string{"state": "alive", "scope": "runtime-bootstrap"})
		case "/readyz", "/api/v1/runtime":
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				write(w, 429, ipc.Problem{Code: "BUSY"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			health, err := manager.Probe(ctx)
			if err != nil {
				write(w, 503, ipc.Problem{Code: "MANAGER_UNAVAILABLE"})
				return
			}
			write(w, 200, health)
		default:
			write(w, 404, ipc.Problem{Code: "NOT_AVAILABLE_IN_BOOTSTRAP"})
		}
	})
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
