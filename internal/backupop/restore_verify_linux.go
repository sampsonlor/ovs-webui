package backupop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"golang.org/x/sys/unix"
)

type VerifyFreshRestoreRequest struct {
	StageRoot, StageID, DestinationRoot          string
	ManagerExecutableSHA256, WebExecutableSHA256 string
}

// VerifyFreshRestore checks only the existing, closed offline publication.
// Expected executable digests refer to the builds reviewed for preparation.
func VerifyFreshRestore(ctx context.Context, req VerifyFreshRestoreRequest) (FreshRestoreManifest, error) {
	if os.Geteuid() != 0 || !apitypes.ManagementID(req.StageID) || !cleanPath(req.StageRoot) || !cleanPath(req.DestinationRoot) ||
		!validRestoreDigest(req.ManagerExecutableSHA256) || !validRestoreDigest(req.WebExecutableSHA256) {
		return FreshRestoreManifest{}, repository.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	web, err := freshRestoreWebOwner()
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	work, err := openRestoreWorkspaceRoot(ctx, req.StageRoot, req.StageID, req.DestinationRoot, web)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer work.close()
	return verifyPublishedRestore(ctx, req, work)
}

func verifyPublishedRestore(ctx context.Context, req VerifyFreshRestoreRequest, work *restoreWorkspace) (FreshRestoreManifest, error) {
	work.published = true
	fd, err := unix.Openat(int(work.root.Fd()), work.final, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	directory := os.NewFile(uintptr(fd), "published-restore")
	defer directory.Close()
	if !work.publishedBody(directory) || !work.separateFromLiveSlots() {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	release, err := work.lockSlots()
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer release()
	manifest, err := readPublishedRestoreManifest(ctx, filepath.Join(work.path(), "manifest.json"))
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	if manifest.Format != 1 || manifest.StageID != req.StageID || manifest.State != "offline-prepared" ||
		manifest.CreatedAt.IsZero() || !manifest.OVSReconciliationRequired ||
		manifest.Manager.ExecutableSHA256 != req.ManagerExecutableSHA256 || manifest.Web.ExecutableSHA256 != req.WebExecutableSHA256 {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	sealed, err := sqlite.InspectStagedRestorePair(ctx, req.StageRoot, req.StageID)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	if manifest.BackupPairID != sealed.Manifest.BackupPair.ID ||
		!work.publishedBody(directory) || !work.slotsUnchanged() || !work.separateFromLiveSlots() {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	before := restoreObservations{manager: sealed.Manager, web: sealed.Web}
	managerPath, webPath := work.database(repository.Manager), work.database(repository.Web)
	var managerFile, webFile unix.Stat_t
	if unix.Lstat(managerPath, &managerFile) != nil || unix.Lstat(webPath, &webFile) != nil {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	after, err := work.inspect(ctx)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	if !preparedObservations(before, after) ||
		manifest.Manager != preparedRestoreSlot(repository.Manager, work.owner(repository.Manager), after.manager, req.ManagerExecutableSHA256) ||
		manifest.Web != preparedRestoreSlot(repository.Web, work.owner(repository.Web), after.web, req.WebExecutableSHA256) ||
		!work.publishedBody(directory) || !work.slotsUnchanged() ||
		!samePublishedImage(managerPath, managerFile) || !samePublishedImage(webPath, webFile) {
		return FreshRestoreManifest{}, restoreFailure(ctx)
	}
	current, err := readPublishedRestoreManifest(ctx, filepath.Join(work.path(), "manifest.json"))
	if err != nil || current != manifest {
		return FreshRestoreManifest{}, restoreFailure(ctx)
	}
	if ctx.Err() != nil {
		return FreshRestoreManifest{}, repository.ErrCanceled
	}
	return manifest, nil
}

func (w *restoreWorkspace) publishedBody(directory *os.File) bool {
	if !w.rootUnchanged() {
		return false
	}
	var pinned, current unix.Stat_t
	if unix.Fstat(int(directory.Fd()), &pinned) != nil || unix.Lstat(w.path(), &current) != nil ||
		pinned.Mode != unix.S_IFDIR|0710 || pinned.Uid != 0 || pinned.Gid != w.web.gid ||
		pinned.Dev != current.Dev || pinned.Ino != current.Ino || pinned.Mode != current.Mode ||
		pinned.Uid != current.Uid || pinned.Gid != current.Gid {
		return false
	}
	if _, err := directory.Seek(0, io.SeekStart); err != nil {
		return false
	}
	names, err := directory.Readdirnames(4)
	slices.Sort(names)
	return (err == nil || errors.Is(err, io.EOF)) && slices.Equal(names, []string{"manager", "manifest.json", "web"})
}

func (w *restoreWorkspace) separateFromLiveSlots() bool {
	for _, kind := range []repository.Kind{repository.Manager, repository.Web} {
		prepared := w.database(kind)
		canonical := filepath.Join("/var/lib/ovs-webui", string(kind), string(kind)+".db")
		for _, paths := range [][2]string{{prepared, canonical}, {filepath.Dir(prepared), filepath.Dir(canonical)}} {
			selected, err := os.Lstat(paths[0])
			if err != nil {
				return false
			}
			live, err := os.Stat(paths[1])
			if (err != nil && !errors.Is(err, os.ErrNotExist)) || (err == nil && os.SameFile(selected, live)) {
				return false
			}
		}
	}
	return true
}

func samePublishedImage(path string, before unix.Stat_t) bool {
	var after unix.Stat_t
	return unix.Lstat(path, &after) == nil && before.Dev == after.Dev && before.Ino == after.Ino &&
		before.Size == after.Size && before.Ctim == after.Ctim
}

func readPublishedRestoreManifest(ctx context.Context, path string) (FreshRestoreManifest, error) {
	file, err := openRestoreFile(path, 0, false)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer file.Close()
	var before unix.Stat_t
	const maxManifest = 4096
	if unix.Fstat(int(file.Fd()), &before) != nil || before.Size <= 0 || before.Size > maxManifest {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(restoreReader{ctx, file}, maxManifest+1))
	var manifest FreshRestoreManifest
	if err != nil || int64(len(data)) != before.Size || json.Unmarshal(data, &manifest) != nil || !samePublishedImage(path, before) {
		return FreshRestoreManifest{}, restoreFailure(ctx)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(data, append(encoded, '\n')) {
		return FreshRestoreManifest{}, repository.ErrUnavailable
	}
	return manifest, nil
}
