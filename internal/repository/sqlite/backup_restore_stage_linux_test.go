package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

func TestStageRestorePairPublishesOnePrivateCopy(t *testing.T) {
	manager, web := backupPairStores(t)
	storeBackupRestoreTestSecret(t, manager, secret.Privileged, secret.Provider, 2)
	storeBackupRestoreTestSecret(t, web, secret.TLS, secret.Provider, 4)
	backupRoot, stagingRoot := privateBackupPairRoot(t), privateBackupPairRoot(t)
	pair, err := PublishBackupPair(t.Context(), backupRoot, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := stageAsTestOwner(t.Context(), backupRoot, pair.ID, stagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	if stage.BackupPair != pair || stage.ID == pair.ID || stage.State != "sealed" || stage.BackupPair.IncludesMasterKeyFiles ||
		stage.ManagerSecurity != "owner-prepare-required" || stage.WebSecurity != "owner-handoff-and-prepare-required" ||
		stage.OVSReconciliation != "required" || stage.KeyEvidenceScope != "secret-record-version-metadata-only" {
		t.Fatalf("incorrect stage evidence: %+v", stage)
	}
	wantedKeys := []BackupRestoreKeys{
		{Partition: secret.Privileged, Required: []int{2}, Missing: []int{}, State: "unknown"},
		{Partition: secret.Session, Required: []int{}, Missing: []int{}, State: "no-secret-records"},
		{Partition: secret.TLS, Required: []int{4}, Missing: []int{}, State: "unknown"},
	}
	if !reflect.DeepEqual(stage.Keys, wantedKeys) {
		t.Fatalf("incorrect key version evidence: %+v", stage.Keys)
	}
	name := restoreStagePrefix + stage.ID
	if !exactPairEntries(stagingRoot, name) || !exactRestoreStageBody(filepath.Join(stagingRoot, name)) {
		t.Fatal("staging root contained unexpected or mutable outside-owner files")
	}
	for _, item := range []BackupPairSnapshot{pair.Manager, pair.Web} {
		kind := string(item.BackupManifest.Kind)
		sealedPath := filepath.Join(stagingRoot, name, kind, pairDatabaseFile)
		sourcePath := filepath.Join(backupRoot, backupPairPrefix+pair.ID, kind, pairDatabaseFile)
		sealed, err := os.Lstat(sealedPath)
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.Lstat(sourcePath)
		if err != nil || os.SameFile(source, sealed) || !safeFile(sealed, uint32(os.Geteuid())) {
			t.Fatalf("stage did not copy %s into a separate owner-only inode: %v", kind, err)
		}
	}
	if source, err := VerifyBackupPair(t.Context(), backupRoot, pair.ID); err != nil || source != pair ||
		!manager.Status().Writable || !web.Status().Writable {
		t.Fatalf("stage changed a source or live store: %v", err)
	}
	// The sealed stage verifies after separating it from the original source.
	if err := os.Rename(filepath.Join(backupRoot, backupPairPrefix+pair.ID), filepath.Join(backupRoot, "offline-backup-fixture")); err != nil {
		t.Fatal(err)
	}
	actual, err := verifyStageAsTestOwner(t.Context(), stagingRoot, stage.ID)
	if err != nil || !reflect.DeepEqual(actual, stage) {
		t.Fatalf("independent stage did not verify: %+v, %v", actual, err)
	}
}

func TestStageRestorePairRejectsUnsafeRootsAndUnsupportedKeyScope(t *testing.T) {
	manager, web := backupPairStores(t)
	storeBackupRestoreTestSecret(t, web, secret.TLS, "unsupported-provider", 1)
	backupRoot, stagingRoot := privateBackupPairRoot(t), privateBackupPairRoot(t)
	pair, err := PublishBackupPair(t.Context(), backupRoot, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, id, dest string
		want           error
	}{
		{"same root", pair.ID, backupRoot, repository.ErrInvalid},
		{"nested root", pair.ID, filepath.Join(backupRoot, "stage"), repository.ErrInvalid},
		{"ancestor root", pair.ID, filepath.Dir(backupRoot), repository.ErrInvalid},
		{"invalid UUID", "../" + pair.ID, stagingRoot, repository.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if value, err := stageAsTestOwner(t.Context(), backupRoot, test.id, test.dest); !errors.Is(err, test.want) || value.ID != "" {
				t.Fatalf("unsafe stage accepted: %+v, %v", value, err)
			}
		})
	}
	if err := os.Chmod(stagingRoot, 0720); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAsTestOwner(t.Context(), backupRoot, pair.ID, stagingRoot); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatalf("group-writable staging root accepted: %v", err)
	}
	if err := os.Chmod(stagingRoot, 0700); err != nil {
		t.Fatal(err)
	}
	parent := privateBackupPairRoot(t)
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(stagingRoot, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAsTestOwner(t.Context(), backupRoot, pair.ID, alias); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatalf("symlink staging root accepted: %v", err)
	}
	nested := filepath.Join(parent, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0720); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAsTestOwner(t.Context(), backupRoot, pair.ID, nested); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatalf("group-writable staging ancestor accepted: %v", err)
	}
	if value, err := stageAsTestOwner(t.Context(), backupRoot, pair.ID, stagingRoot); err == nil ||
		err.Error() != "BACKUP_SECRETSTORE_SCOPE_UNAVAILABLE" || value.ID != "" {
		t.Fatalf("unrecognized encryption provider accepted: %+v, %v", value, err)
	}
	if !exactPairEntries(stagingRoot) {
		t.Fatal("failure leaked a partial stage or leftover working tree")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := stageAsTestOwner(canceled, backupRoot, pair.ID, stagingRoot); !errors.Is(err, repository.ErrCanceled) || value.ID != "" {
		t.Fatalf("canceled restore returned a staged copy: %+v, %v", value, err)
	}
	if os.Geteuid() != 0 {
		if _, err := StageRestorePair(t.Context(), backupRoot, pair.ID, stagingRoot); !errors.Is(err, repository.ErrUnavailable) {
			t.Fatalf("unprivileged production entry point was accepted: %v", err)
		}
		if _, err := VerifyStagedRestorePair(t.Context(), stagingRoot, pair.ID); !errors.Is(err, repository.ErrUnavailable) {
			t.Fatalf("unprivileged verify entry point was accepted: %v", err)
		}
	}
}

