package backupop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const dedicatedManagerBinary = "/usr/libexec/ovs-mgrd"
const freshWebAccount = "ovs-webui-web"

type FreshRestoreRequest struct {
	StageRoot, StageID, DestinationRoot          string
	ManagerExecutableSHA256, WebExecutableSHA256 string
	ManagerAuthKey, ManagerSecretKeys            string
	WebSessionKey, WebSessionKeys, WebTLSKeys    string
	WebTrustAnchors, PublicOrigin                string
}

type PreparedRestoreSlot struct {
	DatabaseID       string `json:"database_id"`
	File             string `json:"file"`
	SHA256           string `json:"sha256"`
	SchemaVersion    int    `json:"schema_version"`
	ExecutableSHA256 string `json:"executable_sha256"`
	UID              uint32 `json:"uid"`
	GID              uint32 `json:"gid"`
}

type FreshRestoreManifest struct {
	Format                    int                 `json:"format"`
	StageID                   string              `json:"stage_id"`
	BackupPairID              string              `json:"backup_pair_id"`
	State                     string              `json:"state"`
	CreatedAt                 time.Time           `json:"created_at"`
	Manager                   PreparedRestoreSlot `json:"manager"`
	Web                       PreparedRestoreSlot `json:"web"`
	OVSReconciliationRequired bool                `json:"ovs_reconciliation_required"`
}

