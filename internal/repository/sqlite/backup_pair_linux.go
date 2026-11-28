package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"golang.org/x/sys/unix"
)

const (
	backupPairPrefix  = "backup-pair-"
	backupPairTimeout = 90 * time.Second
	pairManifestFile  = "manifest.json"
	pairDatabaseFile  = "database.sqlite"
)

// PublishBackupPair accepts existing repository handles and a trusted private
// output root. Each snapshot gets its ordinary writer admission independently.
// A post-rename sync failure returns the pair ID with STORAGE_COMMIT_UNKNOWN.
func PublishBackupPair(ctx context.Context, outputRoot string, manager, web *Store) (BackupPairManifest, error) {
	if manager == nil || web == nil || manager.Kind() != repository.Manager || web.Kind() != repository.Web {
		return BackupPairManifest{}, repository.ErrInvalid
	}
	return publishBackupPair(ctx, outputRoot, func(ctx context.Context, directory string, kind repository.Kind) error {
		if kind == repository.Manager {
			return manager.capturePairSnapshot(ctx, directory)
		}
		return web.capturePairSnapshot(ctx, directory)
	})
}

func publishBackupPair(ctx context.Context, outputRoot string, snapshot func(context.Context, string, repository.Kind) error) (BackupPairManifest, error) {
	if !privatePairDirectory(outputRoot) {
		return BackupPairManifest{}, repository.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, backupPairTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return BackupPairManifest{}, repository.ErrCanceled
	}
	id := repository.NewID()
	stage := filepath.Join(outputRoot, ".staging-"+backupPairPrefix+id)
	if err := os.Mkdir(stage, 0700); err != nil {
		return BackupPairManifest{}, repository.ErrUnavailable
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	manifest := BackupPairManifest{Format: 1, ID: id, Consistency: BackupPairConsistency}
	for _, part := range []struct {
		kind     repository.Kind
		snapshot *BackupPairSnapshot
	}{{repository.Manager, &manifest.Manager}, {repository.Web, &manifest.Web}} {
		directory := filepath.Join(stage, string(part.kind))
		if err := snapshot(ctx, directory, part.kind); err != nil {
			return BackupPairManifest{}, err
		}
		verified, err := verifyPairSnapshot(ctx, directory, part.kind)
		if err != nil {
			return BackupPairManifest{}, err
		}
		*part.snapshot = verified
	}
	manifest.CreatedAt = time.Now().UTC()
	payload, err := boundedPairJSON(manifest)
	if err != nil {
		return BackupPairManifest{}, err
	}
	if _, err := decodeBackupPairManifest(payload, id); err != nil {
		return BackupPairManifest{}, err
	}
	if err := writePairManifest(stage, payload); err != nil {
		return BackupPairManifest{}, err
	}
	if ctx.Err() != nil {
		return BackupPairManifest{}, repository.ErrCanceled
	}
	destination := filepath.Join(outputRoot, backupPairPrefix+id)
	if err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return BackupPairManifest{}, repository.ErrConflict
		}
		return BackupPairManifest{}, repository.ErrUnavailable
	}
	published = true
	if err := syncDirectory(outputRoot); err != nil {
		return manifest, repository.ErrCommitUnknown
	}
	return manifest, nil
}

func (s *Store) capturePairSnapshot(ctx context.Context, directory string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	if !s.checked(true) {
		return repository.ErrUnavailable
	}
	// The admitted live Store is the writer authority. Check its logical size,
	// including committed WAL pages, before VACUUM can allocate a destination.
	var pages, pageSize int64
	if err := s.writer.QueryRowContext(ctx, "SELECT page_count, page_size FROM pragma_page_count(), pragma_page_size()").Scan(&pages, &pageSize); err != nil {
		return pairUnavailable(ctx)
	}
	if pages <= 0 || pageSize <= 0 {
		return repository.ErrUnavailable
	}
	if pages > MaxBackupPairDatabaseBytes/pageSize {
		return ErrBackupPairTooLarge
	}
	if _, err := s.backupAt(ctx, directory, s.Status().SchemaVersion); err != nil {
		// An unavailable backup destination is scoped to the requested pair.
		return pairUnavailable(ctx)
	}
	return nil
}

