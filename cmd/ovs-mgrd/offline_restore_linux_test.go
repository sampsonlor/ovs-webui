package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const offlineRestoreTestID = "00000000-0000-4000-8000-000000000009"

func restoreTestCommand(t *testing.T, args ...string) (offlineRestoreCommand, bool, error) {
	t.Helper()
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backupRoot := flags.String("backup-pair-root", "", "")
	flags.String("database", "", "")
	flags.Bool("version", false, "")
	flags.Bool("prepare-restore-security", false, "")
	options := registerOfflineRestoreFlags(flags)
	if err := flags.Parse(args); err != nil {
		t.Fatal(err)
	}
	return options.command(*backupRoot)
}

func TestOfflineRestoreCLIOptions(t *testing.T) {
	for _, item := range []struct {
		name               string
		args               []string
		want               offlineRestoreCommand
		requested, invalid bool
	}{
		{name: "ordinary arguments", args: []string{"--backup-pair-root=/backup", "--database=/state/live.sqlite"}},
		{name: "stage", args: []string{"--stage-offline-restore-pair=" + offlineRestoreTestID, "--backup-pair-root=/backup", "--restore-stage-root=/stage"},
			want: offlineRestoreCommand{backupID: offlineRestoreTestID, backupRoot: "/backup", stagingRoot: "/stage"}, requested: true},
		{name: "verify", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage"},
			want: offlineRestoreCommand{stageID: offlineRestoreTestID, stagingRoot: "/stage"}, requested: true},
		{name: "empty stage selector", args: []string{"--stage-offline-restore-pair="}, requested: true, invalid: true},
		{name: "empty verify selector", args: []string{"--verify-offline-restore-stage="}, requested: true, invalid: true},
		{name: "standalone stage root", args: []string{"--restore-stage-root="}, requested: true, invalid: true},
		{name: "missing stage root", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID}, requested: true, invalid: true},
		{name: "relative stage root", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=./stage"}, requested: true, invalid: true},
		{name: "dirty stage root", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage/../root"}, requested: true, invalid: true},
		{name: "unscoped id", args: []string{"--verify-offline-restore-stage=../" + offlineRestoreTestID, "--restore-stage-root=/stage"}, requested: true, invalid: true},
		{name: "missing backup root", args: []string{"--stage-offline-restore-pair=" + offlineRestoreTestID, "--restore-stage-root=/stage"}, requested: true, invalid: true},
		{name: "two actions", args: []string{"--stage-offline-restore-pair=" + offlineRestoreTestID, "--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage", "--backup-pair-root=/backup"}, requested: true, invalid: true},
		{name: "verify and source path", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage", "--backup-pair-root="}, requested: true, invalid: true},
		{name: "live override", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage", "--database=/state/live.sqlite"}, requested: true, invalid: true},
		{name: "other prepare", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage", "--prepare-restore-security"}, requested: true, invalid: true},
		{name: "version output mixed", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage", "--version"}, requested: true, invalid: true},
		{name: "unexpected positional", args: []string{"--verify-offline-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/stage", "unexpected"}, requested: true, invalid: true},
	} {
		t.Run(item.name, func(t *testing.T) {
			command, requested, err := restoreTestCommand(t, item.args...)
			if command != item.want || requested != item.requested || errors.Is(err, repository.ErrInvalid) != item.invalid {
				t.Fatalf("command %+v, requested %v, error %v", command, requested, err)
			}
		})
	}
}

func TestFreshRestoreCLIRejectsLiveOrMixedPaths(t *testing.T) {
	args := []string{"--prepare-fresh-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/sealed",
		"--restore-destination-root=/prepared", "--restore-manager-auth-key=/keys/manager/auth.key",
		"--restore-web-session-keys=/keys/web/sessions", "--restore-public-origin=https://approved.invalid"}
	command, requested, err := restoreTestCommand(t, args...)
	if !requested || err != nil || command.fresh == nil || command.fresh.StageID != offlineRestoreTestID ||
		command.fresh.StageRoot != "/sealed" || command.fresh.DestinationRoot != "/prepared" {
		t.Fatalf("prepare command could not be resolved: %+v %v", command, err)
	}
	for _, flag := range []string{
		"--database=/var/lib/ovs-webui/manager/manager.db", "--verify-offline-restore-stage=" + offlineRestoreTestID,
		"--backup-pair-root=/backup", "--prepare-restore-security",
	} {
		command, requested, err := restoreTestCommand(t, append(args, flag)...)
		if !requested || !errors.Is(err, repository.ErrInvalid) || command.fresh != nil {
			t.Fatalf("mixed action %q accepted: %+v %v", flag, command, err)
		}
	}
	if _, requested, err := restoreTestCommand(t, "--restore-web-sha256="+strings.Repeat("a", 64)); !requested || !errors.Is(err, repository.ErrInvalid) {
		t.Fatalf("standalone owner evidence fell through to daemon startup: %v %v", requested, err)
	}
}

func TestVerifyFreshRestoreCLIRequiresRecordedBuildHashesAndOnlyOfflinePaths(t *testing.T) {
	manager, web := strings.Repeat("a", 64), strings.Repeat("b", 64)
	args := []string{"--verify-fresh-restore-stage=" + offlineRestoreTestID, "--restore-stage-root=/sealed",
		"--restore-destination-root=/prepared", "--restore-manager-sha256=" + manager, "--restore-web-sha256=" + web}
	command, requested, err := restoreTestCommand(t, args...)
	verified := command.verifyFresh
	if !requested || err != nil || verified == nil || command.fresh != nil || verified.StageID != offlineRestoreTestID ||
		verified.StageRoot != "/sealed" || verified.DestinationRoot != "/prepared" ||
		verified.ManagerExecutableSHA256 != manager || verified.WebExecutableSHA256 != web {
		t.Fatalf("verify command could not be resolved: %+v %v", command, err)
	}
	for index := range args {
		partial := append(append([]string{}, args[:index]...), args[index+1:]...)
		if command, requested, err := restoreTestCommand(t, partial...); !requested || !errors.Is(err, repository.ErrInvalid) || command.verifyFresh != nil {
			t.Fatalf("verify accepted omitted option %d: %+v %v", index, command, err)
		}
	}
	for _, option := range []string{"--database=/var/lib/ovs-webui/web/web.db", "--backup-pair-root=/backup", "--prepare-restore-security",
		"--verify-offline-restore-stage=" + offlineRestoreTestID, "--prepare-fresh-restore-stage=" + offlineRestoreTestID,
		"--restore-manager-auth-key=", "--restore-web-session-key=/read-not-needed", "--restore-public-origin=https://fixture.invalid"} {
		if command, requested, err := restoreTestCommand(t, append(args, option)...); !requested || !errors.Is(err, repository.ErrInvalid) || command.verifyFresh != nil {
			t.Fatalf("verify accepted mixed option %q: %+v %v", option, command, err)
		}
	}
}

func TestOfflineRestoreCLIReportsOnlySealedEvidence(t *testing.T) {
	report := sqlite.RestoreStageManifest{ID: offlineRestoreTestID, State: "sealed"}
	for _, test := range []struct {
		name string
		err  error
		want int
		json bool
		log  string
	}{
		{"sealed", nil, 0, true, ""},
		{"uncertain publication", repository.ErrCommitUnknown, 1, true, "STORAGE_COMMIT_UNKNOWN"},
		{"canceled", repository.ErrCanceled, 1, false, "RESTORE_STAGE_CANCELED"},
		{"sanitized error", errors.New("/private-path/credential-key"), 1, false, "RESTORE_STAGE_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, log bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&log, nil))
			if got := reportOfflineRestore(logger, &output, report, test.err); got != test.want {
				t.Fatalf("exit=%d want=%d", got, test.want)
			}
			var decoded sqlite.RestoreStageManifest
			if test.json && (json.Unmarshal(output.Bytes(), &decoded) != nil || !reflect.DeepEqual(decoded, report)) ||
				!test.json && output.Len() != 0 || !strings.Contains(log.String(), test.log) ||
				strings.Contains(log.String(), "/private-path") {
				t.Fatalf("incorrect output %q or log %q", &output, &log)
			}
		})
	}
}

func TestOfflineRestoreCLIStageAndVerify(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("offline coordinator requires root")
	}
	stores := make([]*sqlite.Store, 0, 2)
	for _, kind := range []repository.Kind{repository.Manager, repository.Web} {
		options := sqlite.Options{Kind: kind, Path: filepath.Join(t.TempDir(), "database.sqlite"), SoftwareVersion: "test-restore"}
		if err := sqlite.Initialize(t.Context(), options); err != nil {
			t.Fatal(err)
		}
		store, err := sqlite.Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		stores = append(stores, store)
	}
	backupRoot, stageRoot := t.TempDir(), t.TempDir()
	pair, err := sqlite.PublishBackupPair(t.Context(), backupRoot, stores[0], stores[1])
	if err != nil {
		t.Fatal(err)
	}
	var output, log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	command, requested, err := restoreTestCommand(t, "--stage-offline-restore-pair="+pair.ID,
		"--backup-pair-root="+backupRoot, "--restore-stage-root="+stageRoot)
	if !requested || err != nil || runOfflineRestore(t.Context(), logger, &output, command) != 0 {
		t.Fatalf("stage invocation failed: %v, %s", err, &log)
	}
	var staged sqlite.RestoreStageManifest
	if err := json.Unmarshal(output.Bytes(), &staged); err != nil || staged.BackupPair != pair {
		t.Fatalf("expected exact sealed stage, got %+v (%v)", staged, err)
	}
	output.Reset()
	command, requested, err = restoreTestCommand(t, "--verify-offline-restore-stage="+staged.ID, "--restore-stage-root="+stageRoot)
	if !requested || err != nil || runOfflineRestore(t.Context(), logger, &output, command) != 0 {
		t.Fatalf("verify invocation failed: %v, %s", err, &log)
	}
	var checked sqlite.RestoreStageManifest
	if err := json.Unmarshal(output.Bytes(), &checked); err != nil || !reflect.DeepEqual(staged, checked) {
		t.Fatalf("sealed output changed on re-read: %+v (%v)", checked, err)
	}
}
