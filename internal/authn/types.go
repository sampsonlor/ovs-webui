// Package authn defines typed authentication messages shared across the private
// Unix IPC. Browser sessions never receive an opaque manager grant.
package authn

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	SessionIdle     = 15 * time.Minute
	SessionAbsolute = 8 * time.Hour
	Elevation       = 5 * time.Minute
	MaxPrincipals   = 1000
	MaxRoles        = 128
	MaxGrants       = 4096
	MaxTokens       = 4096
)

type Login struct {
	Provider string `json:"provider"`
	Username string `json:"username"`
	Password string `json:"password"`
}
type Claims struct {
	PrincipalID    string    `json:"principal_id"`
	CredentialID   string    `json:"credential_id"`
	CredentialKind string    `json:"credential_kind"`
	DisplayName    string    `json:"display_name"`
	Capabilities   []string  `json:"effective_capabilities"`
	Revision       string    `json:"permission_revision"`
	Epoch          string    `json:"epoch"`
	RequestEpoch   string    `json:"request_epoch"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	ElevatedUntil  time.Time `json:"elevated_until"`
}
type LoginResult struct {
	Grant  string `json:"grant"`
	Claims Claims `json:"claims"`
}
type Check struct {
	Capability string         `json:"capability"`
	Resources  []apitypes.Ref `json:"resources"`
}
type Reauthentication struct {
	Password string `json:"password"`
}
type Query struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}
type Command struct {
	Method       string          `json:"method"`
	URI          string          `json:"uri"`
	Epoch        string          `json:"epoch"`
	RequestID    string          `json:"request_id"`
	Precondition string          `json:"precondition"`
	Payload      json.RawMessage `json:"payload"`
}
type Response struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
	ETag   string          `json:"etag,omitempty"`
}

// Each method receives a credential, never actor/role/permission assertions.
// Check is advisory; Read and Execute re-evaluate their own compiled operation.
type Manager interface {
	Authenticate(context.Context, Login) (LoginResult, error)
	InspectAuth(context.Context, string) (Claims, error)
	CheckAuth(context.Context, string, Check) (Claims, error)
	Reauthenticate(context.Context, string, Reauthentication) (Claims, error)
	RevokeAuth(context.Context, string) error
	ReadAuth(context.Context, string, Query) (Response, error)
	ExecuteAuth(context.Context, string, Command) (apitypes.Result, error)
}
