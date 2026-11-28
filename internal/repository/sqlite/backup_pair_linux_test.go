package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestPublishBackupPairPreservesBothWALSnapshots(t *testing.T) {
	manager, web := backupPairStores(t)
	ctx := t.Context()
	// These synthetic rows exercise the persistent generation/checkpoint and
	// sealed SecretStore storage that a management backup must preserve.
	if err := manager.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO principals VALUES('test-principal','test-principal',1,X'01');
			INSERT INTO generations VALUES('test-gen',X'02',1);
			INSERT INTO request_receipts VALUES('test-receipt','test-request','test-tx','test-corr','test-principal','test-candidate','rev','digest',X'03','received',1);
			INSERT INTO transaction_journal VALUES('test-tx',1,'confirmed',X'04');
			INSERT INTO jobs VALUES('test-job','test-tx','succeeded',X'05');
			INSERT INTO candidate_validations VALUES('test-validation','test-principal','test-candidate','rev','test-job','test-changeset',1,X'06',X'07');
			INSERT INTO field_executions VALUES('test-tx','test-job','test-principal','test-validation','confirmed',1,X'08');
			INSERT INTO safe_applies VALUES('test-tx','test-principal','epoch','test-request','confirmed','manager',X'09');
			INSERT INTO last_known_good VALUES(1,'test-tx','test-gen',1,X'1020');
			INSERT INTO secret_records VALUES('manager','test-secret','test','test',1,X'7771');
		`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := web.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO metadata VALUES('label','test-principal','test-label',X'3334');
			INSERT INTO secret_records VALUES('web','test-secret','test','test',1,X'5556');
		`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{manager, web} {
		info, err := os.Stat(store.options.Path + "-wal")
		if err != nil || info.Size() <= 32 {
			t.Fatalf("no WAL data for %s: %v", store.Kind(), err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(store.options.Path), "master.key"), []byte("synthetic external key file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root := privateBackupPairRoot(t)
	created, err := PublishBackupPair(ctx, root, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	if created.Consistency != BackupPairConsistency || created.IncludesMasterKeyFiles ||
		created.Manager.BackupManifest.SchemaVersion != manager.Status().SchemaVersion ||
		created.Web.BackupManifest.SchemaVersion != web.Status().SchemaVersion ||
		created.Manager.BackupManifest.SoftwareVersion != manager.options.SoftwareVersion ||
		created.Web.BackupManifest.SoftwareVersion != web.options.SoftwareVersion {
		t.Fatalf("unexpected pair identity %+v", created)
	}
	verified, err := VerifyBackupPair(ctx, root, created.ID)
	if err != nil || verified != created {
		t.Fatalf("published pair = %+v, %v", verified, err)
	}
	if !exactPairEntries(root, backupPairPrefix+created.ID) {
		t.Fatal("published pair created additional visible or staged backups")
	}
	pair := filepath.Join(root, backupPairPrefix+created.ID)
	for _, snapshot := range []BackupPairSnapshot{created.Manager, created.Web} {
		dir := filepath.Join(pair, string(snapshot.BackupManifest.Kind))
		if !exactPairEntries(dir, pairDatabaseFile, pairManifestFile) {
			t.Fatal("published an unexpected key/file/sidecar alongside SQLite")
		}
	}
	assertBackupPairValue(t, pair, repository.Manager, "SELECT hex(evidence) FROM generations", "02")
	assertBackupPairValue(t, pair, repository.Manager, "SELECT hex(document) FROM last_known_good", "1020")
	assertBackupPairValue(t, pair, repository.Manager, "SELECT hex(envelope) FROM secret_records", "7771")
	assertBackupPairValue(t, pair, repository.Web, "SELECT hex(document) FROM metadata", "3334")
	assertBackupPairValue(t, pair, repository.Web, "SELECT hex(envelope) FROM secret_records", "5556")
}

func TestVerifyBackupPairRejectsTamperingSidecarsAndSubstitution(t *testing.T) {
	manager, web := backupPairStores(t)
	root := privateBackupPairRoot(t)
	pair, err := PublishBackupPair(t.Context(), root, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, backupPairPrefix+pair.ID)
	managerPath := filepath.Join(path, "manager", pairDatabaseFile)
	webPath := filepath.Join(path, "web", pairDatabaseFile)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			extra := webPath + suffix
			if err := os.WriteFile(extra, nil, 0600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(extra)
			expectInvalidBackupPair(t, root, pair.ID)
		})
	}
	for name, file := range map[string]string{
		"database-edited":    managerPath,
		"submanifest-edit":   filepath.Join(path, "web", pairManifestFile),
		"pair-manifest-edit": filepath.Join(path, pairManifestFile),
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			defer writeBackupPairFixture(t, file, data)
			writeBackupPairFixture(t, file, append(data, '\n'))
			expectInvalidBackupPair(t, root, pair.ID)
		})
	}
	t.Run("missing database", func(t *testing.T) {
		moved := filepath.Join(root, "missing-web-test")
		if err := os.Rename(webPath, moved); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Rename(moved, webPath); err != nil {
				t.Error(err)
			}
		}()
		expectInvalidBackupPair(t, root, pair.ID)
	})
	t.Run("symlink database", func(t *testing.T) {
		moved := filepath.Join(root, "symlink-test")
		if err := os.Rename(webPath, moved); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = os.Remove(webPath)
			if err := os.Rename(moved, webPath); err != nil {
				t.Error(err)
			}
		}()
		if err := os.Symlink(moved, webPath); err != nil {
			t.Fatal(err)
		}
		expectInvalidBackupPair(t, root, pair.ID)
	})
	t.Run("replaced metadata", func(t *testing.T) {
		file := filepath.Join(path, pairManifestFile)
		original, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		defer writeBackupPairFixture(t, file, original)
		altered := pair
		altered.Web.BackupManifest.DatabaseID = repository.NewID()
		raw, err := boundedPairJSON(altered)
		if err != nil {
			t.Fatal(err)
		}
		writeBackupPairFixture(t, file, raw)
		expectInvalidBackupPair(t, root, pair.ID)
	})
	t.Run("oversized database", func(t *testing.T) {
		data, err := os.ReadFile(webPath)
		if err != nil {
			t.Fatal(err)
		}
		defer writeBackupPairFixture(t, webPath, data)
		if err := os.Truncate(webPath, MaxBackupPairDatabaseBytes+1); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyBackupPair(t.Context(), root, pair.ID); !errors.Is(err, ErrBackupPairTooLarge) {
			t.Fatalf("oversized verification = %v", err)
		}
	})
	if _, err := VerifyBackupPair(t.Context(), root, pair.ID); err != nil {
		t.Fatalf("restored fixture no longer verifies: %v", err)
	}
	if _, err := VerifyBackupPair(t.Context(), root, "../backup-pair-"+pair.ID); !errors.Is(err, repository.ErrInvalid) {
		t.Fatalf("unscoped identifier = %v", err)
	}
}

