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
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"golang.org/x/sys/unix"
)

const restoreStagePrefix = "restore-stage-"

// RestoreStageManifest describes a sealed copy prepared for the separate
// offline reconciliation workflow. It never represents a usable live store.
type RestoreStageManifest struct {
	Format            int                 `json:"format"`
	ID                string              `json:"id"`
	CreatedAt         time.Time           `json:"created_at"`
	State             string              `json:"state"`
	BackupPair        BackupPairManifest  `json:"backup_pair"`
	ManagerSecurity   string              `json:"manager_security"`
	WebSecurity       string              `json:"web_security"`
	OVSReconciliation string              `json:"ovs_reconciliation"`
	KeyEvidenceScope  string              `json:"key_evidence_scope"`
	Keys              []BackupRestoreKeys `json:"keys"`
}

const (
	restoreStageSealed          = "sealed"
	restoreStageManagerSecurity = "owner-prepare-required"
	restoreStageWebSecurity     = "owner-handoff-and-prepare-required"
	restoreStageOVS             = "required"
	restoreStageKeyScope        = "secret-record-version-metadata-only"
)

// StageRestorePair copies an exact previously verified backup pair under a
// separate operator-configured root-owned 0700 staging root. It creates only
// generated children. Both sealed slots stay root-owned until offline owner
// preparation. Neither a live path nor a key enters this operation.
// ErrCommitUnknown returns a stage ID that can be checked by verification.
func StageRestorePair(ctx context.Context, backupRoot, backupID, stagingRoot string) (RestoreStageManifest, error) {
	if os.Geteuid() != 0 {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	return stageRestorePair(ctx, backupRoot, backupID, stagingRoot)
}

func stageRestorePair(ctx context.Context, backupRoot, backupID, stagingRoot string) (RestoreStageManifest, error) {
	ctx, cancel := context.WithTimeout(ctx, backupPairTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return RestoreStageManifest{}, repository.ErrCanceled
	}
	if !backupUUID.MatchString(backupID) || rootsOverlap(backupRoot, stagingRoot) {
		return RestoreStageManifest{}, repository.ErrInvalid
	}
	root, err := openRestoreStageRoot(stagingRoot)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	defer root.Close()
	pair, err := VerifyBackupPair(ctx, backupRoot, backupID)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	sourceInfo, sourceErr := os.Lstat(backupRoot)
	rootInfo, rootErr := root.Stat()
	if sourceErr != nil || rootErr != nil || os.SameFile(sourceInfo, rootInfo) {
		return RestoreStageManifest{}, repository.ErrInvalid
	}
	id := repository.NewID()
	finalName := restoreStagePrefix + id
	tempName := ".staging-" + finalName
	tempPath := filepath.Join(stagingRoot, tempName)
	rootFD := int(root.Fd())
	if err := unix.Mkdirat(rootFD, tempName, 0700); err != nil {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tempPath)
		}
	}()
	source := filepath.Join(backupRoot, backupPairPrefix+backupID)
	for _, expected := range []BackupPairSnapshot{pair.Manager, pair.Web} {
		kind := string(expected.BackupManifest.Kind)
		if err := copyRestoreStageSlot(ctx, filepath.Join(source, kind), filepath.Join(tempPath, kind), expected); err != nil {
			return RestoreStageManifest{}, err
		}
	}
	keys, err := restoreStageKeys(ctx, tempPath, pair)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	manifest := RestoreStageManifest{
		Format: 1, ID: id, CreatedAt: time.Now().UTC(), State: restoreStageSealed, BackupPair: pair,
		ManagerSecurity: restoreStageManagerSecurity, WebSecurity: restoreStageWebSecurity,
		OVSReconciliation: restoreStageOVS, KeyEvidenceScope: restoreStageKeyScope, Keys: keys,
	}
	payload, err := boundedPairJSON(manifest)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	if err := writePairManifest(tempPath, payload); err != nil {
		return RestoreStageManifest{}, err
	}
	if ctx.Err() != nil {
		return RestoreStageManifest{}, repository.ErrCanceled
	}
	if !sameRestoreStageRoot(stagingRoot, root) || !exactRestoreStageBody(tempPath) {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	if err := unix.Renameat2(rootFD, tempName, rootFD, finalName, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return RestoreStageManifest{}, repository.ErrConflict
		}
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	published = true
	if err := root.Sync(); err != nil || !sameRestoreStageRoot(stagingRoot, root) {
		return manifest, repository.ErrCommitUnknown
	}
	return manifest, nil
}

