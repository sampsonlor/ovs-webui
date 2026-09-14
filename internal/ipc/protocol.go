package ipc

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const (
	MaxBodyBytes     = 1 << 20
	MaxHeaderBytes   = 16 << 10
	MaxResponseBytes = 64 << 10
	MaxConnections   = 96
	MaxJSONDepth     = 16
	MaxJSONArray     = 4096
	MaxJSONTokens    = 16384
	MaxJSONString    = 64 << 10
	HeaderTimeout    = 2 * time.Second
	RequestTimeout   = 5 * time.Second
)

//go:embed protocol.json
var manifest []byte

type Protocol struct {
	Major    int    `json:"protocol_major"`
	Minor    int    `json:"protocol_minor"`
	Software string `json:"software_version"`
	Digest   string `json:"schema_digest"`
}

func CurrentProtocol(software string) Protocol {
	var compact bytes.Buffer
	if err := json.Compact(&compact, manifest); err != nil {
		panic("invalid embedded protocol manifest")
	}
	sum := sha256.Sum256(compact.Bytes())
	return Protocol{Major: 1, Minor: 0, Software: software, Digest: hex.EncodeToString(sum[:])}
}

type HandshakeReply struct {
	Accepted bool     `json:"accepted"`
	Protocol Protocol `json:"protocol"`
}

type Health struct {
	State               string `json:"state"`
	Scope               string `json:"scope"`
	AuthenticationReady bool   `json:"authentication_ready"`
	ConfigurationReady  bool   `json:"configuration_ready"`
}

func BootstrapHealth() Health {
	return Health{State: "ready", Scope: "runtime-bootstrap"}
}

type InspectRequest struct{}

type Operation struct {
	Name       string
	Capability string
	Class      Class
}

var inspectOperation = Operation{Name: "runtime.inspect", Capability: "state.read", Class: Read}

// Implementations must validate the current mgrd grant, capability and policy.
// Socket credentials and handshake success never confer a business permission.
type Authorizer interface {
	Authorize(context.Context, string, Operation) error
}

var ErrDenied = errors.New("operation denied")
var ErrAuthUnavailable = errors.New("authorization service unavailable")

type DenyAllAuthorizer struct{}

func (DenyAllAuthorizer) Authorize(context.Context, string, Operation) error {
	return ErrAuthUnavailable
}

type Problem struct {
	Code string `json:"code"`
}
type RemoteError struct {
	Code   string
	Status int
}

func (e *RemoteError) Error() string { return e.Code }
