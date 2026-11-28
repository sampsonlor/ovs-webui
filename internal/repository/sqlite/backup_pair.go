package sqlite

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	BackupPairConsistency      = "independent-sqlite-snapshots"
	MaxBackupPairManifestBytes = 8 << 10
	MaxBackupPairDatabaseBytes = 1 << 30
)

var ErrBackupPairTooLarge = errors.New("STORAGE_BACKUP_PAIR_TOO_LARGE")

// Each component can be restored only through the later, separately authorized
// reconciliation workflow. This manifest supplies internal integrity, not source authentication.
type BackupPairManifest struct {
	Format                 int                `json:"format"`
	ID                     string             `json:"id"`
	Consistency            string             `json:"consistency"`
	CreatedAt              time.Time          `json:"created_at"`
	IncludesMasterKeyFiles bool               `json:"includes_master_key_files"`
	Manager                BackupPairSnapshot `json:"manager"`
	Web                    BackupPairSnapshot `json:"web"`
}

type BackupPairSnapshot struct {
	BackupManifest BackupManifest `json:"backup_manifest"`
	DatabaseBytes  int64          `json:"database_bytes"`
	ManifestSHA256 string         `json:"manifest_sha256"`
}

var backupUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func boundedPairJSON(manifest any) ([]byte, error) {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(payload)+1 > MaxBackupPairManifestBytes {
		return nil, repository.ErrUnavailable
	}
	return append(payload, '\n'), nil
}

func exactPairJSON(data []byte, manifest any) bool {
	canonical, err := boundedPairJSON(manifest)
	return err == nil && bytes.Equal(data, canonical)
}

func decodeBackupPairManifest(data []byte, id string) (BackupPairManifest, error) {
	var m BackupPairManifest
	if len(data) > MaxBackupPairManifestBytes || !backupUUID.MatchString(id) ||
		json.Unmarshal(data, &m) != nil || m.Format != 1 || m.ID != id ||
		m.Consistency != BackupPairConsistency || m.CreatedAt.IsZero() || m.IncludesMasterKeyFiles ||
		!validPairSnapshot(m.Manager, repository.Manager) || !validPairSnapshot(m.Web, repository.Web) ||
		m.Manager.BackupManifest.DatabaseID == m.Web.BackupManifest.DatabaseID {
		return BackupPairManifest{}, repository.ErrUnavailable
	}
	// Byte equality also rejects duplicate/unknown keys and alternate encodings.
	if !exactPairJSON(data, m) {
		return BackupPairManifest{}, repository.ErrUnavailable
	}
	return m, nil
}

func validPairSnapshot(snapshot BackupPairSnapshot, kind repository.Kind) bool {
	m := snapshot.BackupManifest
	return snapshot.DatabaseBytes > 0 && snapshot.DatabaseBytes <= MaxBackupPairDatabaseBytes &&
		validPairDigest(snapshot.ManifestSHA256) && m.Format == 1 && m.Kind == kind &&
		backupUUID.MatchString(m.DatabaseID) && m.SchemaVersion > 0 &&
		repository.ValidID(m.SoftwareVersion) && validPairDigest(m.SHA256) && !m.CreatedAt.IsZero()
}

func validPairDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
