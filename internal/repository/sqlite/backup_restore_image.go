package sqlite

import (
	"database/sql"
	"io"
	"io/fs"
	"os"
	"strings"

	"modernc.org/sqlite/vfs"
)

const restoreImageName = "restore.sqlite"

type restoreImageFS struct {
	file *os.File
	info fs.FileInfo
}

func (image restoreImageFS) Open(name string) (fs.File, error) {
	if name != restoreImageName {
		return nil, fs.ErrNotExist
	}
	return restoreImageView{SectionReader: io.NewSectionReader(image.file, 0, image.info.Size()), info: image.info}, nil
}

type restoreImageView struct {
	*io.SectionReader
	info fs.FileInfo
}

func (image restoreImageView) Stat() (fs.FileInfo, error) { return image.info, nil }
func (restoreImageView) Close() error                     { return nil }

// openPinnedRestoreImage makes SQLite read the same descriptor used for the
// digest. The VFS exposes one immutable file and cannot open any host path.
func openPinnedRestoreImage(file *os.File, info fs.FileInfo) (*sql.DB, func(), error) {
	name, mounted, err := vfs.New(restoreImageFS{file: file, info: info})
	if err != nil {
		return nil, nil, err
	}
	// Use the store's read-only options with the VFS's relative file name.
	_, options, _ := strings.Cut(dsn("/"+restoreImageName, true), "?")
	db, err := sql.Open("sqlite", "file:"+restoreImageName+"?"+options+"&immutable=1&vfs="+name)
	if err != nil {
		_ = mounted.Close()
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	return db, func() {
		_ = db.Close()
		_ = mounted.Close()
	}, nil
}