// PrepareFreshRestore creates one fresh offline destination in a dedicated
// existing output root. It never opens a canonical live database, installs
// keys, invokes systemd, or activates the restored state. The recorded binary
// digests come from independent operator review and the exact executed FDs.
func PrepareFreshRestore(ctx context.Context, req FreshRestoreRequest) (FreshRestoreManifest, error) {
	if os.Geteuid() != 0 || !validFreshRequest(req) {
		return FreshRestoreManifest{}, repository.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	web, err := freshRestoreWebOwner()
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	managerBin, err := openRestoreExecutable(ctx, dedicatedManagerBinary, req.ManagerExecutableSHA256)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer managerBin.Close()
	webBin, err := openRestoreExecutable(ctx, DedicatedWebBinary, req.WebExecutableSHA256)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer webBin.Close()
	work, err := openRestoreWorkspace(ctx, req.StageRoot, req.StageID, req.DestinationRoot, web)
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer work.close()
	return prepareFreshWorkspace(ctx, req, work, managerBin, webBin)
}

func freshRestoreWebOwner() (restoreOwner, error) {
	account, err := user.Lookup(freshWebAccount)
	if err != nil || account.Username != freshWebAccount {
		return restoreOwner{}, repository.ErrUnavailable
	}
	uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	if uidErr != nil || gidErr != nil || uid == 0 || gid == 0 {
		return restoreOwner{}, repository.ErrUnavailable
	}
	return restoreOwner{uint32(uid), uint32(gid)}, nil
}

type restoreObservations struct{ manager, web sqlite.RestoreCopyObservation }

func prepareFreshWorkspace(ctx context.Context, req FreshRestoreRequest, work *restoreWorkspace, managerBin, webBin *os.File) (FreshRestoreManifest, error) {
	before, err := work.inspectClosed(ctx)
	if err != nil || before.manager.UnsettledNative || !matchesOriginal(before.manager, work.stage.BackupPair.Manager.BackupManifest) ||
		!matchesOriginal(before.web, work.stage.BackupPair.Web.BackupManifest) {
		return FreshRestoreManifest{}, restoreFailure(ctx)
	}
	managerArgs := []string{"--database", work.database(repository.Manager), "--auth-key-file", req.ManagerAuthKey}
	managerArgs = addRestoreOption(managerArgs, "--secret-key-directory", req.ManagerSecretKeys)
	webArgs := []string{"--database", work.database(repository.Web), "--public-origin", req.PublicOrigin}
	webArgs = addRestoreOption(webArgs, "--session-key-file", req.WebSessionKey)
	webArgs = addRestoreOption(webArgs, "--session-key-directory", req.WebSessionKeys)
	webArgs = addRestoreOption(webArgs, "--tls-key-directory", req.WebTLSKeys)
	webArgs = addRestoreOption(webArgs, "--tls-trust-file", req.WebTrustAnchors)
	var certificate []byte
	if before.web.TLSStateSHA256 != "" {
		if req.WebTLSKeys == "" {
			return FreshRestoreManifest{}, repository.ErrUnavailable
		}
		certificate, err = runRestoreOwner(ctx, webBin, work.web, append(webArgs, "--print-tls-certificate")...)
		if err != nil || !bytes.HasPrefix(certificate, []byte("-----BEGIN CERTIFICATE-----\n")) {
			return FreshRestoreManifest{}, restoreFailure(ctx)
		}
	}
	if _, err := runRestoreOwner(ctx, managerBin, restoreOwner{}, append(managerArgs, "--prepare-restore-security")...); err != nil {
		return FreshRestoreManifest{}, err
	}
	if _, err := runRestoreOwner(ctx, webBin, work.web, append(webArgs, "--prepare-restore-security")...); err != nil {
		return FreshRestoreManifest{}, err
	}
	if len(certificate) != 0 {
		current, err := runRestoreOwner(ctx, webBin, work.web, append(webArgs, "--print-tls-certificate")...)
		if err != nil || !bytes.Equal(current, certificate) {
			return FreshRestoreManifest{}, restoreFailure(ctx)
		}
	}
	release, err := work.lockSlots()
	if err != nil {
		return FreshRestoreManifest{}, err
	}
	defer release()
	after, err := work.inspect(ctx)
	if err != nil || !preparedObservations(before, after) {
		return FreshRestoreManifest{}, restoreFailure(ctx)
	}
	manifest := FreshRestoreManifest{
		Format: 1, StageID: req.StageID, BackupPairID: work.stage.BackupPair.ID, CreatedAt: time.Now().UTC(),
		State: "offline-prepared", OVSReconciliationRequired: true,
		Manager: preparedRestoreSlot(repository.Manager, work.owner(repository.Manager), after.manager, req.ManagerExecutableSHA256),
		Web:     preparedRestoreSlot(repository.Web, work.owner(repository.Web), after.web, req.WebExecutableSHA256),
	}
	if err := work.writePreparedManifest(manifest); err != nil {
		return FreshRestoreManifest{}, err
	}
	if _, err := work.publish(ctx); err != nil {
		if work.published {
			return manifest, err
		}
		return FreshRestoreManifest{}, err
	}
	return manifest, nil
}

func (w *restoreWorkspace) inspectClosed(ctx context.Context) (restoreObservations, error) {
	release, err := w.lockSlots()
	if err != nil {
		return restoreObservations{}, err
	}
	defer release()
	return w.inspect(ctx)
}

func (w *restoreWorkspace) inspect(ctx context.Context) (restoreObservations, error) {
	manager, err := sqlite.InspectRestoreCopy(ctx, w.database(repository.Manager), repository.Manager, 0)
	if err != nil {
		return restoreObservations{}, err
	}
	web, err := sqlite.InspectRestoreCopy(ctx, w.database(repository.Web), repository.Web, w.web.uid)
	return restoreObservations{manager, web}, err
}

func matchesOriginal(o sqlite.RestoreCopyObservation, original sqlite.BackupManifest) bool {
	return o.DatabaseID == original.DatabaseID && o.Schema == original.SchemaVersion && o.SHA256 == original.SHA256
}

func preparedObservations(before, after restoreObservations) bool {
	for _, pair := range [][2]sqlite.RestoreCopyObservation{{before.manager, after.manager}, {before.web, after.web}} {
		original, prepared := pair[0], pair[1]
		if !apitypes.ManagementID(original.DatabaseID) || !apitypes.ManagementID(original.APIEpoch) || !apitypes.ManagementID(prepared.APIEpoch) ||
			!validRestoreDigest(original.SHA256) || !validRestoreDigest(prepared.SHA256) ||
			prepared.DatabaseID != original.DatabaseID || prepared.Schema != original.Schema || !prepared.SecurityReset ||
			prepared.APIEpoch == original.APIEpoch || prepared.SHA256 == original.SHA256 {
			return false
		}
	}
	return apitypes.ManagementID(before.manager.AuthEpoch) && apitypes.ManagementID(after.manager.AuthEpoch) &&
		before.manager.AuthEpoch != after.manager.AuthEpoch && after.manager.ReconciliationRequired &&
		!before.manager.UnsettledNative && !after.manager.UnsettledNative &&
		after.manager.RestoreAuditCount == before.manager.RestoreAuditCount+1 &&
		(before.web.TLSStateSHA256 == "" && after.web.TLSStateSHA256 == "" ||
			before.web.TLSStateSHA256 != "" && after.web.TLSStateSHA256 != "" && before.web.TLSStateSHA256 != after.web.TLSStateSHA256)
}

func preparedRestoreSlot(kind repository.Kind, owner restoreOwner, o sqlite.RestoreCopyObservation, executable string) PreparedRestoreSlot {
	return PreparedRestoreSlot{DatabaseID: o.DatabaseID, File: string(kind) + "/" + string(kind) + ".db", SHA256: o.SHA256,
		SchemaVersion: o.Schema, ExecutableSHA256: executable, UID: owner.uid, GID: owner.gid}
}

func (w *restoreWorkspace) writePreparedManifest(manifest FreshRestoreManifest) error {
	for _, kind := range []repository.Kind{repository.Manager, repository.Web} {
		path := w.database(kind)
		image, err := openRestoreFile(path, w.owner(kind).uid, false)
		if err != nil {
			return err
		}
		err = image.Sync()
		closeErr := image.Close()
		if err != nil || closeErr != nil || syncRestoreDirectory(filepath.Dir(path)) != nil {
			return repository.ErrUnavailable
		}
	}
	file, err := os.OpenFile(filepath.Join(w.path(), "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return repository.ErrUnavailable
	}
	err = json.NewEncoder(file).Encode(manifest)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return repository.ErrUnavailable
	}
	return nil
}

func validFreshRequest(r FreshRestoreRequest) bool {
	if !cleanPath(r.StageRoot) || !cleanPath(r.DestinationRoot) || !apitypes.ManagementID(r.StageID) ||
		!validRestoreDigest(r.ManagerExecutableSHA256) || !validRestoreDigest(r.WebExecutableSHA256) ||
		!cleanPath(r.ManagerAuthKey) || (r.WebSessionKey == "") == (r.WebSessionKeys == "") {
		return false
	}
	for _, path := range []string{r.ManagerAuthKey, r.ManagerSecretKeys, r.WebSessionKey, r.WebSessionKeys, r.WebTLSKeys, r.WebTrustAnchors} {
		if path != "" && (!cleanPath(path) || overlapRestorePaths(r.StageRoot, path) || overlapRestorePaths(r.DestinationRoot, path)) {
			return false
		}
	}
	origin, err := url.Parse(r.PublicOrigin)
	return err == nil && len(r.PublicOrigin) <= 256 && origin.Scheme == "https" && origin.Hostname() != "" &&
		origin.User == nil && origin.Opaque == "" && origin.EscapedPath() == "" && origin.RawQuery == "" &&
		!origin.ForceQuery && origin.Fragment == "" && origin.String() == r.PublicOrigin
}

func validRestoreDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil && hex.EncodeToString(decoded) == value
}