// VerifyStagedRestorePair checks the sealed stage independently of the source
// backup and recomputes evidence from both complete, still sealed copies.
func VerifyStagedRestorePair(ctx context.Context, stagingRoot, stageID string) (RestoreStageManifest, error) {
	if os.Geteuid() != 0 {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	return verifyStagedRestorePair(ctx, stagingRoot, stageID)
}

func verifyStagedRestorePair(ctx context.Context, stagingRoot, stageID string) (RestoreStageManifest, error) {
	return verifyStagedRestore(ctx, stagingRoot, stageID, nil)
}

type StagedRestoreInspection struct {
	Manifest     RestoreStageManifest
	Manager, Web RestoreCopyObservation
}

// InspectStagedRestorePair verifies the sealed manifest and reads its two
// original owner states without handing off ownership or changing the images.
func InspectStagedRestorePair(ctx context.Context, stagingRoot, stageID string) (StagedRestoreInspection, error) {
	var out StagedRestoreInspection
	if os.Geteuid() != 0 {
		return out, repository.ErrUnavailable
	}
	manifest, err := verifyStagedRestore(ctx, stagingRoot, stageID, &out)
	if err != nil {
		return StagedRestoreInspection{}, err
	}
	out.Manifest = manifest
	return out, nil
}

func verifyStagedRestore(ctx context.Context, stagingRoot, stageID string, copyStates *StagedRestoreInspection) (RestoreStageManifest, error) {
	if !backupUUID.MatchString(stageID) {
		return RestoreStageManifest{}, repository.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, backupPairTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return RestoreStageManifest{}, repository.ErrCanceled
	}
	root, err := openRestoreStageRoot(stagingRoot)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	defer root.Close()
	directory := filepath.Join(stagingRoot, restoreStagePrefix+stageID)
	if !exactRestoreStageBody(directory) {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	data, err := readPairManifest(filepath.Join(directory, pairManifestFile))
	if err != nil {
		return RestoreStageManifest{}, err
	}
	var manifest RestoreStageManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Format != 1 || manifest.ID != stageID || manifest.CreatedAt.IsZero() ||
		manifest.State != restoreStageSealed || manifest.ManagerSecurity != restoreStageManagerSecurity ||
		manifest.WebSecurity != restoreStageWebSecurity || manifest.OVSReconciliation != restoreStageOVS ||
		manifest.KeyEvidenceScope != restoreStageKeyScope {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	pair := manifest.BackupPair
	pairPayload, err := boundedPairJSON(pair)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	if _, err := decodeBackupPairManifest(pairPayload, pair.ID); err != nil {
		return RestoreStageManifest{}, err
	}
	for _, expected := range []BackupPairSnapshot{pair.Manager, pair.Web} {
		kind := expected.BackupManifest.Kind
		slot := filepath.Join(directory, string(kind))
		actual, err := verifyPairSnapshot(ctx, slot, kind)
		if err != nil {
			return RestoreStageManifest{}, err
		}
		if actual != expected {
			return RestoreStageManifest{}, repository.ErrUnavailable
		}
		if copyStates != nil {
			state, err := inspectRestoreImage(ctx, filepath.Join(slot, pairDatabaseFile), kind, 0)
			if err != nil {
				return RestoreStageManifest{}, err
			}
			if state.DatabaseID != expected.BackupManifest.DatabaseID || state.Schema != expected.BackupManifest.SchemaVersion ||
				state.SHA256 != expected.BackupManifest.SHA256 {
				return RestoreStageManifest{}, repository.ErrUnavailable
			}
			if kind == repository.Manager {
				copyStates.Manager = state
			} else {
				copyStates.Web = state
			}
		}
	}
	manifest.Keys, err = restoreStageKeys(ctx, directory, pair)
	if err != nil {
		return RestoreStageManifest{}, err
	}
	if !exactPairJSON(data, manifest) || !sameRestoreStageRoot(stagingRoot, root) ||
		!exactRestoreStageBody(directory) {
		return RestoreStageManifest{}, repository.ErrUnavailable
	}
	if ctx.Err() != nil {
		return RestoreStageManifest{}, repository.ErrCanceled
	}
	return manifest, nil
}

func exactRestoreStageBody(directory string) bool {
	if !exactPairEntries(directory, pairManifestFile, "manager", "web") {
		return false
	}
	for _, path := range []string{directory, filepath.Join(directory, "manager"), filepath.Join(directory, "web")} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode() != os.ModeDir|0700 || !privatePairDirectory(path) {
			return false
		}
		files := []string{pairManifestFile}
		if path != directory {
			files = append(files, pairDatabaseFile)
		}
		for _, file := range files {
			info, err := os.Lstat(filepath.Join(path, file))
			if err != nil || info.Mode() != 0600 || !safeFile(info, uint32(os.Geteuid())) {
				return false
			}
		}
		if path != directory && !exactPairEntries(path, files...) {
			return false
		}
	}
	return true
}

func openRestoreStageRoot(path string) (*os.File, error) {
	if !privatePairDirectory(path) {
		return nil, repository.ErrUnavailable
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	root := os.NewFile(uintptr(fd), "restore-stage-root")
	if !sameRestoreStageRoot(path, root) {
		_ = root.Close()
		return nil, repository.ErrUnavailable
	}
	return root, nil
}

func sameRestoreStageRoot(path string, root *os.File) bool {
	if !privatePairDirectory(path) {
		return false
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode() != os.ModeDir|0700 {
		return false
	}
	opened, err := root.Stat()
	return err == nil && os.SameFile(current, opened)
}

func rootsOverlap(first, second string) bool {
	if !filepath.IsAbs(first) || filepath.Clean(first) != first || !filepath.IsAbs(second) || filepath.Clean(second) != second {
		return true
	}
	for _, paths := range [][2]string{{first, second}, {second, first}} {
		rel, err := filepath.Rel(paths[0], paths[1])
		if err != nil || rel != ".." && !strings.HasPrefix(rel, "../") {
			return true
		}
	}
	return false
}

func copyRestoreStageSlot(ctx context.Context, from, into string, expected BackupPairSnapshot) error {
	if !exactPairEntries(from, pairManifestFile, pairDatabaseFile) {
		return repository.ErrUnavailable
	}
	fd, err := unix.Open(filepath.Join(from, pairDatabaseFile), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return repository.ErrUnavailable
	}
	source := os.NewFile(uintptr(fd), "restore-stage-source")
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !safeFile(info, uint32(os.Geteuid())) || info.Size() != expected.DatabaseBytes {
		return repository.ErrUnavailable
	}
	if err := os.Mkdir(into, 0700); err != nil {
		return repository.ErrUnavailable
	}
	image, err := os.OpenFile(filepath.Join(into, pairDatabaseFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return repository.ErrUnavailable
	}
	hash := sha256.New()
	_, err = io.CopyN(io.MultiWriter(image, hash), backupStreamReader{ctx, source}, expected.DatabaseBytes)
	if err == nil {
		err = image.Sync()
	}
	closeErr := image.Close()
	if err != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != expected.BackupManifest.SHA256 {
		return pairUnavailable(ctx)
	}
	payload, err := boundedPairJSON(expected.BackupManifest)
	if err != nil {
		return err
	}
	if err := writePairManifest(into, payload); err != nil {
		return err
	}
	actual, err := verifyPairSnapshot(ctx, into, expected.BackupManifest.Kind)
	if err != nil {
		return err
	}
	if actual != expected {
		return repository.ErrUnavailable
	}
	return nil
}

func restoreStageKeys(ctx context.Context, dir string, pair BackupPairManifest) ([]BackupRestoreKeys, error) {
	keys := make([]BackupRestoreKeys, 0, 3)
	for _, slot := range []struct {
		snapshot   BackupPairSnapshot
		partitions []string
	}{
		{pair.Manager, []string{secret.Privileged}},
		{pair.Web, []string{secret.Session, secret.TLS}},
	} {
		m := slot.snapshot.BackupManifest
		path := filepath.Join(dir, string(m.Kind), pairDatabaseFile)
		required, err := backupKeyVersions(ctx, path, m.SchemaVersion, slot.partitions)
		if err != nil {
			return nil, err
		}
		for _, partition := range slot.partitions {
			item := BackupRestoreKeys{Partition: partition, Required: []int{}, Missing: []int{}, State: "no-secret-records"}
			if needed := required[partition]; len(needed) != 0 {
				item.Required = needed
				item.State = "unknown"
			}
			keys = append(keys, item)
		}
	}
	return keys, nil
}
