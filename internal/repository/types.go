// Package repository defines storage contracts without leaking SQLite types.
package repository

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

type Kind string

const (
	Web         Kind = "web"
	Manager     Kind = "manager"
	MaxDocument      = 1 << 20
)

var (
	ErrUnavailable   = errors.New("STORAGE_UNAVAILABLE")
	ErrInvalid       = errors.New("STORAGE_INVALID_INPUT")
	ErrConflict      = errors.New("STORAGE_CONFLICT")
	ErrNotFound      = errors.New("STORAGE_NOT_FOUND")
	ErrBusy          = errors.New("STORAGE_BUSY")
	ErrCanceled      = errors.New("STORAGE_CANCELED")
	ErrCommitUnknown = errors.New("STORAGE_COMMIT_UNKNOWN")
)

// Unknown commits must be reconciled by the original persistent request ID.
// They are never permission to dispatch or repeat a provider mutation.
type Status struct {
	State         string `json:"state"`
	Code          string `json:"code,omitempty"`
	Warning       string `json:"warning,omitempty"`
	Readable      bool   `json:"readable"`
	Writable      bool   `json:"writable"`
	SchemaVersion int    `json:"schema_version"`
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system randomness unavailable")
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func ValidID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func ValidDocument(b json.RawMessage) bool {
	return len(b) > 0 && len(b) <= MaxDocument && json.Valid(b)
}

// These are immutable storage envelopes, not validated switching operations.
type Candidate struct {
	ID, OwnerID, Revision string
	Document              json.RawMessage
}
type Handoff struct {
	RequestID, TransactionID, CorrelationID, OwnerID, CandidateID, CandidateRevision, PayloadHash string
	Document                                                                                      json.RawMessage
	ReceiptID                                                                                     string
}
type Receipt struct{ ID, RequestID, TransactionID, CorrelationID, PayloadHash, Stage string }
type MetadataKind string

const (
	Preference MetadataKind = "preference"
	Label      MetadataKind = "label"
	Profile    MetadataKind = "profile"
	Override   MetadataKind = "override"
)

type Metadata struct {
	Kind        MetadataKind
	OwnerID, ID string
	Document    json.RawMessage
}

// Reusable grants are SecretStore references, never plaintext in web.db.
type SessionMapping struct {
	SessionHash                 [32]byte
	PrincipalID, GrantSecretRef string
	ExpiresAt                   time.Time
}
type Principal struct {
	ID, Name         string
	Disabled         bool
	PasswordVerifier []byte
}