func addRestoreOption(args []string, option, value string) []string {
	if value != "" {
		return append(args, option, value)
	}
	return args
}

func openRestoreExecutable(ctx context.Context, path, expected string) (*os.File, error) {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || !rootImmutable(info) {
			return nil, repository.ErrUnavailable
		}
		if parent == "/" {
			break
		}
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 || !rootImmutable(info) ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || info.Size() <= 0 || info.Size() > 128<<20 {
		_ = file.Close()
		return nil, repository.ErrUnavailable
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(restoreReader{ctx, file}, info.Size()+1))
	if err != nil || n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != expected {
		_ = file.Close()
		return nil, restoreFailure(ctx)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, repository.ErrUnavailable
	}
	return file, nil
}

type ownerReport struct{ data bytes.Buffer }

func (r *ownerReport) Write(p []byte) (int, error) {
	if r.data.Len()+len(p) > 256<<10 {
		return 0, repository.ErrUnavailable
	}
	return r.data.Write(p)
}

func runRestoreOwner(ctx context.Context, binary *os.File, owner restoreOwner, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	cmd.Args[0] = binary.Name()
	cmd.ExtraFiles = []*os.File{binary}
	cmd.Dir, cmd.Env = "/", []string{"LANG=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.WaitDelay = time.Second
	if owner.uid != 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: owner.uid, Gid: owner.gid}}
	}
	var output ownerReport
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, restoreFailure(ctx)
	}
	return output.data.Bytes(), nil
}

func restoreFailure(ctx context.Context) error {
	if ctx.Err() != nil {
		return repository.ErrCanceled
	}
	return repository.ErrUnavailable
}
