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
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

type offlineCheckpointOptions struct {
	flags                             *flag.FlagSet
	root, generation, publish, verify string
}

func registerOfflineCheckpointFlags(flags *flag.FlagSet) *offlineCheckpointOptions {
	o := &offlineCheckpointOptions{flags: flags}
	flags.StringVar(&o.root, "checkpoint-root", "", "Existing private root directory for one independent VLAN checkpoint")
	flags.StringVar(&o.generation, "checkpoint-generation", "", "Exact confirmed instance generation reviewed for the VLAN checkpoint")
	flags.StringVar(&o.publish, "publish-vlan-checkpoint", "", "Offline: publish one supported confirmed VLAN checkpoint for this transaction")
	flags.StringVar(&o.verify, "verify-vlan-checkpoint", "", "Offline: verify the independent VLAN checkpoint for this transaction")
	return o
}

func (o *offlineCheckpointOptions) command() (sqlite.VLANCheckpointRequest, bool, error) {
	seen := map[string]bool{}
	o.flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	publish, verify := seen["publish-vlan-checkpoint"], seen["verify-vlan-checkpoint"]
	if !publish && !verify && !seen["checkpoint-root"] && !seen["checkpoint-generation"] {
		return sqlite.VLANCheckpointRequest{}, false, nil
	}
	for name := range seen {
		switch name {
		case "publish-vlan-checkpoint", "verify-vlan-checkpoint", "checkpoint-root", "checkpoint-generation", "database":
		default:
			return sqlite.VLANCheckpointRequest{}, true, repository.ErrInvalid
		}
	}
	id := o.publish
	if verify {
		id = o.verify
	}
	if publish == verify || verify && seen["database"] ||
		!apitypes.ManagementID(id) || !apitypes.ManagementID(o.generation) ||
		!filepath.IsAbs(o.root) || filepath.Clean(o.root) != o.root {
		return sqlite.VLANCheckpointRequest{}, true, repository.ErrInvalid
	}
	return sqlite.VLANCheckpointRequest{Root: o.root, TransactionID: id, Generation: o.generation}, true, nil
}

func runOfflineCheckpoint(ctx context.Context, logger *slog.Logger, out io.Writer, req sqlite.VLANCheckpointRequest, source string, publish bool) int {
	var result sqlite.VLANCheckpoint
	var err error
	if publish {
		result, err = sqlite.PublishVLANCheckpoint(ctx, source, req)
	} else {
		result, err = sqlite.VerifyVLANCheckpoint(ctx, req)
	}
	if err != nil && !errors.Is(err, repository.ErrCommitUnknown) {
		code := "CHECKPOINT_EVIDENCE_UNAVAILABLE"
		switch {
		case errors.Is(err, sqlite.ErrCheckpointScope):
			code = "CHECKPOINT_SCOPE_UNSUPPORTED"
		case errors.Is(err, repository.ErrConflict):
			code = "CHECKPOINT_DESTINATION_CONFLICT"
		}
		logger.Error("offline_checkpoint_failed", "code", code)
		return 1
	}
	if json.NewEncoder(out).Encode(result) != nil {
		logger.Error("offline_checkpoint_report_unavailable", "code", "CHECKPOINT_REPORT_UNAVAILABLE")
		return 1
	}
	if err != nil {
		logger.Error("offline_checkpoint_publication_unproven", "code", "STORAGE_COMMIT_UNKNOWN")
		return 1
	}
	return 0
}
