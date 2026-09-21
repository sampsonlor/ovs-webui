package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// Run pnpm frontend:build before compiling release binaries. The source landing
// page keeps Go-only tests buildable; missing SPA assets fail explicitly at HTTP.
//
//go:embed assets
var staticAssets embed.FS

var uiRoute = regexp.MustCompile(`^/(?:ports(?:/[0-9a-f-]{36}(?:/vlan)?)?|bridges/[0-9a-f-]{36}|interfaces/[0-9a-f-]{36}|changes/(?:candidate|candidates/[0-9a-f-]{36}|validations/[0-9a-f-]{36}|transactions(?:/[0-9a-f-]{36})?)|operations/(?:jobs|events|audit)(?:/[0-9a-f-]{36})?|overview|visibility|administration)?$`)

func staticHandler(assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.URL.RawPath != "" {
			r.Close = true
			w.Header().Set("Connection", "close")
			http.Error(w, "Invalid static request", http.StatusBadRequest)
			return
		}
		name := "index.html"
		asset := strings.HasPrefix(r.URL.Path, "/assets/")
		if asset {
			name = strings.TrimPrefix(r.URL.Path, "/")
			if !fs.ValidPath(name) || path.Clean(name) != name || (path.Ext(name) != ".js" && path.Ext(name) != ".css") {
				http.NotFound(w, r)
				return
			}
		} else if !uiRoute.MatchString(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		body, err := fs.ReadFile(assets, "assets/spa/"+name)
		if err != nil {
			if !asset {
				http.Error(w, "UI assets unavailable. Build the frontend before packaging ovs-webd.", http.StatusServiceUnavailable)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		if asset {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}
