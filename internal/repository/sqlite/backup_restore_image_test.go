package sqlite

import (
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedRestoreImageReadsOnlySelectedDescriptor(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "selected.sqlite")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMaxOpenConns(1)
	var journal string
	if err := writer.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil || journal != "wal" {
		_ = writer.Close()
		t.Fatalf("prepare fixture WAL mode: %q %v", journal, err)
	}
	_, writeErr := writer.ExecContext(ctx, `CREATE TABLE selected (marker TEXT);
INSERT INTO selected VALUES ('original fixture')`)
	if closeErr := writer.Close(); writeErr != nil || closeErr != nil {
		t.Fatalf("write isolated fixture: %v %v", writeErr, closeErr)
	}
	if _, err := os.Stat(path + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated image did not close without a WAL sidecar: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	image := restoreImageFS{file: file, info: info}
	if wrong, err := image.Open(path); !errors.Is(err, fs.ErrNotExist) {
		if wrong != nil {
			_ = wrong.Close()
		}
		t.Fatalf("VFS accepted a host path: %v", err)
	}
	db, cleanup, err := openPinnedRestoreImage(file, info)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var marker string
	if err := db.QueryRowContext(ctx, "SELECT marker FROM selected").Scan(&marker); err != nil || marker != "original fixture" {
		t.Fatalf("could not read selected descriptor after hashing: %q %v", marker, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM selected"); err == nil {
		t.Fatal("offline image view accepted a write")
	}
}
