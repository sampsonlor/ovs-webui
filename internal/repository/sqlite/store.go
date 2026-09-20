// Package sqlite is the only driver adapter. SQL callbacks stay inside the
// repository tree; callers above that boundary use typed repository methods.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	driver "modernc.org/sqlite"
)

const ReadTimeout = 2 * time.Second
const WriteTimeout = 5 * time.Second
const ordinaryReaders = 3 // The fourth bounded reader is reserved for recovery.

type Options struct {
	Path            string
	Kind            repository.Kind
	SoftwareVersion string
}
type Store struct {
	options                                                 Options
	guard                                                   *fileGuard
	writer, readers                                         *sql.DB
	recoveryReaders                                         *sql.DB
	mu                                                      sync.RWMutex
	status                                                  repository.Status
	writeSlot, writeQueue, readSlots, readQueue             chan struct{}
	recoveryReadSlot, recoveryReadQueue, recoveryWriteQueue chan struct{}
	recoveryWriters                                         atomic.Int32
	commit                                                  func(*sql.Tx) error
}

func newStore(o Options) *Store {
	return &Store{options: o, status: repository.Status{State: "degraded", Code: "STORAGE_UNAVAILABLE"}, writeSlot: make(chan struct{}, 1), writeQueue: make(chan struct{}, 16), readSlots: make(chan struct{}, ordinaryReaders), readQueue: make(chan struct{}, 16), recoveryReadSlot: make(chan struct{}, 1), recoveryReadQueue: make(chan struct{}, 64), recoveryWriteQueue: make(chan struct{}, 64), commit: func(tx *sql.Tx) error { return tx.Commit() }}
}
func (s *Store) Kind() repository.Kind     { return s.options.Kind }
func (s *Store) Status() repository.Status { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }
func (s *Store) degrade(code string, readable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = "degraded"
	s.status.Code = code
	s.status.Writable = false
	s.status.Readable = readable
}
func (s *Store) Close() error {
	s.degrade("STORAGE_CLOSED", false)
	if s.readers != nil {
		_ = s.readers.Close()
	}
	if s.recoveryReaders != nil {
		_ = s.recoveryReaders.Close()
	}
	if s.writer != nil {
		_ = s.writer.Close()
	}
	s.guard.close()
	return nil
}
func dsn(path string, readOnly bool) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{}
	q.Set("mode", "rw")
	if readOnly {
		q.Set("mode", "ro")
		q.Set("_query_only", "true")
	}
	q.Set("_busy_timeout", "2000")
	q.Set("_foreign_keys", "true")
	q.Set("_synchronous", "FULL")
	q.Set("_defensive", "true")
	q.Add("_pragma", "trusted_schema(OFF)")
	q.Add("_pragma", "cache_size(-2048)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}
func connect(path string, ro bool, max int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, ro))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(max)
	db.SetMaxIdleConns(max)
	return db, nil
}

// Initialize is a deliberate install action. Existing or interrupted files are
// never overwritten or treated as a fresh installation by Open.
func Initialize(ctx context.Context, o Options) error {
	if !validOptions(o) {
		return repository.ErrInvalid
	}
	g, err := acquireGuard(o.Path)
	if err != nil {
		return err
	}
	defer g.close()
	f, err := os.OpenFile(o.Path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return repository.ErrConflict
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return repository.ErrUnavailable
	}
	_ = f.Close()
	if err = g.check(); err != nil {
		return err
	}
	db, err := connect(o.Path, false, 1)
	if err != nil {
		return repository.ErrUnavailable
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = configureWriter(ctx, db); err != nil {
		return repository.ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return repository.ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE database_meta(singleton INTEGER PRIMARY KEY CHECK(singleton=1),kind TEXT NOT NULL,database_id TEXT NOT NULL,software_version TEXT NOT NULL) STRICT; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,software_version TEXT NOT NULL,applied_at INTEGER NOT NULL) STRICT;`); err != nil {
		return repository.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO database_meta VALUES(1,?,?,?)`, o.Kind, repository.NewID(), o.SoftwareVersion); err != nil {
		return repository.ErrUnavailable
	}
	if err = apply(ctx, tx, migrations.For(o.Kind), o.SoftwareVersion); err != nil {
		return repository.ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return repository.ErrCommitUnknown
	}
	if err = syncDirectory(filepath.Dir(o.Path)); err != nil {
		return repository.ErrCommitUnknown
	}
	return nil
}
func validOptions(o Options) bool {
	return (o.Kind == repository.Web || o.Kind == repository.Manager) && repository.ValidID(o.SoftwareVersion)
}
func configureWriter(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return errors.New("WAL required")
	}
	_, err := db.ExecContext(ctx, "PRAGMA wal_autocheckpoint=1000")
	return err
}

