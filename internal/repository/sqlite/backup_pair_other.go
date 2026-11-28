//go:build !linux

package sqlite

import (
	"context"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func PublishBackupPair(context.Context, string, *Store, *Store) (BackupPairManifest, error) {
	return BackupPairManifest{}, repository.ErrUnavailable
}

func VerifyBackupPair(context.Context, string, string) (BackupPairManifest, error) {
	return BackupPairManifest{}, repository.ErrUnavailable
}
