// Package supportbundle builds the bounded, redacted support archive from read-only snapshots.
package supportbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/redact"
)

type Kind string

const (
	KindRuntime Kind = "runtime"
	KindOVS     Kind = "ovs"
	KindHealth  Kind = "health"
	KindEvents  Kind = "events"
	KindAudit   Kind = "audit"
)

const (
	TTL                 = 15 * time.Minute
	MaxBundleBytes      = 512 << 10
	MaxSectionBytes     = 64 << 10
	MaxEvidenceRows     = 64
	MaxHealthComponents = 32
)

var (
	ErrInvalidScope = errors.New("invalid support bundle scope")
	ErrInvalidTime  = errors.New("invalid support bundle timestamp")
	ErrTooLarge     = errors.New("support bundle exceeds byte limit")
)

var kinds = [...]Kind{KindRuntime, KindOVS, KindHealth, KindEvents, KindAudit}

type Inputs struct {
	Runtime *RuntimeSnapshot
	OVS     *OVSSnapshot
	Health  *HealthSnapshot
	Events  *EvidencePage
	Audit   *EvidencePage
}

type RuntimeSnapshot struct {
	Version, Architecture, Platform         string
	ConfigurationReady, AuthenticationReady bool
}

type OVSSnapshot struct {
	Version, State string
	Topology       *TopologyCounts
}

type TopologyCounts struct {
	Bridges    int `json:"bridges"`
	Ports      int `json:"ports"`
	Interfaces int `json:"interfaces"`
}

type HealthSnapshot struct {
	Components []HealthComponent
	Truncated  bool
}

type HealthComponent struct {
	Key, State       string
	ObservedAt       *time.Time
	Active, Capacity *int
}

type EvidencePage struct {
	// Rows are the newest page of the authorized evidence view; upstream
	// pagination or an exhausted source budget should set Truncated.
	Rows      []EvidenceRow
	Truncated bool
}

type EvidenceRow struct {
	Operation, Outcome string
	ObservedAt         time.Time
}

type SectionState string

const (
	SectionIncluded    SectionState = "included"
	SectionPartial     SectionState = "partial"
	SectionTruncated   SectionState = "truncated"
	SectionNoData      SectionState = "no-data"
	SectionUnavailable SectionState = "unavailable"
)

type Manifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Sections  []Section `json:"sections"`
	Redaction Redaction `json:"redaction"`
}

func (m Manifest) Expired(now time.Time) bool {
	return m.ExpiresAt.IsZero() || !now.Before(m.ExpiresAt)
}

type Redaction struct {
	Policy  string   `json:"policy"`
	Omitted []string `json:"omitted"`
}

type Section struct {
	Kind   Kind         `json:"kind"`
	State  SectionState `json:"state"`
	File   string       `json:"file,omitempty"`
	Bytes  int          `json:"bytes,omitempty"`
	SHA256 string       `json:"sha256,omitempty"`
	Reason string       `json:"reason,omitempty"`
}

type Bundle struct {
	Bytes    []byte
	Manifest Manifest
}

func ValidateKinds(requested []Kind) error {
	_, err := orderedKinds(requested)
	return err
}

func orderedKinds(requested []Kind) ([]Kind, error) {
	if len(requested) == 0 || len(requested) > len(kinds) {
		return nil, ErrInvalidScope
	}
	var used [len(kinds)]bool
	for _, candidate := range requested {
		position := -1
		for i, kind := range kinds {
			if kind == candidate {
				position = i
				break
			}
		}
		if position < 0 || used[position] {
			return nil, ErrInvalidScope
		}
		used[position] = true
	}
	ordered := make([]Kind, 0, len(requested))
	for i, kind := range kinds {
		if used[i] {
			ordered = append(ordered, kind)
		}
	}
	return ordered, nil
}

// Build has no system, network, or file access. The caller owns source permissions and artifact retention.
func Build(ctx context.Context, now time.Time, requested []Kind, in Inputs) (Bundle, error) {
	ordered, err := orderedKinds(requested)
	if err != nil {
		return Bundle{}, err
	}
	created := publicTime(now)
	expires := publicTime(now.Add(TTL))
	if created == nil || expires == nil {
		return Bundle{}, ErrInvalidTime
	}
	manifest := Manifest{
		Version: 1, CreatedAt: *created, ExpiresAt: *expires,
		Sections: make([]Section, 0, len(ordered)),
		Redaction: Redaction{Policy: "strict-summary-v1", Omitted: []string{
			"secrets-and-authentication-material", "identities-and-host-addresses", "object-names-and-native-options",
			"raw-logs-and-command-output", "files-and-environment",
		}},
	}
	payloads := make([][]byte, 0, len(ordered))
	for _, kind := range ordered {
		if err := ctx.Err(); err != nil {
			return Bundle{}, err
		}
		section, document := project(kind, in)
		var payload []byte
		if document != nil {
			payload, err = encodeDocument(document)
			if err != nil {
				return Bundle{}, err
			}
			if len(payload) > MaxSectionBytes {
				section.State, section.Reason = SectionTruncated, "section-byte-limit"
				payload = nil
			} else {
				section.Bytes = len(payload)
				digest := sha256.Sum256(payload)
				section.SHA256 = hex.EncodeToString(digest[:])
				section.File = string(kind) + ".json"
			}
		}
		manifest.Sections = append(manifest.Sections, section)
		payloads = append(payloads, payload)
	}

	manifestBytes, err := encodeDocument(manifest)
	if err != nil {
		return Bundle{}, err
	}
	var data archiveBuffer
	archive := zip.NewWriter(&data)
	if err := writeEntry(archive, "manifest.json", manifestBytes); err != nil {
		return Bundle{}, err
	}
	for i, section := range manifest.Sections {
		if section.File != "" {
			if err := writeEntry(archive, section.File, payloads[i]); err != nil {
				return Bundle{}, err
			}
		}
	}
	if err := archive.Close(); err != nil {
		return Bundle{}, err
	}
	if err := ctx.Err(); err != nil {
		return Bundle{}, err
	}
	return Bundle{Bytes: data.buffer.Bytes(), Manifest: manifest}, nil
}

func encodeDocument(projected any) ([]byte, error) {
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, err
	}
	// The allowlist is the first boundary. Apply the shared redactor as well
	// so future typed fields cannot silently widen the ordinary export policy.
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	redacted, err := json.MarshalIndent(redact.Document(document), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(redacted, '\n'), nil
}

func writeEntry(archive *zip.Writer, name string, data []byte) error {
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

type archiveBuffer struct{ buffer bytes.Buffer }

func (b *archiveBuffer) Write(chunk []byte) (int, error) {
	if len(chunk) > MaxBundleBytes-b.buffer.Len() {
		return 0, ErrTooLarge
	}
	return b.buffer.Write(chunk)
}
