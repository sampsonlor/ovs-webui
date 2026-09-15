// Package inventory owns the read-only, provider-independent OVS object view.
// Provider and public handlers never assign or recover identities themselves.
package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
)

const (
	MaxRows          = 2048
	MaxSnapshotBytes = 4 << 20
	MaxRowBytes      = 16 << 10
	MaxIdentities    = 16384
	FreshFor         = 6 * time.Second
	ContinuityGap    = 30 * time.Second
)

type Reference struct {
	Table    string `json:"table"`
	Strength string `json:"strength"`
	Position string `json:"position"`
}
type Column struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	NativeType json.RawMessage `json:"native_type"`
	Mutable    bool            `json:"mutable"`
	Ephemeral  bool            `json:"ephemeral"`
	References []Reference     `json:"references"`
	Monitored  bool            `json:"monitored"`
}
type Table struct {
	Name    string     `json:"name"`
	Root    bool       `json:"root"`
	Indexes [][]string `json:"indexes"`
	Columns []Column   `json:"columns"`
}
type Schema struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Digest  string  `json:"digest"`
	Tables  []Table `json:"tables"`
}
type Row struct {
	UUID   string         `json:"uuid"`
	Values map[string]any `json:"values"`
}
type Rows map[string]map[string]Row

// A journal witness describes bytes already observed in the configured file.
// No database content, credentials or host path are published by the API.
type FileWitness struct {
	Available     bool   `json:"available"`
	Device        uint64 `json:"device"`
	Inode         uint64 `json:"inode"`
	Size          int64  `json:"size"`
	Offset        int64  `json:"offset"`
	Length        int    `json:"length"`
	Digest        string `json:"digest"`
	PriorMatches  bool   `json:"prior_matches"`
	ServerHasFile bool   `json:"server_has_file"`
}
type Evidence struct {
	Endpoint   string      `json:"endpoint"`
	Database   string      `json:"database"`
	Schema     string      `json:"schema"`
	Root       string      `json:"root"`
	Anchors    []string    `json:"anchors"`
	Peer       string      `json:"peer"`
	Boot       string      `json:"boot"`
	File       FileWitness `json:"file"`
	ObservedAt time.Time   `json:"observed_at"`
	Continuous bool        `json:"continuous"`
}
type Observation struct {
	Schema   Schema
	Rows     Rows
	Evidence Evidence
}
type Binding struct {
	ManagementID string `json:"management_id"`
	Table        string `json:"table"`
	UUID         string `json:"ovs_uuid"`
	State        string `json:"state"`
}
type Decision struct {
	Generation    string             `json:"generation"`
	State         string             `json:"state"`
	Reason        string             `json:"reason"`
	Evidence      Evidence           `json:"evidence"`
	PendingDigest string             `json:"pending_digest"`
	Bindings      map[string]Binding `json:"-"`
}
type Registry interface {
	Previous(context.Context) (Decision, error)
	Reconcile(context.Context, Observation) (Decision, error)
	Accept(context.Context, Observation, string, string) (Decision, error)
}
type Reader interface {
	Read(context.Context, string, map[string]string, url.Values, authn.Claims) (any, error)
}

func Operation(id string) bool {
	switch id {
	case "readInventory", "readInventorySchema", "listPorts", "readPort", "listBridges", "readBridge", "listInterfaces", "readInterface", "listBonds", "readBond":
		return true
	}
	return false
}
func Key(table, uuid string) string { return table + "/" + uuid }
func Digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func EvidenceDigest(e Evidence) string {
	// Same observed state can be reviewed offline; sampling time/transport PID do
	// not confer continuity or invalidate the review on an ordinary reconnect.
	e.ObservedAt = time.Time{}
	e.Continuous = false
	e.Peer = ""
	e.File.PriorMatches = false
	return Digest(e)
}
