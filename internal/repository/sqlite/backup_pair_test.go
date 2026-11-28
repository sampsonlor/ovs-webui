package sqlite

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func exampleBackupPair() BackupPairManifest {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	entry := func(kind repository.Kind, id string) BackupPairSnapshot {
		return BackupPairSnapshot{
			BackupManifest: BackupManifest{Format: 1, Kind: kind, DatabaseID: id,
				SchemaVersion: 12, SoftwareVersion: "test-version", SHA256: strings.Repeat("a", 64), CreatedAt: at},
			DatabaseBytes: 4096, ManifestSHA256: strings.Repeat("b", 64),
		}
	}
	return BackupPairManifest{
		Format: 1, ID: "00000000-0000-4000-8000-000000000001", Consistency: BackupPairConsistency, CreatedAt: at,
		Manager: entry(repository.Manager, "00000000-0000-4000-8000-000000000002"),
		Web:     entry(repository.Web, "00000000-0000-4000-8000-000000000003"),
	}
}

func TestBackupPairManifestRequiresExactPrivateFormat(t *testing.T) {
	expected := exampleBackupPair()
	raw, err := boundedPairJSON(expected)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := decodeBackupPairManifest(raw, expected.ID); err != nil || decoded != expected {
		t.Fatalf("decode = %+v, %v", decoded, err)
	}
	for name, bad := range map[string][]byte{
		"alternate encoding":  append(bytes.Clone(raw), '\n'),
		"duplicate pair ID":   bytes.Replace(raw, []byte(`"format": 1,`), []byte(`"format": 1, "id":"`+expected.ID+`",`), 1),
		"unrecognized field":  bytes.Replace(raw, []byte(`"format": 1,`), []byte(`"format": 1, "extra": true,`), 1),
		"oversized manifest":  make([]byte, MaxBackupPairManifestBytes+1),
		"partial manifest":    raw[:len(raw)/2],
		"incorrect key claim": bytes.Replace(raw, []byte(`"includes_master_key_files": false`), []byte(`"includes_master_key_files": true`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBackupPairManifest(bad, expected.ID); err == nil {
				t.Fatal("invalid backup pair accepted")
			}
		})
	}
	if _, err := decodeBackupPairManifest(raw, repository.NewID()); err == nil {
		t.Fatal("accepted a manifest under a different pair identifier")
	}
	for name, change := range map[string]func(*BackupPairManifest){
		"mislabeled state":      func(m *BackupPairManifest) { m.Consistency = "atomic-cross-db" },
		"missing web component": func(m *BackupPairManifest) { m.Web = BackupPairSnapshot{} },
		"duplicate database ID": func(m *BackupPairManifest) { m.Web.BackupManifest.DatabaseID = m.Manager.BackupManifest.DatabaseID },
		"swapped components":    func(m *BackupPairManifest) { m.Manager, m.Web = m.Web, m.Manager },
		"invalid digest":        func(m *BackupPairManifest) { m.Web.BackupManifest.SHA256 = strings.Repeat("Z", 64) },
		"oversized database":    func(m *BackupPairManifest) { m.Web.DatabaseBytes = MaxBackupPairDatabaseBytes + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := expected
			change(&manifest)
			payload, err := boundedPairJSON(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeBackupPairManifest(payload, expected.ID); err == nil {
				t.Fatal("invalid backup pair accepted")
			}
		})
	}
}
