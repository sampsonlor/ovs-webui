package backupop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

func TestFreshRestoreRequestBindsOwnerCredentialsAndDestination(t *testing.T) {
	root := FreshRestoreRequest{
		StageRoot: "/sealed/stages", StageID: repository.NewID(), DestinationRoot: "/prepared/restores",
		ManagerExecutableSHA256: strings.Repeat("a", 64), WebExecutableSHA256: strings.Repeat("b", 64),
		ManagerAuthKey: "/var/lib/ovs-webui/manager/auth.key", WebSessionKeys: "/var/lib/ovs-webui/web/session-keys",
		PublicOrigin: "https://127.0.0.1:9443",
	}
	if !validFreshRequest(root) {
		t.Fatal("valid owner-specific paths rejected")
	}
	for _, change := range []func(*FreshRestoreRequest){
		func(r *FreshRestoreRequest) { r.ManagerExecutableSHA256 = strings.Repeat("A", 64) },
		func(r *FreshRestoreRequest) { r.StageID = "../different" },
		func(r *FreshRestoreRequest) { r.ManagerAuthKey = "/sealed/stages/inside" },
		func(r *FreshRestoreRequest) { r.WebSessionKeys = "/prepared/restores/inside" },
		func(r *FreshRestoreRequest) { r.WebSessionKey = "/keys/ambiguous" },
		func(r *FreshRestoreRequest) { r.PublicOrigin = "https://secret@127.0.0.1:9443" },
		func(r *FreshRestoreRequest) { r.PublicOrigin = "http://127.0.0.1" },
	} {
		modified := root
		change(&modified)
		if validFreshRequest(modified) {
			t.Fatal("unsafe request accepted")
		}
	}
	if restoreOwnerCanTraverse(0701, 999, 999) || !restoreOwnerCanTraverse(0710, 999, 999) ||
		!restoreOwnerCanTraverse(0711, 0, 999) {
		t.Fatal("did not apply the effective Web group mode")
	}
}

func TestFreshRestoreRequiresActualCommittedChangesForBothOwners(t *testing.T) {
	manager := sqlite.RestoreCopyObservation{DatabaseID: repository.NewID(), APIEpoch: repository.NewID(), AuthEpoch: repository.NewID(),
		SHA256: strings.Repeat("a", 64), Schema: 9, RestoreAuditCount: 4}
	web := sqlite.RestoreCopyObservation{DatabaseID: repository.NewID(), APIEpoch: repository.NewID(), SHA256: strings.Repeat("b", 64),
		TLSStateSHA256: strings.Repeat("e", 64), Schema: 7}
	before := restoreObservations{manager, web}
	manager.APIEpoch, manager.AuthEpoch, manager.SHA256 = repository.NewID(), repository.NewID(), strings.Repeat("c", 64)
	manager.RestoreAuditCount++
	manager.SecurityReset, manager.ReconciliationRequired = true, true
	web.APIEpoch, web.SHA256, web.TLSStateSHA256 = repository.NewID(), strings.Repeat("d", 64), strings.Repeat("f", 64)
	web.SecurityReset = true
	after := restoreObservations{manager, web}
	if !preparedObservations(before, after) {
		t.Fatal("complete owner proof was rejected")
	}
	for _, change := range []func(*restoreObservations){
		func(o *restoreObservations) { o.manager.RestoreAuditCount-- },
		func(o *restoreObservations) { o.manager.ReconciliationRequired = false },
		func(o *restoreObservations) { o.manager.UnsettledNative = true },
		func(o *restoreObservations) { o.web.TLSStateSHA256 = "" },
		func(o *restoreObservations) { o.web.TLSStateSHA256 = before.web.TLSStateSHA256 },
		func(o *restoreObservations) { o.web.APIEpoch = before.web.APIEpoch },
		func(o *restoreObservations) { o.web.SecurityReset = false },
		func(o *restoreObservations) { o.web.DatabaseID = repository.NewID() },
	} {
		modified := after
		change(&modified)
		if preparedObservations(before, modified) {
			t.Fatal("partial or contradictory proof accepted")
		}
	}
}

func TestFreshRestoreWorkspacePreservesStageAndRefusesReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("exact separate-owner mode test requires a disposable Linux root fixture")
	}
	ctx := t.Context()
	root := t.TempDir()
	for _, directory := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(directory, 0711); err != nil {
			t.Fatal(err)
		}
	}
	private := func(name string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	manager := restoreSource(t, private("old-manager", 0700), repository.Manager)
	web := restoreSource(t, private("old-web", 0700), repository.Web)
	backupRoot, stageRoot, destination := private("backup", 0700), private("stage", 0700), private("new", 0711)
	pair, err := sqlite.PublishBackupPair(ctx, backupRoot, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := sqlite.StageRestorePair(ctx, backupRoot, pair.ID, stageRoot)
	if err != nil {
		t.Fatal(err)
	}
	work, err := openRestoreWorkspace(ctx, stageRoot, stage.ID, destination, restoreOwner{61234, 61234})
	if err != nil {
		t.Fatal(err)
	}
	defer work.close()
	before, err := work.inspectClosed(ctx)
	if err != nil || !matchesOriginal(before.manager, pair.Manager.BackupManifest) || !matchesOriginal(before.web, pair.Web.BackupManifest) {
		t.Fatalf("independent working image proof failed: %v", err)
	}
	if _, err := work.publish(ctx); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatalf("unguarded publication accepted: %v", err)
	}
	release, err := work.lockSlots()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if other, err := openRestoreFile(work.database(repository.Web)+".lock", work.web.uid, true); err == nil {
		_ = other.Close()
		t.Fatal("same owner's competing process lock could be acquired")
	}
	final := filepath.Join(destination, freshRestorePrefix+stage.ID)
	if err := os.Mkdir(final, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := work.publish(ctx); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("existing destination was replaceable: %v", err)
	}
	if _, err := openRestoreWorkspace(ctx, stageRoot, stage.ID, destination, work.web); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("same stage replay was accepted: %v", err)
	}
	if verified, err := sqlite.VerifyStagedRestorePair(ctx, stageRoot, stage.ID); err != nil || verified.ID != stage.ID {
		t.Fatalf("failed publication changed the sealed stage: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := work.publish(canceled); !errors.Is(err, repository.ErrCanceled) {
		t.Fatalf("canceled publication returned %v", err)
	}
}

func restoreSource(t *testing.T, directory string, kind repository.Kind) *sqlite.Store {
	t.Helper()
	opts := sqlite.Options{Path: filepath.Join(directory, string(kind)+".db"), Kind: kind, SoftwareVersion: "synthetic-test"}
	if err := sqlite.Initialize(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
