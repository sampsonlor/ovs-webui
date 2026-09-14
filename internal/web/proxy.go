package web

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// ProxyPolicy accepts only an explicit immediate-peer CIDR and a closed header
// allowlist. It never derives identity, Origin or CSRF from proxy assertions.
// webd still requires TLS on the proxy-to-webd connection.
type ProxyPolicy struct {
	host                 string
	peers                []netip.Prefix
	proto, forwardedHost bool
}

func NewProxyPolicy(origin, cidrs, headers string) (*ProxyPolicy, error) {
	p := &ProxyPolicy{}
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, apitypes.Fail(422, "PROXY_CONFIGURATION_INVALID")
		}
		p.host = u.Host
	}
	if cidrs != "" {
		for _, v := range strings.Split(cidrs, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(v))
			if err != nil {
				return nil, apitypes.Fail(422, "PROXY_CONFIGURATION_INVALID")
			}
			p.peers = append(p.peers, prefix)
		}
	}
	if headers != "" {
		if len(p.peers) == 0 || p.host == "" {
			return nil, apitypes.Fail(422, "PROXY_CONFIGURATION_INVALID")
		}
		for _, v := range strings.Split(headers, ",") {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "x-forwarded-proto":
				if p.proto {
					return nil, apitypes.Fail(422, "PROXY_CONFIGURATION_INVALID")
				}
				p.proto = true
			case "x-forwarded-host":
				if p.forwardedHost {
					return nil, apitypes.Fail(422, "PROXY_CONFIGURATION_INVALID")
				}
				p.forwardedHost = true
			default:
				return nil, apitypes.Fail(422, "PROXY_HEADER_UNSUPPORTED")
			}
		}
	}
	if len(p.peers) > 0 && headers == "" {
		return nil, apitypes.Fail(422, "PROXY_CONFIGURATION_INVALID")
	}
	return p, nil
}
func (p *ProxyPolicy) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		peer, e := netip.ParseAddr(host)
		trusted := false
		if err == nil && e == nil {
			peer = peer.Unmap()
			for _, prefix := range p.peers {
				if prefix.Contains(peer) {
					trusted = true
					break
				}
			}
		}
		effectiveHost := r.Host
		reject := false
		if trusted {
			if p.proto && len(r.Header.Values("X-Forwarded-Proto")) > 0 {
				values := r.Header.Values("X-Forwarded-Proto")
				reject = len(values) != 1 || values[0] != "https"
			}
			if p.forwardedHost && len(r.Header.Values("X-Forwarded-Host")) > 0 {
				values := r.Header.Values("X-Forwarded-Host")
				if len(values) != 1 || values[0] != p.host {
					reject = true
				} else {
					effectiveHost = values[0]
				}
			}
		}
		// Remove every forwarded assertion, including non-allowlisted identity fields,
		// before the actual public handler and its independent Origin/CSRF checks.
		r = r.Clone(r.Context())
		r.Host = effectiveHost
		for name := range r.Header {
			lower := strings.ToLower(name)
			if lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-auth-") || lower == "x-remote-user" {
				r.Header.Del(name)
			}
		}
		if reject || r.TLS == nil || (p.host != "" && effectiveHost != p.host) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(421)
			_, _ = w.Write([]byte(`{"error":{"code":"HTTPS_AUTHORITY_REJECTED"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
