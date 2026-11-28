package backupop

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestReadPublishedRestoreManifestRequiresExactRootOwnedReport(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("exact root-owned manifest mode requires root")
	}
	manifest := FreshRestoreManifest{Format: 1, StageID: repository.NewID(), BackupPairID: repository.NewID(),
		State: "offline-prepared", CreatedAt: time.Unix(1738368000, 0).UTC(), OVSReconciliationRequired: true}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readPublishedRestoreManifest(t.Context(), path); err != nil || got != manifest {
		t.Fatalf("exact published manifest rejected: %v", err)
	}
	for _, data := range [][]byte{
		bytes.Replace(encoded.Bytes(), []byte(`"format":1`), []byte(`"format":1,"format":1`), 1),
		bytes.Replace(encoded.Bytes(), []byte(`"format":1`), []byte(`"format":1,"unknown":"field"`), 1),
		bytes.Repeat([]byte{' '}, 4097),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPublishedRestoreManifest(t.Context(), path); !errors.Is(err, repository.ErrUnavailable) {
			t.Fatalf("noncanonical published manifest accepted: %v", err)
		}
	}
	alias := filepath.Join(filepath.Dir(path), "alias.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readPublishedRestoreManifest(t.Context(), alias); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatalf("symlinked report accepted: %v", err)
	}
}