func Open(ctx context.Context, o Options) (*Store, error) {
	return openWithMigrations(ctx, o, migrations.For(o.Kind))
}
func openWithMigrations(ctx context.Context, o Options, plan []migrations.Migration) (s *Store, err error) {
	s = newStore(o)
	fail := func(code string) (*Store, error) { s.degrade(code, false); return s, errors.New(code) }
	if !validOptions(o) {
		return fail("STORAGE_INVALID_OPTIONS")
	}
	s.guard, err = acquireGuard(o.Path)
	if err != nil {
		return fail(err.Error())
	}
	if err = s.guard.check(); err != nil {
		return fail(err.Error())
	}
	probe, err := connect(o.Path, true, 1)
	if err != nil {
		return fail("STORAGE_OPEN_FAILED")
	}
	defer probe.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = integrity(ctx, probe); err != nil {
		return fail("STORAGE_INTEGRITY_FAILED")
	}
	current, err := verifySchema(ctx, probe, o.Kind, plan)
	if err != nil {
		return fail("STORAGE_SCHEMA_MISMATCH")
	}
	s.writer, err = connect(o.Path, false, 1)
	if err != nil {
		return fail("STORAGE_OPEN_FAILED")
	}
	if err = configureWriter(ctx, s.writer); err != nil {
		return fail("STORAGE_WAL_UNAVAILABLE")
	}
	if current < len(plan) {
		if _, err = s.backup(ctx, current); err != nil {
			return fail("STORAGE_BACKUP_REQUIRED")
		}
		tx, e := s.writer.BeginTx(ctx, nil)
		if e != nil {
			return fail("STORAGE_MIGRATION_FAILED")
		}
		e = apply(ctx, tx, plan[current:], o.SoftwareVersion)
		if e == nil {
			_, e = tx.ExecContext(ctx, "UPDATE database_meta SET software_version=? WHERE singleton=1", o.SoftwareVersion)
		}
		if e != nil {
			_ = tx.Rollback()
			return fail("STORAGE_MIGRATION_FAILED")
		}
		if e = tx.Commit(); e != nil {
			return fail("STORAGE_MIGRATION_UNKNOWN")
		}
	}
	s.readers, err = connect(o.Path, true, ordinaryReaders)
	if err != nil {
		return fail("STORAGE_READERS_UNAVAILABLE")
	}
	if err = integrity(ctx, s.readers); err != nil {
		return fail("STORAGE_INTEGRITY_FAILED")
	}
	s.recoveryReaders, err = connect(o.Path, true, 1)
	if err != nil {
		return fail("STORAGE_RECOVERY_READER_UNAVAILABLE")
	}
	s.mu.Lock()
	s.status = repository.Status{State: "ready", Readable: true, Writable: true, SchemaVersion: len(plan)}
	s.mu.Unlock()
	return s, nil
}
func integrity(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil || result != "ok" {
		return repository.ErrUnavailable
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return repository.ErrUnavailable
	}
	return rows.Err()
}
func verifySchema(ctx context.Context, db *sql.DB, kind repository.Kind, plan []migrations.Migration) (int, error) {
	var actual repository.Kind
	var identity, software string
	if err := db.QueryRowContext(ctx, "SELECT kind,database_id,software_version FROM database_meta WHERE singleton=1").Scan(&actual, &identity, &software); err != nil || actual != kind || !repository.ValidID(identity) || !repository.ValidID(software) {
		return 0, repository.ErrUnavailable
	}
	rows, err := db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	current := 0
	for rows.Next() {
		var version int
		var hash string
		if err := rows.Scan(&version, &hash); err != nil {
			return 0, err
		}
		if current >= len(plan) || version != current+1 || version != plan[current].Version || hash != plan[current].Checksum {
			return 0, repository.ErrUnavailable
		}
		current++
	}
	if current == 0 {
		return 0, repository.ErrUnavailable
	}
	return current, rows.Err()
}
func apply(ctx context.Context, tx *sql.Tx, plan []migrations.Migration, software string) error {
	for _, m := range plan {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(?,?,?,?)", m.Version, m.Checksum, software, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}
func acquire(ctx context.Context, active, waiting chan struct{}) (func(), error) {
	if ctx.Err() != nil {
		return nil, repository.ErrCanceled
	}
	select {
	case active <- struct{}{}:
		return func() { <-active }, nil
	default:
	}
	select {
	case waiting <- struct{}{}:
		defer func() { <-waiting }()
	default:
		return nil, repository.ErrBusy
	}
	select {
	case active <- struct{}{}:
		if ctx.Err() != nil {
			<-active
			return nil, repository.ErrCanceled
		}
		return func() { <-active }, nil
	case <-ctx.Done():
		return nil, repository.ErrCanceled
	}
}
func (s *Store) checked(writable bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.status.Readable || (writable && !s.status.Writable) {
		return false
	}
	if err := s.guard.check(); err != nil {
		s.status = repository.Status{State: "degraded", Code: err.Error()}
		return false
	}
	return true
}
func (s *Store) Read(ctx context.Context, fn func(context.Context, *sql.Conn) error) error {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	slots, queue, pool := s.readSlots, s.readQueue, s.readers
	if isRecovery(ctx) {
		slots, queue, pool = s.recoveryReadSlot, s.recoveryReadQueue, s.recoveryReaders
	}
	release, err := acquire(ctx, slots, queue)
	if err != nil {
		return err
	}
	defer release()
	if !s.checked(false) {
		return repository.ErrUnavailable
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return s.failure(err)
	}
	defer conn.Close()
	return s.failure(fn(ctx, conn))
}

// Callbacks contain SQL only. Commit is the durable acceptance boundary.
func (s *Store) Write(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, WriteTimeout)
	defer cancel()
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	if !s.checked(true) {
		return repository.ErrUnavailable
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return s.failure(err)
	}
	defer tx.Rollback()
	if err = fn(ctx, tx); err != nil {
		return s.failure(err)
	}
	if err = ctx.Err(); err != nil {
		return s.failure(err)
	}
	if !s.checked(true) {
		return repository.ErrUnavailable
	}
	if err = s.commit(tx); err != nil {
		s.degrade("STORAGE_COMMIT_UNKNOWN", true)
		// A failed COMMIT can leave a driver transaction open. Retire its
		// connection rather than retaining locks or admitting another write.
		_ = s.writer.Close()
		return repository.ErrCommitUnknown
	}
	return nil
}
func (s *Store) failure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return repository.ErrNotFound
	}
	if errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrInvalid) {
		return err
	}
	var rejection repository.Rejection
	if errors.As(err, &rejection) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return repository.ErrCanceled
	}
	var failure *driver.Error
	if errors.As(err, &failure) {
		switch failure.Code() & 255 {
		case 5, 6:
			return repository.ErrBusy
		case 9:
			return repository.ErrCanceled
		case 19:
			return repository.ErrConflict
		case 13:
			s.degrade("STORAGE_FULL", true)
		case 10:
			s.degrade("STORAGE_IO_FAILED", false)
		case 11, 26:
			s.degrade("STORAGE_INTEGRITY_FAILED", false)
		default:
			s.degrade("STORAGE_DATABASE_FAILED", false)
		}
	} else {
		s.degrade("STORAGE_OPERATION_FAILED", false)
	}
	return repository.ErrUnavailable
}
func (s *Store) Checkpoint(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, WriteTimeout)
	defer cancel()
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	if !s.checked(true) {
		return repository.ErrUnavailable
	}
	var busy, log, done int
	err = s.writer.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &done)
	if err != nil {
		return s.failure(err)
	}
	if busy != 0 || done < log {
		s.mu.Lock()
		s.status.Warning = "STORAGE_CHECKPOINT_PENDING"
		s.mu.Unlock()
		return repository.ErrBusy
	}
	s.mu.Lock()
	s.status.Warning = ""
	s.mu.Unlock()
	return nil
}

// SQL source strings are embedded and reviewed; no request may supply pragmas,
// migrations, connection URIs, filenames or callbacks through the IPC surface.
func (s *Store) Probe(ctx context.Context) repository.Status {
	status := s.Status()
	if !status.Readable {
		return status
	}
	err := s.Read(ctx, func(ctx context.Context, c *sql.Conn) error {
		var v int
		return c.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&v)
	})
	status = s.Status()
	if err != nil && status.Writable {
		// A saturated or canceled probe cannot claim readiness. This response
		// does not permanently latch a transient timeout as database damage.
		status.State, status.Code = "degraded", err.Error()
		status.Readable, status.Writable = false, false
	}
	return status
}
func (s *Store) Maintain(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.Status().Writable {
				_ = s.Checkpoint(ctx)
			}
		}
	}
}
