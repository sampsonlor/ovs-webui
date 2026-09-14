//go:build linux

package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/migrations"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

var background = context.Background()

func options(t *testing.T) Options {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return Options{Path: filepath.Join(directory, "web.db"), Kind: repository.Web, SoftwareVersion: "test-v1"}
}
func initialized(t *testing.T, o Options) *Store {
	t.Helper()
	if err := Initialize(background, o); err != nil {
		t.Fatal(err)
	}
	return opened(t, o)
}
func opened(t *testing.T, o Options) *Store {
	t.Helper()
	s, err := Open(background, o)
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func writeDocument(s *Store, id string, body []byte) error {
	return s.Write(background, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES('label','synthetic-owner',?,?)", id, body)
		return err
	})
}
func documentCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.Read(background, func(ctx context.Context, c *sql.Conn) error {
		return c.QueryRowContext(ctx, "SELECT count(*) FROM metadata").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
func mutateOffline(t *testing.T, o Options, query string) {
	t.Helper()
	db, err := connect(o.Path, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitInitializationAndNoAutomaticRecreation(t *testing.T) {
	o := options(t)
	s, err := Open(background, o)
	if err == nil || s.Status().Code != "STORAGE_MISSING" {
		t.Fatal(s.Status(), err)
	}
	_ = s.Close()
	if _, err = os.Stat(o.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("normal start created database")
	}
	s = initialized(t, o)
	if err = writeDocument(s, "kept", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	before, _ := os.ReadFile(o.Path)
	if err = Initialize(background, o); err == nil {
		t.Fatal("reinitialized existing database")
	}
	after, _ := os.ReadFile(o.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("explicit initialization overwrote state")
	}
	s = opened(t, o)
	if documentCount(t, s) != 1 {
		t.Fatal("lost durable row")
	}
}
func TestSettingsIndependentReadersAndSingleton(t *testing.T) {
	o := options(t)
	s := initialized(t, o)
	var wal string
	var syncMode, foreignKeys, busy int
	if err := s.writer.QueryRow("PRAGMA journal_mode").Scan(&wal); err != nil {
		t.Fatal(err)
	}
	_ = s.writer.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	_ = s.writer.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	_ = s.writer.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	if wal != "wal" || syncMode != 2 || foreignKeys != 1 || busy != 2000 {
		t.Fatalf("unsafe settings: %s/%d/%d/%d", wal, syncMode, foreignKeys, busy)
	}
	conns := []*sql.Conn{}
	for i := 0; i < 4; i++ {
		c, err := s.readers.Conn(background)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var q int
		if err = c.QueryRowContext(background, "PRAGMA query_only").Scan(&q); err != nil || q != 1 {
			t.Fatal("reader writable", err)
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
	other, err := Open(background, o)
	defer other.Close()
	if err == nil {
		t.Fatal("two repository owners admitted")
	}
	if s.writer.Stats().MaxOpenConnections != 1 || s.readers.Stats().MaxOpenConnections != 4 {
		t.Fatal("unbounded connection pool")
	}
}
func TestDamagedMissingAndMismatchedDatabasesFailClosed(t *testing.T) {
	for _, scenario := range []string{"corrupt", "empty", "checksum", "future", "kind", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			o := options(t)
			s := initialized(t, o)
			_ = s.Close()
			switch scenario {
			case "corrupt":
				if err := os.WriteFile(o.Path, []byte("synthetic corrupt database"), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.Truncate(o.Path, 0); err != nil {
					t.Fatal(err)
				}
			case "checksum":
				mutateOffline(t, o, "UPDATE schema_migrations SET checksum='modified' WHERE version=1")
			case "future":
				mutateOffline(t, o, "INSERT INTO schema_migrations VALUES(999,'future','future-build',0)")
			case "kind":
				o.Kind = repository.Manager
			case "missing":
				if err := os.Remove(o.Path); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(o.Path)
			failed, err := Open(background, o)
			defer failed.Close()
			if err == nil || failed.Status().Writable || failed.Status().Readable {
				t.Fatal("unsafe database opened", scenario, failed.Status())
			}
			if writeDocument(failed, "forbidden", []byte(`{}`)) != repository.ErrUnavailable {
				t.Fatal("degraded write admitted")
			}
			after, _ := os.ReadFile(o.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed startup modified evidence")
			}
			if scenario == "missing" {
				if _, err = os.Stat(o.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing DB recreated")
				}
			}
		})
	}
}
func TestPrivatePathsAndReplacementProtection(t *testing.T) {
	for _, scenario := range []string{"directory-mode", "file-mode", "symlink", "sidecar", "hardlink"} {
		t.Run(scenario, func(t *testing.T) {
			o := options(t)
			s := initialized(t, o)
			_ = s.Close()
			sentinel := filepath.Join(t.TempDir(), "preserve")
			_ = os.WriteFile(sentinel, []byte("preserve"), 0600)
			switch scenario {
			case "directory-mode":
				_ = os.Chmod(filepath.Dir(o.Path), 0750)
			case "file-mode":
				_ = os.Chmod(o.Path, 0640)
			case "symlink":
				_ = os.Rename(o.Path, o.Path+".original")
				if err := os.Symlink(sentinel, o.Path); err != nil {
					t.Fatal(err)
				}
			case "sidecar":
				if err := os.Symlink(sentinel, o.Path+"-wal"); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(o.Path, o.Path+".link"); err != nil {
					t.Fatal(err)
				}
			}
			failed, err := Open(background, o)
			defer failed.Close()
			if err == nil {
				t.Fatal("unsafe path admitted")
			}
			body, _ := os.ReadFile(sentinel)
			if string(body) != "preserve" {
				t.Fatal("unrelated file changed")
			}
		})
	}
	o := options(t)
	s := initialized(t, o)
	if err := os.Rename(o.Path, o.Path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.Path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if writeDocument(s, "refused", []byte(`{}`)) != repository.ErrUnavailable || s.Status().Code != "STORAGE_REPLACED" {
		t.Fatal("replaced database accepted")
	}
}
func migration(version int, sql string) migrations.Migration {
	sum := sha256.Sum256([]byte(sql))
	return migrations.Migration{Version: version, SQL: sql, Checksum: hex.EncodeToString(sum[:])}
}
func TestAtomicMigrationBackupAndFailurePreservePriorData(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			o := options(t)
			s := initialized(t, o)
			if err := writeDocument(s, "kept", []byte(`{"saved":true}`)); err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			sqlText := "CREATE TABLE migration_probe(id INTEGER PRIMARY KEY) STRICT;"
			if fail {
				sqlText += "INSERT INTO missing_table VALUES(1);"
			}
			plan := append(migrations.For(o.Kind), migration(3, sqlText))
			next, err := openWithMigrations(background, o, plan)
			_ = next.Close()
			if (err != nil) != fail {
				t.Fatal("migration result", err)
			}
			backups, _ := filepath.Glob(filepath.Join(filepath.Dir(o.Path), "backup-*"))
			if len(backups) != 1 {
				t.Fatal("migration lacked snapshot")
			}
			manifest, err := VerifyBackup(background, backups[0], o.Kind)
			if err != nil || manifest.SchemaVersion != 2 {
				t.Fatal("unverifiable pre-migration backup", err)
			}
			db, err := connect(o.Path, true, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var count, applied int
			_ = db.QueryRow("SELECT count(*) FROM metadata").Scan(&count)
			_ = db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&applied)
			if count != 1 || applied != map[bool]int{true: 2, false: 3}[fail] {
				t.Fatal("migration lost data or partially applied", count, applied)
			}
			_ = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='migration_probe'").Scan(&count)
			if (count == 0) != fail {
				t.Fatal("DDL did not roll back atomically")
			}
		})
	}
}
func TestConsistentWALBackupAndTamperRejection(t *testing.T) {
	o := options(t)
	s := initialized(t, o)
	if err := writeDocument(s, "wal-row", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	path, err := s.Backup(background)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(background, path, repository.Manager); err == nil {
		t.Fatal("cross-kind backup accepted")
	}
	if _, err = VerifyBackup(background, path, o.Kind); err != nil {
		t.Fatal(err)
	}
	db, err := connect(filepath.Join(path, "database.sqlite"), true, 1)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow("SELECT count(*) FROM metadata").Scan(&n)
	_ = db.Close()
	if n != 1 {
		t.Fatal("WAL commit lost from snapshot")
	}
	f, err := os.OpenFile(filepath.Join(path, "database.sqlite"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("tamper"))
	_ = f.Close()
	if _, err = VerifyBackup(background, path, o.Kind); err == nil {
		t.Fatal("tampered backup accepted")
	}
}
func TestUnknownCommitRequiresReconciliation(t *testing.T) {
	s := initialized(t, options(t))
	s.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return errors.New("synthetic lost completion")
	}
	if err := writeDocument(s, "original", []byte(`{}`)); err != repository.ErrCommitUnknown {
		t.Fatal(err)
	}
	if s.Status().Writable || documentCount(t, s) != 1 {
		t.Fatal("lost completion misrepresented as not committed")
	}
	if err := writeDocument(s, "replay", []byte(`{}`)); err != repository.ErrUnavailable {
		t.Fatal("replayed after uncertain commit")
	}
}
func TestOversizedBackupManifestIsRejected(t *testing.T) {
	s := initialized(t, options(t))
	directory, err := s.Backup(background)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "manifest.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte(strings.Repeat(" ", 8193) + `{}`))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(background, directory, repository.Web); err == nil {
		t.Fatal("accepted a manifest with hidden trailing data")
	}
}
func TestTransientProbeFailureDoesNotClaimReadyOrLatch(t *testing.T) {
	s := initialized(t, options(t))
	ctx, cancel := context.WithCancel(background)
	cancel()
	status := s.Probe(ctx)
	if status.State != "degraded" || status.Code != "STORAGE_CANCELED" || status.Writable {
		t.Fatal("canceled probe reported ready", status)
	}
	if status = s.Probe(background); status.State != "ready" || !status.Writable {
		t.Fatal("transient failure latched", status)
	}
}
func TestBusyCheckpointIsARecoverableWarning(t *testing.T) {
	s := initialized(t, options(t))
	if err := writeDocument(s, "before-snapshot", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	c, err := s.readers.Conn(background)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.ExecContext(background, "BEGIN DEFERRED"); err != nil {
		t.Fatal(err)
	}
	defer c.ExecContext(background, "ROLLBACK")
	var n int
	if err = c.QueryRowContext(background, "SELECT count(*) FROM metadata").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err = writeDocument(s, "after-snapshot", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.Checkpoint(background); err != repository.ErrBusy || !s.Status().Writable || s.Status().Warning != "STORAGE_CHECKPOINT_PENDING" {
		t.Fatal("pinned reader misclassified", err, s.Status())
	}
	if _, err = c.ExecContext(background, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err = s.Checkpoint(background); err != nil || s.Status().Warning != "" || !s.Status().Writable {
		t.Fatal("checkpoint did not recover", err, s.Status())
	}
}
func TestReadDeadlineAndWriterBusyAreBounded(t *testing.T) {
	o := options(t)
	s := initialized(t, o)
	started := time.Now()
	err := s.Read(background, func(ctx context.Context, c *sql.Conn) error {
		var n int64
		return c.QueryRowContext(ctx, "WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq WHERE x<1000000000) SELECT sum(x) FROM seq").Scan(&n)
	})
	if err != repository.ErrCanceled || time.Since(started) > 4*time.Second {
		t.Fatal("read deadline not enforced", err, time.Since(started))
	}
	other, err := connect(o.Path, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.BeginTx(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = writeDocument(s, "busy", []byte(`{}`)); err != repository.ErrBusy {
		t.Fatal("busy writer lost its disposition", err)
	}
	if !s.Status().Writable {
		t.Fatal("transient busy latched corruption")
	}
}
func TestCanceledQueueDoesNotLeakCapacity(t *testing.T) {
	s := initialized(t, options(t))
	s.writeSlot <- struct{}{}
	ctx, cancel := context.WithCancel(background)
	done := make(chan error, 1)
	go func() {
		done <- s.Write(ctx, func(context.Context, *sql.Tx) error { t.Error("canceled queued write ran"); return nil })
	}()
	deadline := time.Now().Add(time.Second)
	for len(s.writeQueue) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != repository.ErrCanceled {
		t.Fatal(err)
	}
	<-s.writeSlot
	if len(s.writeQueue) != 0 {
		t.Fatal("waiting slot leaked")
	}
	if err := writeDocument(s, "after", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCrashRecoveryFromRealProcessKill(t *testing.T) {
	o := options(t)
	s := initialized(t, o)
	_ = s.Close()
	for _, mode := range []string{"uncommitted", "committed"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStorageCrashChild$")
		cmd.Env = append(os.Environ(), "OVS_STORAGE_CRASH_CHILD="+mode, "OVS_STORAGE_CRASH_PATH="+o.Path)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ready := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				if scanner.Text() == "READY" {
					ready <- "READY"
					return
				}
			}
			ready <- "EOF"
		}()
		select {
		case line := <-ready:
			if line != "READY" {
				t.Fatal("child not ready", line)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatal("child startup timeout")
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdin.Close()
		s = opened(t, o)
		expected := 0
		if mode == "committed" {
			expected = 1
		}
		if documentCount(t, s) != expected {
			t.Fatal("crash recovery broke commit boundary", mode)
		}
		_ = s.Close()
	}
}
func TestStorageCrashChild(t *testing.T) {
	mode := os.Getenv("OVS_STORAGE_CRASH_CHILD")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	o := Options{Path: os.Getenv("OVS_STORAGE_CRASH_PATH"), Kind: repository.Web, SoftwareVersion: "test-v1"}
	s, err := Open(background, o)
	if err != nil {
		t.Fatal(err)
	}
	block := func() { fmt.Println("READY"); _, _ = io.ReadFull(os.Stdin, make([]byte, 1)) }
	if mode == "committed" {
		if err = writeDocument(s, "durable", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		block()
	} else {
		_ = s.Write(background, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES('label','owner','not-durable',?)", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			block()
			return nil
		})
	}
	_ = s.Close()
}

func TestFilesystemFull(t *testing.T) {
	root := os.Getenv("OVS_STORAGE_FULL_DIR")
	if root == "" {
		t.Skip("requires isolated bounded tmpfs from CI")
	}
	o := Options{Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "test-v1"}
	s := initialized(t, o)
	if err := writeDocument(s, "prior", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(background); err != nil {
		t.Fatal(err)
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Type != 0x01021994 || stat.Blocks*uint64(stat.Bsize) > 8<<20 {
		t.Fatal("test requires small dedicated tmpfs")
	}
	filler := filepath.Join(root, "filler")
	f, err := os.OpenFile(filler, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	target := int64(stat.Bavail)*stat.Bsize - 32*1024
	if target > 0 {
		_, err = io.CopyN(f, strings.NewReader(strings.Repeat("x", int(target))), target)
	}
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = writeDocument(s, "full", []byte(`{"data":"`+strings.Repeat("x", 900000)+`"}`))
	if err == nil || s.Status().Writable {
		t.Fatal("filesystem full did not stop admission", err, s.Status())
	}
	_ = s.Close()
	if err = os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	s = opened(t, o)
	count := documentCount(t, s)
	if count < 1 || count > 2 {
		t.Fatal("prior committed data lost")
	}
	t.Log("real tmpfs exhaustion preserved prior commit and required recovery")
}
