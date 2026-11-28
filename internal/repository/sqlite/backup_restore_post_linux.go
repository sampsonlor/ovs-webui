package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"golang.org/x/sys/unix"
)

// RestoreCopyObservation can prove properties of an isolated closed image.
// It does not prove which owner prepared it or whether a TLS key is available.
type RestoreCopyObservation struct {
	DatabaseID, SHA256     string
	Schema                 int
	APIEpoch, AuthEpoch    string
	SecurityReset          bool
	ReconciliationRequired bool
	RestoreAuditCount      int64
	UnsettledNative        bool
	TLSStateSHA256         string
}

// InspectRestoreCopy is for the root offline transfer coordinator and only
// reads a verified, sidecar-free working copy under its owner process guard.
func InspectRestoreCopy(ctx context.Context, path string, kind repository.Kind, owner uint32) (RestoreCopyObservation, error) {
	if filepath.Base(path) != string(kind)+".db" {
		return RestoreCopyObservation{}, repository.ErrUnavailable
	}
	return inspectRestoreImage(ctx, path, kind, owner)
}

func inspectRestoreImage(ctx context.Context, path string, kind repository.Kind, owner uint32) (RestoreCopyObservation, error) {
	var out RestoreCopyObservation
	if os.Geteuid() != 0 || kind != repository.Manager && kind != repository.Web ||
		!filepath.IsAbs(path) || filepath.Clean(path) != path ||
		secureDirectory(filepath.Dir(path), owner) != nil || !restoreCopyClosed(path) {
		return out, repository.ErrUnavailable
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return out, repository.ErrUnavailable
	}
	file := os.NewFile(uintptr(fd), "offline-restore-copy")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !safeFile(info, owner) || info.Size() <= 0 || info.Size() > MaxBackupPairDatabaseBytes {
		return out, repository.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hash := sha256.New()
	if _, err := io.Copy(hash, backupReader{ctx: ctx, source: file}); err != nil {
		return out, pairUnavailable(ctx)
	}
	out.SHA256 = hex.EncodeToString(hash.Sum(nil))
	db, closeImage, err := openPinnedRestoreImage(file, info)
	if err != nil {
		return RestoreCopyObservation{}, repository.ErrUnavailable
	}
	defer closeImage()
	plan := migrations.For(kind)
	out.Schema, err = verifySchema(ctx, db, kind, plan)
	if err != nil || out.Schema != len(plan) || integrity(ctx, db) != nil ||
		db.QueryRowContext(ctx, "SELECT database_id FROM database_meta WHERE singleton=1").Scan(&out.DatabaseID) != nil ||
		db.QueryRowContext(ctx, "SELECT epoch FROM api_authority WHERE singleton=1").Scan(&out.APIEpoch) != nil {
		return RestoreCopyObservation{}, pairUnavailable(ctx)
	}
	if kind == repository.Manager {
		err = readManagerRestoreCopy(ctx, db, &out)
	} else {
		err = readWebRestoreCopy(ctx, db, &out)
	}
	after, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !safeFile(after, owner) || !os.SameFile(info, after) || info.Size() != after.Size() ||
		info.Sys().(*syscall.Stat_t).Ctim != after.Sys().(*syscall.Stat_t).Ctim || !restoreCopyClosed(path) || ctx.Err() != nil {
		return RestoreCopyObservation{}, pairUnavailable(ctx)
	}
	return out, nil
}

func restoreCopyClosed(path string) bool {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func readManagerRestoreCopy(ctx context.Context, db *sql.DB, out *RestoreCopyObservation) error {
	return db.QueryRowContext(ctx, `
SELECT (SELECT epoch FROM auth_state WHERE singleton=1),
       NOT EXISTS(SELECT 1 FROM grants WHERE revoked_at IS NULL)
         AND NOT EXISTS(SELECT 1 FROM api_tokens WHERE revoked_at IS NULL)
         AND NOT EXISTS(SELECT 1 FROM auth_grants WHERE elevated_until <> 0)
         AND EXISTS(SELECT 1 FROM tls_authority WHERE singleton=1 AND trial_id=''),
       EXISTS(SELECT 1 FROM inventory_state WHERE singleton=1 AND state='reconciliation-required'
              AND reason='management-restore' AND restore_required=1 AND pending_digest=''),
       (SELECT count(*) FROM auth_audit a JOIN evidence_records e ON e.id=a.id
        WHERE e.collection='audit' AND e.operation='restore-security' AND a.result='completed'),
       EXISTS(SELECT 1 FROM safe_applies WHERE state NOT IN ('confirmed','rolled-back','not-committed'))
         OR EXISTS(SELECT 1 FROM field_executions f WHERE f.state NOT IN ('succeeded','failed')
             AND NOT EXISTS(SELECT 1 FROM safe_applies s WHERE s.id=f.id AND s.state IN ('confirmed','rolled-back','not-committed')))`).
		Scan(&out.AuthEpoch, &out.SecurityReset, &out.ReconciliationRequired, &out.RestoreAuditCount, &out.UnsettledNative)
}

func readWebRestoreCopy(ctx context.Context, db *sql.DB, out *RestoreCopyObservation) error {
	if err := db.QueryRowContext(ctx, `
SELECT NOT EXISTS(SELECT 1 FROM browser_sessions)
       AND NOT EXISTS(SELECT 1 FROM session_mappings)
       AND NOT EXISTS(SELECT 1 FROM handoff_outbox WHERE receipt_id IS NULL)`).Scan(&out.SecurityReset); err != nil {
		return err
	}
	var size int
	var ciphertext []byte
	err := db.QueryRowContext(ctx, `SELECT length(envelope), CASE WHEN length(envelope) <= 393216 THEN envelope ELSE X'' END
                                      FROM tls_local_state WHERE singleton=1`).Scan(&size, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || size <= 0 || size != len(ciphertext) {
		return repository.ErrUnavailable
	}
	hash := sha256.Sum256(ciphertext)
	out.TLSStateSHA256 = hex.EncodeToString(hash[:])
	return nil
}
