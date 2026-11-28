package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/backupop"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const (
	stageRestoreFlag       = "stage-offline-restore-pair"
	verifyRestoreFlag      = "verify-offline-restore-stage"
	restoreRootFlag        = "restore-stage-root"
	prepareRestoreFlag     = "prepare-fresh-restore-stage"
	verifyFreshRestoreFlag = "verify-fresh-restore-stage"
	restoreDestinationFlag = "restore-destination-root"
)

type offlineRestoreFlags struct {
	flags                          *flag.FlagSet
	backupID, stageID, stagingRoot string
	prepareID, verifyFreshID       string
	fresh                          backupop.FreshRestoreRequest
}

type offlineRestoreCommand struct {
	backupID, backupRoot string
	stageID, stagingRoot string
	fresh                *backupop.FreshRestoreRequest
	verifyFresh          *backupop.VerifyFreshRestoreRequest
}

func registerOfflineRestoreFlags(flags *flag.FlagSet) *offlineRestoreFlags {
	o := &offlineRestoreFlags{flags: flags}
	flags.StringVar(&o.backupID, stageRestoreFlag, "", "Copy an exact backup pair into a private offline restore stage")
	flags.StringVar(&o.stageID, verifyRestoreFlag, "", "Verify an exact sealed offline restore stage")
	flags.StringVar(&o.stagingRoot, restoreRootFlag, "", "Existing root-owned private directory for sealed restore stages")
	flags.StringVar(&o.prepareID, prepareRestoreFlag, "", "Prepare a distinct fresh offline destination from this sealed stage")
	flags.StringVar(&o.verifyFreshID, verifyFreshRestoreFlag, "", "Verify the existing fresh offline destination for this sealed stage")
	flags.StringVar(&o.fresh.DestinationRoot, restoreDestinationFlag, "", "Existing dedicated trusted parent for fresh restore destinations")
	flags.StringVar(&o.fresh.ManagerExecutableSHA256, "restore-manager-sha256", "", "Reviewed SHA-256 of installed ovs-mgrd")
	flags.StringVar(&o.fresh.WebExecutableSHA256, "restore-web-sha256", "", "Reviewed SHA-256 of installed ovs-webd")
	flags.StringVar(&o.fresh.ManagerAuthKey, "restore-manager-auth-key", "", "Reviewed Manager auth key file")
	flags.StringVar(&o.fresh.ManagerSecretKeys, "restore-manager-secret-keys", "", "Reviewed Manager SecretStore key directory")
	flags.StringVar(&o.fresh.WebSessionKey, "restore-web-session-key", "", "Reviewed Web legacy session key file")
	flags.StringVar(&o.fresh.WebSessionKeys, "restore-web-session-keys", "", "Reviewed Web session key directory")
	flags.StringVar(&o.fresh.WebTLSKeys, "restore-web-tls-keys", "", "Reviewed Web TLS key directory")
	flags.StringVar(&o.fresh.WebTrustAnchors, "restore-web-trust-file", "", "Reviewed Web private certificate trust file")
	flags.StringVar(&o.fresh.PublicOrigin, "restore-public-origin", "", "Approved HTTPS origin for retained Web TLS identity")
	return o
}

