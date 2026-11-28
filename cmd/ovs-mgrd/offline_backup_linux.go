//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"

	"github.com/sampsonlor/ovs-webui/internal/backupop"
	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

func runOfflineBackup(ctx context.Context, logger *slog.Logger, create bool, id, root, managerDB, webDB string, uid, gid uint) int {
	var output any
	var err error
	if create {
		if uid == 0 || gid == 0 || uint64(uid) >= 1<<32-1 || uint64(gid) >= 1<<32-1 {
			logger.Error("backup_request_rejected", "code", "BACKUP_WEB_OWNER_REQUIRED")
			return 2
		}
		output, err = backupop.CreateOfflinePair(ctx, backupop.PairRequest{
			OutputRoot: root, ManagerDatabase: managerDB, WebDatabase: webDB,
			WebUID: uint32(uid), WebGID: uint32(gid), Version: buildinfo.SoftwareVersion(),
		})
	} else {
		output, err = sqlite.InspectBackupRestore(ctx, root, id, nil)
	}
	if err != nil {
		if errors.Is(err, repository.ErrCommitUnknown) {
			_ = json.NewEncoder(os.Stdout).Encode(output)
			logger.Error("backup_publication_unproven", "code", "STORAGE_COMMIT_UNKNOWN")
		} else {
			logger.Error("backup_request_failed", "code", "BACKUP_UNAVAILABLE")
		}
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		logger.Error("backup_output_unavailable", "code", "BACKUP_REPORT_UNAVAILABLE")
		return 1
	}
	return 0
}
