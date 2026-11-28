//go:build linux

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"golang.org/x/sys/unix"
)

type VLANCheckpointRequest struct {
	Root, TransactionID, Generation string
}

// PublishVLANCheckpoint requires a closed Manager database. The exclusive
// repository guard remains held through publication, so a normal Manager
// process cannot advance the confirmed record after it was read.
func PublishVLANCheckpoint(ctx context.Context, managerDatabase string, req VLANCheckpointRequest) (VLANCheckpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validCheckpointRequest(req) || filepath.Base(managerDatabase) != "manager.db" ||
		filepath.Dir(managerDatabase) == req.Root || checkPrivateFile(managerDatabase) != nil {
		return VLANCheckpoint{}, repository.ErrInvalid
	}
	guard, err := acquireGuard(managerDatabase)
	if err != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	defer guard.close()
	if guard.check() != nil || !restoreCopyClosed(managerDatabase) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	fd, err := unix.Open(managerDatabase, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	image := os.NewFile(uintptr(fd), "offline-manager-checkpoint")
	defer image.Close()
	info, err := image.Stat()
	if err != nil || !safeFile(info, 0) || !os.SameFile(guard.identity, info) || info.Size() < 1 ||
		info.Size() > MaxBackupPairDatabaseBytes {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	db, closeImage, err := openPinnedRestoreImage(image, info)
	if err != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	defer closeImage()
	checkpoint, err := readVLANCheckpoint(ctx, db, req)
	if err != nil {
		return VLANCheckpoint{}, err
	}
	after, err := image.Stat()
	if err != nil || guard.check() != nil || !restoreCopyClosed(managerDatabase) || !os.SameFile(after, guard.identity) ||
		after.Size() != info.Size() || info.Sys().(*syscall.Stat_t).Ctim != after.Sys().(*syscall.Stat_t).Ctim {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	return publishVLANCheckpointFile(ctx, req.Root, checkpoint)
}

func readVLANCheckpoint(ctx context.Context, db *sql.DB, req VLANCheckpointRequest) (VLANCheckpoint, error) {
	plan := migrations.For(repository.Manager)
	version, err := verifySchema(ctx, db, repository.Manager, plan)
	if err != nil || version != len(plan) || integrity(ctx, db) != nil {
		return VLANCheckpoint{}, pairUnavailable(ctx)
	}
	source := vlanCheckpointSource{manifest: VLANCheckpointManifest{
		Format: checkpointFormat, EvidenceScope: checkpointScope, SourceSchema: version, TransactionID: req.TransactionID, InstanceGeneration: req.Generation,
	}}
	var confirmedAt int64
	err = db.QueryRowContext(ctx, `
		SELECT m.database_id,m.software_version,l.confirmed_at_ms,
		       json_extract(CAST(i.evidence AS TEXT),'$.root'),
		       json_extract(CAST(i.evidence AS TEXT),'$.schema'),
		       CASE WHEN length(l.document) BETWEEN 1 AND 320000 THEN l.document ELSE X'' END
		FROM last_known_good l
		JOIN safe_applies s ON s.id=l.transaction_id AND s.state='confirmed'
		JOIN transaction_journal j ON j.transaction_id=l.transaction_id AND j.state='confirmed'
		JOIN inventory_state i ON i.singleton=1 AND i.generation=l.generation AND i.state='confirmed'
		    AND i.restore_required=0 AND i.pending_digest=''
		JOIN database_meta m ON m.singleton=1 AND m.kind='manager'
		WHERE l.singleton=1 AND l.transaction_id=? AND l.generation=?`, req.TransactionID, req.Generation).
		Scan(&source.manifest.SourceDatabaseID, &source.manifest.SourceSoftware, &confirmedAt,
			&source.root, &source.schema, &source.document)
	if err != nil || confirmedAt <= 0 {
		return VLANCheckpoint{}, pairUnavailable(ctx)
	}
	source.manifest.ConfirmedAt = time.UnixMilli(confirmedAt).UTC()
	return projectVLANCheckpoint(source, func(table, nativeID string) (candidate.Binding, error) {
		out := candidate.Binding{Table: table, OVSUUID: nativeID, Generation: req.Generation}
		err := db.QueryRowContext(ctx, `
			SELECT management_id FROM identities WHERE generation=? AND table_name=? AND ovs_uuid=? AND state='active'`,
			req.Generation, table, nativeID).Scan(&out.ManagementID)
		return out, err
	})
}

func validCheckpointRequest(req VLANCheckpointRequest) bool {
	return os.Geteuid() == 0 && apitypes.ManagementID(req.TransactionID) && apitypes.ManagementID(req.Generation) &&
		privatePairDirectory(req.Root)
}

const checkpointPendingName = ".last-known-good-vlan.pending"

type checkpointFiles struct {
	lock, current, pending os.FileInfo
}

func inspectCheckpointRoot(root string) (checkpointFiles, error) {
	var files checkpointFiles
	if !privatePairDirectory(root) {
		return files, repository.ErrUnavailable
	}
	dir, err := os.Open(root)
	if err != nil {
		return files, repository.ErrUnavailable
	}
	defer dir.Close()
	names, err := dir.Readdirnames(4)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > 3 {
		return files, repository.ErrUnavailable
	}
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || !safeFile(info, 0) {
			return files, repository.ErrUnavailable
		}
		switch name {
		case checkpointFileName + ".lock":
			if info.Size() != 0 {
				return files, repository.ErrUnavailable
			}
			files.lock = info
		case checkpointFileName:
			files.current = info
		case checkpointPendingName:
			if info.Size() > maxCheckpointBytes {
				return files, repository.ErrUnavailable
			}
			files.pending = info
		default:
			return files, repository.ErrUnavailable
		}
	}
	if files.lock == nil && (files.current != nil || files.pending != nil) {
		return files, repository.ErrUnavailable
	}
	return files, nil
}

func openCheckpointRoot(root string, creating bool) (*fileGuard, checkpointFiles, error) {
	files, err := inspectCheckpointRoot(root)
	if err != nil || !creating && (files.lock == nil || files.current == nil) {
		return nil, checkpointFiles{}, repository.ErrUnavailable
	}
	guard, err := acquireGuard(filepath.Join(root, checkpointFileName))
	if err != nil {
		return nil, checkpointFiles{}, repository.ErrUnavailable
	}
	actual, err := inspectCheckpointRoot(root)
	if err != nil || !sameCheckpointFile(files.current, actual.current) ||
		!sameCheckpointFile(files.pending, actual.pending) ||
		files.lock != nil && !sameCheckpointFile(files.lock, actual.lock) ||
		!checkpointLockCurrent(guard, actual.lock) {
		guard.close()
		return nil, checkpointFiles{}, repository.ErrUnavailable
	}
	return guard, actual, nil
}

func sameCheckpointFile(expected, actual os.FileInfo) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	return os.SameFile(expected, actual)
}

func checkpointLockCurrent(guard *fileGuard, expected os.FileInfo) bool {
	pinned, err := guard.lock.Stat()
	current, statErr := os.Lstat(guard.path + ".lock")
	return err == nil && statErr == nil && safeFile(pinned, 0) && safeFile(current, 0) &&
		expected != nil && os.SameFile(expected, pinned) && os.SameFile(pinned, current)
}

// VerifyVLANCheckpoint reads the independent file without opening SQLite.
// It verifies the requested historical reference, not present OVS state.
func VerifyVLANCheckpoint(ctx context.Context, req VLANCheckpointRequest) (VLANCheckpoint, error) {
	if !validCheckpointRequest(req) {
		return VLANCheckpoint{}, repository.ErrInvalid
	}
	guard, files, err := openCheckpointRoot(req.Root, false)
	if err != nil {
		return VLANCheckpoint{}, err
	}
	defer guard.close()
	checkpoint, err := readVLANCheckpointFile(guard, files.current)
	if err != nil || !checkpointLockCurrent(guard, files.lock) || checkpoint.Manifest.TransactionID != req.TransactionID ||
		checkpoint.Manifest.InstanceGeneration != req.Generation || ctx.Err() != nil {
		return VLANCheckpoint{}, pairUnavailable(ctx)
	}
	return checkpoint, nil
}

func readVLANCheckpointFile(guard *fileGuard, expected os.FileInfo) (VLANCheckpoint, error) {
	if guard.check() != nil || !sameCheckpointFile(expected, guard.identity) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	fd, err := unix.Open(guard.path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	file := os.NewFile(uintptr(fd), "independent-vlan-checkpoint")
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !safeFile(before, 0) || !os.SameFile(guard.identity, before) ||
		before.Size() < 1 || before.Size() > maxCheckpointBytes {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCheckpointBytes+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || int64(len(data)) != before.Size() || after.Size() != before.Size() ||
		before.Sys().(*syscall.Stat_t).Ctim != after.Sys().(*syscall.Stat_t).Ctim || guard.check() != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	return decodeVLANCheckpoint(data)
}

func publishVLANCheckpointFile(ctx context.Context, root string, data VLANCheckpoint) (VLANCheckpoint, error) {
	body, err := encodeVLANCheckpoint(data)
	if err != nil {
		return VLANCheckpoint{}, err
	}
	guard, files, err := openCheckpointRoot(root, true)
	if err != nil {
		return VLANCheckpoint{}, err
	}
	defer guard.close()
	var current VLANCheckpoint
	if files.current != nil {
		current, err = readVLANCheckpointFile(guard, files.current)
		if err != nil {
			return VLANCheckpoint{}, err
		}
		if current.SHA256 != data.SHA256 && (current.Manifest.SourceDatabaseID != data.Manifest.SourceDatabaseID ||
			!data.Manifest.ConfirmedAt.After(current.Manifest.ConfirmedAt)) {
			return VLANCheckpoint{}, repository.ErrConflict
		}
	}
	if ctx.Err() != nil {
		return VLANCheckpoint{}, repository.ErrCanceled
	}
	if !checkpointLockCurrent(guard, files.lock) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	pending := filepath.Join(root, checkpointPendingName)
	if files.pending != nil {
		// Verify ignores this one safe interrupted slot. Only a publisher with
		// the unchanged lock may discard it before opening a new private file.
		old, err := os.Lstat(pending)
		if err != nil || !safeFile(old, 0) || !os.SameFile(files.pending, old) || os.Remove(pending) != nil {
			return VLANCheckpoint{}, repository.ErrUnavailable
		}
	}
	if current.SHA256 == data.SHA256 {
		return current, nil
	}
	tmp, err := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	defer os.Remove(pending)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(body)
	}
	if err == nil {
		err = tmp.Sync()
	}
	staged, statErr := tmp.Stat()
	closeErr := tmp.Close()
	reopened, pathErr := os.Lstat(pending)
	if err != nil || statErr != nil || closeErr != nil || pathErr != nil || !safeFile(staged, 0) ||
		!safeFile(reopened, 0) || !os.SameFile(staged, reopened) ||
		!checkpointLockCurrent(guard, files.lock) || !privatePairDirectory(root) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	if ctx.Err() != nil {
		return VLANCheckpoint{}, repository.ErrCanceled
	}
	if files.current == nil {
		err = unix.Renameat2(unix.AT_FDCWD, pending, unix.AT_FDCWD, guard.path, unix.RENAME_NOREPLACE)
	} else if guard.check() == nil && sameCheckpointFile(files.current, guard.identity) {
		err = os.Rename(pending, guard.path)
	} else {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	if err != nil {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	if syncDirectory(root) != nil {
		return data, repository.ErrCommitUnknown
	}
	return data, nil
}
