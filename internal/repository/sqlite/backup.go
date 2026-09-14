package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type BackupManifest struct {
	Format          int             `json:"format"`
	Kind            repository.Kind `json:"kind"`
	DatabaseID      string          `json:"database_id"`
	SchemaVersion   int             `json:"schema_version"`
	SoftwareVersion string          `json:"software_version"`
	SHA256          string          `json:"sha256"`
	CreatedAt       time.Time       `json:"created_at"`
}

// Backup publishes a manifest only after SQLite produces a consistent snapshot,
// verification succeeds and both files and their directory have been synced.
func (s *Store) Backup(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := acquire(ctx, s.writeSlot, s.writeQueue)
	if err != nil {
		return "", err
	}
	defer release()
	if !s.checked(true) {
		return "", repository.ErrUnavailable
	}
	path, err := s.backup(ctx, s.Status().SchemaVersion)
	if err != nil {
		s.degrade("STORAGE_BACKUP_FAILED", true)
		return "", repository.ErrUnavailable
	}
	return path, nil
}
func (s *Store) backup(ctx context.Context, version int) (string, error) {
	directory := filepath.Join(filepath.Dir(s.options.Path), "backup-"+repository.NewID())
	if err := os.Mkdir(directory, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, "database.sqlite")
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	// VACUUM INTO understands the WAL; copying the main file would lose commits.
	if _, err = s.writer.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return "", err
	}
	check, err := connect(destination, true, 1)
	if err != nil {
		return "", err
	}
	if err = integrity(ctx, check); err != nil {
		_ = check.Close()
		return "", err
	}
	var identity string
	err = check.QueryRowContext(ctx, "SELECT database_id FROM database_meta WHERE singleton=1").Scan(&identity)
	_ = check.Close()
	if err != nil {
		return "", err
	}
	f, err = os.OpenFile(destination, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return "", err
	}
	_ = f.Close()
	hash, err := fileHash(destination)
	if err != nil {
		return "", err
	}
	manifest := BackupManifest{Format: 1, Kind: s.options.Kind, DatabaseID: identity, SchemaVersion: version, SoftwareVersion: s.options.SoftwareVersion, SHA256: hash, CreatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	f, err = os.OpenFile(filepath.Join(directory, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = syncDirectory(directory); err != nil {
		return "", err
	}
	if err = syncDirectory(filepath.Dir(directory)); err != nil {
		return "", err
	}
	return directory, nil
}
func fileHash(path string) (string, error) {
	if err := checkPrivateFile(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Verification is an offline prerequisite for the later recovery workflow. It
// never restores over a live database or reactivates backed-up grants/sessions.
func VerifyBackup(ctx context.Context, directory string, kind repository.Kind) (BackupManifest, error) {
	var manifest BackupManifest
	fail := func() (BackupManifest, error) { return BackupManifest{}, repository.ErrUnavailable }
	path := filepath.Join(directory, "manifest.json")
	if err := checkPrivateFile(path); err != nil {
		return fail()
	}
	f, err := os.Open(path)
	if err != nil {
		return fail()
	}
	decoder := json.NewDecoder(io.LimitReader(f, 8193))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&manifest)
	if err == nil {
		var tail any
		if decoder.Decode(&tail) != io.EOF {
			err = errors.New("invalid manifest")
		}
	}
	_ = f.Close()
	if err != nil || manifest.Format != 1 || manifest.Kind != kind || manifest.SchemaVersion < 1 || manifest.SchemaVersion > len(migrations.For(kind)) {
		return fail()
	}
	path = filepath.Join(directory, "database.sqlite")
	hash, err := fileHash(path)
	if err != nil || hash != manifest.SHA256 {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := connect(path, true, 1)
	if err != nil {
		return fail()
	}
	defer db.Close()
	if integrity(ctx, db) != nil {
		return fail()
	}
	version, err := verifySchema(ctx, db, kind, migrations.For(kind))
	if err != nil || version != manifest.SchemaVersion {
		return fail()
	}
	var id string
	if db.QueryRowContext(ctx, "SELECT database_id FROM database_meta WHERE singleton=1").Scan(&id) != nil || id != manifest.DatabaseID {
		return fail()
	}
	return manifest, nil
}