func TestVerifyStagedRestorePairRejectsChangedBodyAndManifest(t *testing.T) {
	manager, web := backupPairStores(t)
	backupRoot, stagingRoot := privateBackupPairRoot(t), privateBackupPairRoot(t)
	pair, err := PublishBackupPair(t.Context(), backupRoot, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := stageAsTestOwner(t.Context(), backupRoot, pair.ID, stagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(stagingRoot, restoreStagePrefix+stage.ID)
	image := filepath.Join(directory, "web", pairDatabaseFile)
	t.Run("unsealed sidecar", func(t *testing.T) {
		writeBackupPairFixture(t, image+"-wal", nil)
		defer os.Remove(image + "-wal")
		expectInvalidRestoreStage(t, stagingRoot, stage.ID)
	})
	t.Run("missing image in private tree", func(t *testing.T) {
		rename := filepath.Join(stagingRoot, "private-test-image")
		if err := os.Rename(image, rename); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Rename(rename, image); err != nil {
				t.Error(err)
			}
		}()
		expectInvalidRestoreStage(t, stagingRoot, stage.ID)
	})
	t.Run("corrupted image", func(t *testing.T) {
		data, err := os.ReadFile(image)
		if err != nil || len(data) == 0 {
			t.Fatal(err)
		}
		corrupt := append([]byte(nil), data...)
		corrupt[0] ^= 0x01
		writeBackupPairFixture(t, image, corrupt)
		defer writeBackupPairFixture(t, image, data)
		expectInvalidRestoreStage(t, stagingRoot, stage.ID)
	})
	t.Run("outside-owner mode", func(t *testing.T) {
		if err := os.Chmod(filepath.Dir(image), 0710); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(filepath.Dir(image), 0700)
		expectInvalidRestoreStage(t, stagingRoot, stage.ID)
	})
	t.Run("forged readiness metadata", func(t *testing.T) {
		path := filepath.Join(directory, pairManifestFile)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		forged := strings.Replace(string(original), `"no-secret-records"`, `"verified-ready"`, 1)
		if forged == string(original) {
			t.Fatal("fixture has no key readiness evidence")
		}
		writeBackupPairFixture(t, path, []byte(forged))
		defer writeBackupPairFixture(t, path, original)
		expectInvalidRestoreStage(t, stagingRoot, stage.ID)
	})
	if _, err := verifyStageAsTestOwner(t.Context(), stagingRoot, stage.ID); err != nil {
		t.Fatal(err)
	}
}

func stageAsTestOwner(ctx context.Context, backupRoot, backupID, stagingRoot string) (RestoreStageManifest, error) {
	if os.Geteuid() == 0 {
		return StageRestorePair(ctx, backupRoot, backupID, stagingRoot)
	}
	return stageRestorePair(ctx, backupRoot, backupID, stagingRoot)
}

func verifyStageAsTestOwner(ctx context.Context, stagingRoot, stageID string) (RestoreStageManifest, error) {
	if os.Geteuid() == 0 {
		return VerifyStagedRestorePair(ctx, stagingRoot, stageID)
	}
	return verifyStagedRestorePair(ctx, stagingRoot, stageID)
}

func storeBackupRestoreTestSecret(t *testing.T, store *Store, partition, provider string, version int) {
	t.Helper()
	if err := store.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO secret_records VALUES(?,'synthetic-id','synthetic-purpose',?,?,X'AA')", partition, provider, version)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func expectInvalidRestoreStage(t *testing.T, root, id string) {
	t.Helper()
	if stage, err := verifyStageAsTestOwner(t.Context(), root, id); err == nil || stage.ID != "" {
		t.Fatalf("invalid stage accepted: %q, %v", stage.ID, err)
	}
}
