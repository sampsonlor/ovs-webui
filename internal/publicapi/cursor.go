package publicapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
)

type PageScope struct {
	Principal          string `json:"principal"`
	PermissionRevision string `json:"permission_revision"`
	Operation          string `json:"operation"`
	Filter             string `json:"filter"`
	Order              string `json:"order"`
	Generation         string `json:"generation"`
	Snapshot           string `json:"snapshot"`
}
type cursorEnvelope struct {
	Scope     PageScope `json:"scope"`
	LastKey   string    `json:"last_key"`
	ExpiresAt int64     `json:"expires_at"`
}
type Cursors struct {
	key [32]byte
	now func() time.Time
}

func NewCursors() *Cursors {
	c := &Cursors{now: time.Now}
	if _, err := rand.Read(c.key[:]); err != nil {
		panic("system randomness unavailable")
	}
	return c
}
func (c *Cursors) Issue(scope PageScope, lastKey string, expires time.Time) (string, error) {
	if !apitypes.ManagementID(scope.Principal) || !apitypes.ManagementID(scope.Snapshot) || len(lastKey) > 128 || scope.Operation == "" || scope.PermissionRevision == "" || len(scope.Filter) > 1024 || expires.After(c.now().Add(30*time.Second)) || !expires.After(c.now()) {
		return "", apitypes.Fail(422, "INVALID_CURSOR_SCOPE")
	}
	data, err := json.Marshal(cursorEnvelope{Scope: scope, LastKey: lastKey, ExpiresAt: expires.UnixMilli()})
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, c.key[:])
	_, _ = h.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}
func (c *Cursors) Read(token string, expected PageScope) (string, error) {
	fail := func() (string, error) { return "", apitypes.Fail(410, "CURSOR_EXPIRED") }
	if len(token) > 4096 {
		return fail()
	}
	pieces := strings.Split(token, ".")
	if len(pieces) != 2 {
		return fail()
	}
	data, err := base64.RawURLEncoding.DecodeString(pieces[0])
	if err != nil {
		return fail()
	}
	signature, err := base64.RawURLEncoding.DecodeString(pieces[1])
	if err != nil {
		return fail()
	}
	h := hmac.New(sha256.New, c.key[:])
	_, _ = h.Write(data)
	if !hmac.Equal(signature, h.Sum(nil)) {
		return fail()
	}
	var decoded cursorEnvelope
	if ipc.DecodeStrict(data, &decoded) != nil || decoded.Scope != expected || decoded.ExpiresAt <= c.now().UnixMilli() || len(decoded.LastKey) > 128 {
		return fail()
	}
	return decoded.LastKey, nil
}
