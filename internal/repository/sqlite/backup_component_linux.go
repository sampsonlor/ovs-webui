package sqlite

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const backupComponentMagic = "OVSDBBK1"
const backupComponentHeader = 20

// WriteBackupComponent streams only a verified private snapshot owned by the
// current store identity. An offline coordinator must hold each store's
// exclusive process guard and give this backend a context-bound private pipe.
func (s *Store) WriteBackupComponent(ctx context.Context, pipe io.Writer) error {
	if pipe == nil {
		return repository.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, backupPairTimeout)
	defer cancel()
	owned, err := os.MkdirTemp(filepath.Dir(s.options.Path), ".backup-component-")
	if err != nil {
		return repository.ErrUnavailable
	}
	defer func() { _ = os.RemoveAll(owned) }()
	directory := filepath.Join(owned, "snapshot")
	if err := s.capturePairSnapshot(ctx, directory); err != nil {
		return err
	}
	info, err := verifyPairSnapshot(ctx, directory, s.Kind())
	if err != nil {
		return err
	}
	manifest, err := readPairManifest(filepath.Join(directory, pairManifestFile))
	if err != nil {
		return err
	}
	image, err := os.Open(filepath.Join(directory, pairDatabaseFile))
	if err != nil {
		return repository.ErrUnavailable
	}
	defer image.Close()

	var header [backupComponentHeader]byte
	copy(header[:8], backupComponentMagic)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(manifest)))
	binary.BigEndian.PutUint64(header[12:], uint64(info.DatabaseBytes))
	if _, err := io.Copy(pipe, bytes.NewReader(header[:])); err != nil {
		return pairUnavailable(ctx)
	}
	if _, err := io.Copy(pipe, bytes.NewReader(manifest)); err != nil {
		return pairUnavailable(ctx)
	}
	if _, err := io.CopyN(pipe, backupStreamReader{ctx, image}, info.DatabaseBytes); err != nil {
		return pairUnavailable(ctx)
	}
	return nil
}

// PublishBackupPairStreams allows a trusted coordinator to transfer the two
// owned snapshots over private pipes under separate effective UIDs. Stream
// bytes alone do not prove that the two writer locks overlapped.
func PublishBackupPairStreams(ctx context.Context, outputRoot string, manager, web io.Reader) (BackupPairManifest, error) {
	if manager == nil || web == nil {
		return BackupPairManifest{}, repository.ErrInvalid
	}
	return publishBackupPair(ctx, outputRoot, func(ctx context.Context, directory string, kind repository.Kind) error {
		if kind == repository.Manager {
			return receiveBackupComponent(ctx, manager, directory, kind)
		}
		return receiveBackupComponent(ctx, web, directory, kind)
	})
}

type backupStreamReader struct {
	ctx context.Context
	in  io.Reader
}

func (r backupStreamReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(data)
}

func receiveBackupComponent(ctx context.Context, pipe io.Reader, directory string, kind repository.Kind) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	reader := backupStreamReader{ctx, pipe}
	var header [backupComponentHeader]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil || string(header[:8]) != backupComponentMagic {
		return pairUnavailable(ctx)
	}
	manifestSize := binary.BigEndian.Uint32(header[8:12])
	imageSize := binary.BigEndian.Uint64(header[12:])
	if manifestSize == 0 || manifestSize > MaxBackupPairManifestBytes || imageSize == 0 || imageSize > MaxBackupPairDatabaseBytes {
		return ErrBackupPairTooLarge
	}
	metadata := make([]byte, manifestSize)
	var manifest BackupManifest
	if _, err := io.ReadFull(reader, metadata); err != nil || json.Unmarshal(metadata, &manifest) != nil ||
		manifest.Kind != kind || !exactPairJSON(metadata, manifest) {
		return pairUnavailable(ctx)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return repository.ErrUnavailable
	}
	file, err := os.OpenFile(filepath.Join(directory, pairDatabaseFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return repository.ErrUnavailable
	}
	if _, err = io.CopyN(file, reader, int64(imageSize)); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return pairUnavailable(ctx)
	}
	var extra [1]byte
	if _, err := io.ReadFull(reader, extra[:]); err != io.EOF {
		return pairUnavailable(ctx)
	}
	return writePairManifest(directory, metadata)
}