func writePairManifest(directory string, payload []byte) error {
	file, err := os.OpenFile(filepath.Join(directory, pairManifestFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return repository.ErrUnavailable
	}
	if _, err = file.Write(payload); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil || syncDirectory(directory) != nil {
		return repository.ErrUnavailable
	}
	return nil
}

// VerifyBackupPair reads the exact generated pair under a configured private
// root. It exposes no install, restore, or arbitrary path/filename operation.
func VerifyBackupPair(ctx context.Context, outputRoot, id string) (BackupPairManifest, error) {
	if !backupUUID.MatchString(id) {
		return BackupPairManifest{}, repository.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if ctx.Err() != nil {
		return BackupPairManifest{}, repository.ErrCanceled
	}
	directory := filepath.Join(outputRoot, backupPairPrefix+id)
	if !privatePairDirectory(outputRoot) || !exactPairEntries(directory, pairManifestFile, "manager", "web") {
		return BackupPairManifest{}, repository.ErrUnavailable
	}
	data, err := readPairManifest(filepath.Join(directory, pairManifestFile))
	if err != nil {
		return BackupPairManifest{}, err
	}
	manifest, err := decodeBackupPairManifest(data, id)
	if err != nil {
		return BackupPairManifest{}, err
	}
	for _, expected := range []BackupPairSnapshot{manifest.Manager, manifest.Web} {
		kind := expected.BackupManifest.Kind
		snapshot, err := verifyPairSnapshot(ctx, filepath.Join(directory, string(kind)), kind)
		if err != nil {
			return BackupPairManifest{}, err
		}
		if snapshot != expected {
			return BackupPairManifest{}, repository.ErrUnavailable
		}
	}
	if ctx.Err() != nil {
		return BackupPairManifest{}, repository.ErrCanceled
	}
	return manifest, nil
}

func verifyPairSnapshot(ctx context.Context, directory string, kind repository.Kind) (BackupPairSnapshot, error) {
	if !exactPairEntries(directory, pairManifestFile, pairDatabaseFile) {
		return BackupPairSnapshot{}, repository.ErrUnavailable
	}
	path := filepath.Join(directory, pairDatabaseFile)
	info, err := os.Lstat(path)
	if err != nil || !safeFile(info, uint32(os.Geteuid())) || info.Size() <= 0 {
		return BackupPairSnapshot{}, repository.ErrUnavailable
	}
	if info.Size() > MaxBackupPairDatabaseBytes {
		return BackupPairSnapshot{}, ErrBackupPairTooLarge
	}
	data, err := readPairManifest(filepath.Join(directory, pairManifestFile))
	if err != nil {
		return BackupPairSnapshot{}, err
	}
	manifest, err := VerifyBackup(ctx, directory, kind)
	if err != nil || !exactPairJSON(data, manifest) || !exactPairEntries(directory, pairManifestFile, pairDatabaseFile) {
		return BackupPairSnapshot{}, pairUnavailable(ctx)
	}
	after, err := os.Lstat(path)
	if err != nil || !safeFile(after, uint32(os.Geteuid())) || !os.SameFile(info, after) ||
		info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return BackupPairSnapshot{}, repository.ErrUnavailable
	}
	digest := sha256.Sum256(data)
	return BackupPairSnapshot{
		BackupManifest: manifest,
		DatabaseBytes:  info.Size(), ManifestSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func readPairManifest(path string) ([]byte, error) {
	if checkPrivateFile(path) != nil {
		return nil, repository.ErrUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxBackupPairManifestBytes+1))
	if err != nil || len(data) > MaxBackupPairManifestBytes {
		return nil, repository.ErrUnavailable
	}
	return data, nil
}

func privatePairDirectory(directory string) bool {
	return filepath.IsAbs(directory) && filepath.Clean(directory) == directory &&
		secureDirectory(directory, uint32(os.Geteuid())) == nil
}

func exactPairEntries(directory string, want ...string) bool {
	if !privatePairDirectory(directory) {
		return false
	}
	f, err := os.Open(directory)
	if err != nil {
		return false
	}
	defer f.Close()
	actual, err := f.Readdirnames(len(want) + 1)
	if (err != nil && err != io.EOF) || len(actual) != len(want) {
		return false
	}
	slices.Sort(actual)
	slices.Sort(want)
	return slices.Equal(actual, want)
}

func pairUnavailable(ctx context.Context) error {
	if ctx.Err() != nil {
		return repository.ErrCanceled
	}
	return repository.ErrUnavailable
}