func (o *offlineRestoreFlags) command(backupRoot string) (offlineRestoreCommand, bool, error) {
	var stage, verify, prepare, verifyFresh, stagingRoot, sourceRoot, extra, targetOptions, preparationOptions bool
	o.flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case stageRestoreFlag:
			stage = true
		case verifyRestoreFlag:
			verify = true
		case prepareRestoreFlag:
			prepare = true
		case verifyFreshRestoreFlag:
			verifyFresh = true
		case restoreRootFlag:
			stagingRoot = true
		case "backup-pair-root":
			sourceRoot = true
		case restoreDestinationFlag, "restore-manager-sha256", "restore-web-sha256":
			targetOptions = true
		case "restore-manager-auth-key",
			"restore-manager-secret-keys", "restore-web-session-key", "restore-web-session-keys", "restore-web-tls-keys",
			"restore-web-trust-file", "restore-public-origin":
			preparationOptions = true
		default:
			extra = true
		}
	})
	if !stage && !verify && !prepare && !verifyFresh && !stagingRoot && !targetOptions && !preparationOptions {
		return offlineRestoreCommand{}, false, nil
	}
	if !stagingRoot || !absoluteMaintenanceRoot(o.stagingRoot) || extra || o.flags.NArg() != 0 {
		return offlineRestoreCommand{}, true, repository.ErrInvalid
	}
	if verifyFresh {
		if stage || verify || prepare || sourceRoot || preparationOptions || !apitypes.ManagementID(o.verifyFreshID) ||
			!absoluteMaintenanceRoot(o.fresh.DestinationRoot) || o.fresh.ManagerExecutableSHA256 == "" || o.fresh.WebExecutableSHA256 == "" {
			return offlineRestoreCommand{}, true, repository.ErrInvalid
		}
		return offlineRestoreCommand{verifyFresh: &backupop.VerifyFreshRestoreRequest{
			StageRoot: o.stagingRoot, StageID: o.verifyFreshID, DestinationRoot: o.fresh.DestinationRoot,
			ManagerExecutableSHA256: o.fresh.ManagerExecutableSHA256, WebExecutableSHA256: o.fresh.WebExecutableSHA256,
		}}, true, nil
	}
	if prepare {
		if stage || verify || sourceRoot || !apitypes.ManagementID(o.prepareID) || !absoluteMaintenanceRoot(o.fresh.DestinationRoot) {
			return offlineRestoreCommand{}, true, repository.ErrInvalid
		}
		fresh := o.fresh
		fresh.StageRoot, fresh.StageID = o.stagingRoot, o.prepareID
		return offlineRestoreCommand{fresh: &fresh}, true, nil
	}
	if stage == verify || targetOptions || preparationOptions {
		return offlineRestoreCommand{}, true, repository.ErrInvalid
	}
	if stage && sourceRoot && absoluteMaintenanceRoot(backupRoot) && apitypes.ManagementID(o.backupID) {
		return offlineRestoreCommand{backupID: o.backupID, backupRoot: backupRoot, stagingRoot: o.stagingRoot}, true, nil
	}
	if verify && !sourceRoot && apitypes.ManagementID(o.stageID) {
		return offlineRestoreCommand{stageID: o.stageID, stagingRoot: o.stagingRoot}, true, nil
	}
	return offlineRestoreCommand{}, true, repository.ErrInvalid
}

func absoluteMaintenanceRoot(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

func runOfflineRestore(ctx context.Context, logger *slog.Logger, output io.Writer, command offlineRestoreCommand) int {
	if command.verifyFresh != nil {
		result, err := backupop.VerifyFreshRestore(ctx, *command.verifyFresh)
		return reportOfflineRestore(logger, output, result, err)
	}
	if command.fresh != nil {
		result, err := backupop.PrepareFreshRestore(ctx, *command.fresh)
		return reportOfflineRestore(logger, output, result, err)
	}
	var result sqlite.RestoreStageManifest
	var err error
	if command.backupID != "" {
		result, err = sqlite.StageRestorePair(ctx, command.backupRoot, command.backupID, command.stagingRoot)
	} else {
		result, err = sqlite.VerifyStagedRestorePair(ctx, command.stagingRoot, command.stageID)
	}
	return reportOfflineRestore(logger, output, result, err)
}

func reportOfflineRestore(logger *slog.Logger, output io.Writer, result any, err error) int {
	if err != nil && !errors.Is(err, repository.ErrCommitUnknown) {
		code := "RESTORE_STAGE_UNAVAILABLE"
		if errors.Is(err, repository.ErrCanceled) {
			code = "RESTORE_STAGE_CANCELED"
		}
		logger.Error("restore_stage_failed", "code", code)
		return 1
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		logger.Error("restore_stage_report_unavailable", "code", "RESTORE_STAGE_REPORT_UNAVAILABLE")
		return 1
	}
	if err != nil {
		logger.Error("restore_stage_publication_unproven", "code", "STORAGE_COMMIT_UNKNOWN")
		return 1
	}
	return 0
}
