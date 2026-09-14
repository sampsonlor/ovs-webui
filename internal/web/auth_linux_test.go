//go:build linux

package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/sessions"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const browserPassword = "synthetic-browser-password"

func browserFixture(t *testing.T) (*publicapi.Handler, *Authentication, *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	open := func(kind repository.Kind) *sqlite.Store {
		root := t.TempDir()
		_ = os.Chmod(root, 0700)
		o := sqlite.Options{Path: filepath.Join(root, string(kind)+".db"), Kind: kind, SoftwareVersion: "auth-http-test"}
		if err := sqlite.Initialize(ctx, o); err != nil {
			t.Fatal(err)
		}
		s, err := sqlite.Open(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	managerStore := open(repository.Manager)
	webStore := open(repository.Web)
	manager, err := auth.New(managerStore, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Bootstrap(ctx, "admin", browserPassword); err != nil {
		t.Fatal(err)
	}
	mappings, err := sessions.New(ctx, webStore, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAuthentication(manager, mappings)
	h, err := publicapi.New(publicapi.Options{Authorizer: a, Gateway: a, PublicOrigin: "https://console.example"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, a, webStore
}
func browserCall(h http.Handler, method, path string, body any, cookie, csrf, origin string) *httptest.ResponseRecorder {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "https://console.example/api/v1"+path, bytes.NewReader(data))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		r.Header.Set("Cookie", sessionCookie+"="+cookie)
	}
	if csrf != "" {
		r.Header.Set("X-OVS-CSRF-Token", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	return out
}
func browserLogin(t *testing.T, h http.Handler) (string, string, map[string]any) {
	t.Helper()
	out := browserCall(h, "POST", "/sessions", map[string]any{"provider": "local", "username": "admin", "password": browserPassword}, "", "", "https://console.example")
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body.String())
	}
	cookies := out.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("cookie not issued")
	}
	var body map[string]any
	_ = json.Unmarshal(out.Body.Bytes(), &body)
	return cookies[0].Value, body["csrf_token"].(string), body
}
func TestBrowserLoginOriginCookieAndReauthentication(t *testing.T) {
	h, _, _ := browserFixture(t)
	body := map[string]any{"provider": "local", "username": "admin", "password": browserPassword}
	for _, origin := range []string{"", "https://hostile.example"} {
		out := browserCall(h, "POST", "/sessions", body, "", "", origin)
		if out.Code != 403 {
			t.Fatal("login CSRF", out.Code)
		}
	}
	cookie, csrf, session := browserLogin(t, h)
	out := browserCall(h, "GET", "/session", nil, cookie, "", "")
	if out.Code != 200 || strings.Contains(out.Body.String(), "ovsg_") || strings.Contains(out.Body.String(), browserPassword) {
		t.Fatal("invalid session response", out.Code, out.Body.String())
	}
	for _, token := range []string{"", "invalid"} {
		out = browserCall(h, "POST", "/session/reauthentication", map[string]string{"password": browserPassword}, cookie, token, "https://console.example")
		if out.Code != 403 {
			t.Fatal("reauth CSRF bypass", out.Code)
		}
	}
	out = browserCall(h, "POST", "/session/reauthentication", map[string]string{"password": browserPassword}, cookie, csrf, "https://console.example")
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var elevated map[string]any
	_ = json.Unmarshal(out.Body.Bytes(), &elevated)
	if elevated["principal_id"] != session["principal_id"] || elevated["expires_at"] != session["expires_at"] {
		t.Fatal("reauth changed principal or prolonged session")
	}
	// Use a separate login to inspect the complete cookie contract.
	out = browserCall(h, "POST", "/sessions", body, "", "", "https://console.example")
	c := out.Result().Cookies()[0]
	if c.Name != sessionCookie || c.Domain != "" || c.Path != "/" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
}
func TestWebDatabaseRestoreCannotUndoManagerLogout(t *testing.T) {
	h, a, webStore := browserFixture(t)
	cookie, csrf, _ := browserLogin(t, h)
	hash := authn.Hash(cookie)
	var envelope []byte
	var expiry int64
	if err := webStore.Read(context.Background(), func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT envelope,expires_at FROM browser_sessions WHERE session_hash=?", hash[:]).Scan(&envelope, &expiry)
	}); err != nil {
		t.Fatal(err)
	}
	out := browserCall(h, "DELETE", "/session", nil, cookie, csrf, "https://console.example")
	if out.Code != 204 || out.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed", out.Code, out.Body.String())
	}
	if err := webStore.Write(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO browser_sessions VALUES(?,?,?)", hash[:], envelope, expiry)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out = browserCall(h, "GET", "/session", nil, cookie, "", "")
	if out.Code != 401 {
		t.Fatal("restored web.db undid mgrd revocation", out.Code)
	}
	if _, err := a.Revalidate(context.Background(), publicapi.Subject{ID: repository.NewID(), CredentialKind: "cookie", SessionCookie: cookie}); err == nil {
		t.Fatal("forged subject survived revalidation")
	}
}
func TestBrowserCannotSupplyGrantRoleOrDuplicateSession(t *testing.T) {
	h, a, _ := browserFixture(t)
	cookie, _, _ := browserLogin(t, h)
	m, err := a.sessions.Load(context.Background(), cookie)
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range []http.Header{
		{"Authorization": []string{"Bearer " + m.Grant}},
		{"Authorization": []string{"Bearer " + authn.Secret("ovst_")}, "Cookie": []string{sessionCookie + "=" + cookie}},
		{"Cookie": []string{sessionCookie + "=" + cookie + "; " + sessionCookie + "=" + cookie}},
	} {
		r := httptest.NewRequest("GET", "https://console.example/api/v1/session", nil)
		r.Header = headers
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code < 400 {
			t.Fatal("credential confusion", headers, out.Code)
		}
	}
	body := map[string]any{"provider": "local", "username": "admin", "password": browserPassword, "actor": "admin", "capabilities": []string{"*"}, "mode": "expert"}
	out := browserCall(h, "POST", "/sessions", body, "", "", "https://console.example")
	if out.Code < 400 {
		t.Fatal("client supplied authority")
	}
}