func TestPublishBackupPairFailureCannotExposeOneDatabase(t *testing.T) {
	manager, web := backupPairStores(t)
	root := privateBackupPairRoot(t)
	if _, err := PublishBackupPair(t.Context(), root, web, manager); !errors.Is(err, repository.ErrInvalid) {
		t.Fatalf("wrong store order = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PublishBackupPair(canceled, root, manager, web); !errors.Is(err, repository.ErrCanceled) {
		t.Fatalf("canceled publication = %v", err)
	}
	if err := web.Close(); err != nil {
		t.Fatal(err)
	}
	if manifest, err := PublishBackupPair(t.Context(), root, manager, web); err == nil || manifest.ID != "" {
		t.Fatalf("partial pair appeared: %q, %v", manifest.ID, err)
	}
	if !exactPairEntries(root) || !manager.Status().Writable {
		t.Fatal("staged data leaked or the healthy source lost its write authority")
	}
}

func backupPairStores(t *testing.T) (*Store, *Store) {
	t.Helper()
	managerOpts := options(t)
	managerOpts.Kind = repository.Manager
	managerOpts.Path = filepath.Join(filepath.Dir(managerOpts.Path), "manager.db")
	managerOpts.SoftwareVersion = "test-manager-version"
	webOpts := options(t)
	webOpts.SoftwareVersion = "test-web-version"
	return initialized(t, managerOpts), initialized(t, webOpts)
}

func privateBackupPairRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeBackupPairFixture(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func expectInvalidBackupPair(t *testing.T, root, id string) {
	t.Helper()
	if manifest, err := VerifyBackupPair(t.Context(), root, id); err == nil || manifest.ID != "" {
		t.Fatalf("invalid pair accepted: %q, %v", manifest.ID, err)
	}
}

func assertBackupPairValue(t *testing.T, directory string, kind repository.Kind, query, expected string) {
	t.Helper()
	db, err := connect(filepath.Join(directory, string(kind), pairDatabaseFile), true, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var actual string
	if err := db.QueryRowContext(t.Context(), query).Scan(&actual); err != nil || actual != expected {
		t.Fatalf("%s snapshot value = %q, want %q: %v", kind, actual, expected, err)
	}
}
