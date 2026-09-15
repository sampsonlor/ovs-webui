//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	ovsprovider "github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/redact"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/secrets"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/runtimehost"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

func main() { os.Exit(run()) }
func run() int {
	socket := flag.String("socket", "/run/ovs-webui/mgrd.sock", "Root-owned Unix socket path")
	uid := flag.Uint("webd-uid", 0, "Required fixed non-root ovs-webd UID")
	gid := flag.Uint("webd-gid", 0, "Required ovs-webd socket group GID")
	version := flag.Bool("version", false, "Print build version")
	database := flag.String("database", "/var/lib/ovs-webui/manager/manager.db", "Private manager database path")
	initialize := flag.Bool("init-database", false, "Explicitly initialize a new database and exit; never overwrite")
	authKeyPath := flag.String("auth-key-file", "", "Private authentication key (default: auth.key beside manager.db)")
	initAuthKey := flag.Bool("init-auth-key", false, "Explicitly create a new authentication key and exit; never overwrite")
	bootstrap := flag.String("bootstrap-admin", "", "Initialize first local administrator; read password from stdin, then exit")
	secretDir := flag.String("secret-key-directory", "", "Private versioned privileged keys (default: secret-keys beside manager.db)")
	initSecret := flag.Bool("init-secret-store", false, "Explicitly initialize privileged SecretStore keys and exit")
	rotateSecret := flag.Bool("rotate-secret-key", false, "Offline privileged key rotation, retaining old versions")
	restore := flag.Bool("prepare-restore-security", false, "Offline: rotate restored auth/request epochs and revoke all grants/tokens; run with webd reconciliation")
	ovsSocket := flag.String("ovsdb-socket", "/run/openvswitch/db.sock", "Read-only local OVSDB Unix socket")
	ovsFile := flag.String("ovsdb-file", "/var/lib/openvswitch/conf.db", "Canonical database file for lifecycle evidence (no symlinks)")
	ovsUID := flag.Uint("ovsdb-peer-uid", 0, "Required OVSDB Unix peer UID")
	acceptEvidence := flag.String("reconcile-ovsdb", "", "Offline: accept an exact reviewed inventory evidence digest, assign a new generation, then exit")
	acceptReason := flag.String("reconciliation-reason", "", "Administrative reason for offline identity reconciliation")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.SoftwareVersion())
		return 0
	}
	logger := slog.New(redact.New(slog.NewJSONHandler(os.Stderr, nil))).With("service", "ovs-mgrd")
	actions := 0
	for _, enabled := range []bool{*initialize, *initAuthKey, *bootstrap != "", *initSecret, *rotateSecret, *restore, *acceptEvidence != ""} {
		if enabled {
			actions++
		}
	}
	if flag.NArg() != 0 || os.Geteuid() != 0 || actions > 1 || (actions == 0 && (*uid == 0 || *gid == 0 || uint64(*uid) >= 1<<32-1 || uint64(*gid) >= 1<<32-1)) {
		logger.Error("invalid_process_configuration", "code", "ROOT_AND_FIXED_WEBD_ID_REQUIRED")
		return 2
	}
	syscall.Umask(0077)
	if *authKeyPath == "" {
		*authKeyPath = filepath.Join(filepath.Dir(*database), "auth.key")
	}
	if *secretDir == "" {
		*secretDir = filepath.Join(filepath.Dir(*database), "secret-keys")
	}
	if *initAuthKey {
		if _, err := authn.KeyFile(*authKeyPath, true); err != nil {
			logger.Error("auth_key_initialization_failed", "code", "AUTH_KEY_UNAVAILABLE")
			return 1
		}
		logger.Info("auth_key_initialized")
		return 0
	}
	options := sqlite.Options{Path: *database, Kind: repository.Manager, SoftwareVersion: buildinfo.SoftwareVersion()}
	if *initialize {
		if err := sqlite.Initialize(context.Background(), options); err != nil {
			logger.Error("database_initialization_failed", "code", "STORAGE_INITIALIZATION_FAILED")
			return 1
		}
		logger.Info("database_initialized")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	store, storageErr := sqlite.Open(ctx, options)
	defer store.Close()
	if storageErr != nil {
		logger.Error("storage_degraded", "code", store.Status().Code)
	}
	if *initSecret {
		if storageErr != nil {
			logger.Error("security_action_failed", "code", "STORAGE_UNAVAILABLE")
			return 1
		}
		if _, err := secret.InitializeKeys(*secretDir, nil); err != nil {
			logger.Error("security_action_failed", "code", "SECRET_KEY_UNAVAILABLE")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	var privileged *secrets.Store
	if _, err := os.Lstat(*secretDir); err == nil {
		keys, e := secret.LoadKeys(*secretDir)
		if e == nil && storageErr == nil {
			privileged, e = secrets.New(ctx, store, secret.Privileged, keys)
		}
		if e != nil || privileged == nil {
			logger.Error("service_start_failed", "code", "SECRET_KEY_UNAVAILABLE")
			return 1
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		logger.Error("service_start_failed", "code", "SECRET_KEY_UNAVAILABLE")
		return 1
	}
	if *rotateSecret {
		if privileged == nil {
			logger.Error("security_action_failed", "code", "SECRET_KEY_UNAVAILABLE")
			return 1
		}
		next, err := secret.PrepareKey(*secretDir)
		if err == nil {
			err = privileged.Rewrap(ctx, next)
		}
		if err == nil {
			err = secret.ActivateKeys(*secretDir, next.Active)
		}
		if err != nil {
			logger.Error("security_action_failed", "code", "SECRET_ROTATION_FAILED")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	var authentication *auth.Repository
	if key, err := authn.KeyFile(*authKeyPath, false); err == nil && storageErr == nil {
		authentication, err = auth.New(store, key)
		if err != nil {
			logger.Error("authentication_degraded", "code", "AUTH_UNAVAILABLE")
		}
	} else {
		logger.Warn("authentication_degraded", "code", "AUTH_KEY_UNAVAILABLE")
	}
	if *bootstrap != "" {
		if authentication == nil {
			logger.Error("bootstrap_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1027))
		if err != nil || len(data) > 1026 {
			logger.Error("bootstrap_failed", "code", "PASSWORD_INPUT_INVALID")
			return 1
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if err = authentication.Bootstrap(ctx, *bootstrap, password); err != nil {
			logger.Error("bootstrap_failed", "code", auth.ErrorCode(err))
			return 1
		}
		logger.Info("local_administrator_initialized")
		return 0
	}
	if *restore {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		if privileged != nil {
			if err := privileged.Rewrap(ctx, privileged.Ring); err != nil {
				logger.Error("security_action_failed", "code", "SECRET_KEY_UNAVAILABLE")
				return 1
			}
		}
		if err := authentication.PrepareRestore(ctx); err != nil {
			logger.Error("security_action_failed", "code", "RESTORE_SECURITY_FAILED")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	go store.Maintain(ctx)
	// Reconcile the persisted lease before accepting reads or advertising ready.
	// A one-second maintenance tick must not expose an expired trial as current
	// immediately after a manager restart.
	if authentication != nil {
		if _, err := authentication.TLSState(ctx); err != nil {
			logger.Error("service_start_failed", "code", "TLS_RECOVERY_UNAVAILABLE")
			return 1
		}
	}
	var inventoryService *inventory.Service
	if storageErr == nil {
		reg, err := registry.New(store)
		if err != nil {
			logger.Error("inventory_start_failed", "code", "INVENTORY_STORAGE_UNAVAILABLE")
			return 1
		}
		inventoryService = inventory.New(reg)
		if *ovsUID >= 1<<32-1 {
			logger.Error("inventory_start_failed", "code", "OVSDB_PEER_INVALID")
			return 2
		}
		provider, err := ovsprovider.New(ovsprovider.Options{Socket: *ovsSocket, DatabaseFile: *ovsFile, PeerUID: uint32(*ovsUID)})
		if err != nil {
			logger.Error("inventory_start_failed", "code", "OVSDB_ENDPOINT_INVALID")
			return 2
		}
		providerCtx, cancelProvider := context.WithCancel(ctx)
		defer cancelProvider()
		providerDone := make(chan struct{})
		go func() { defer close(providerDone); provider.Run(providerCtx, inventoryService) }()
		if *acceptEvidence != "" {
			deadline := time.NewTimer(8 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for !inventoryService.Observed() {
				select {
				case <-ctx.Done():
					return 1
				case <-deadline.C:
					logger.Error("reconciliation_failed", "code", "OVSDB_NOT_OBSERVED")
					return 1
				case <-tick.C:
				}
			}
			cancelProvider()
			<-providerDone
			if err = inventoryService.Accept(ctx, *acceptEvidence, *acceptReason); err != nil {
				logger.Error("reconciliation_failed", "code", auth.ErrorCode(err))
				return 1
			}
			logger.Info("inventory_reconciliation_completed", "configuration_written", false)
			return 0
		}
		if authentication != nil {
			authentication.WithInventory(inventoryService)
		}
	} else if *acceptEvidence != "" {
		logger.Error("reconciliation_failed", "code", "INVENTORY_STORAGE_UNAVAILABLE")
		return 1
	}
	listener, err := ipc.ListenUnix(ipc.SocketOptions{Path: *socket, OwnerUID: 0, GroupGID: uint32(*gid), PeerUID: uint32(*uid)})
	if err != nil {
		logger.Error("service_start_failed", "code", "IPC_LISTENER_UNAVAILABLE")
		return 1
	}
	defer listener.Release()
	var authorizer ipc.Authorizer
	var authService authn.Manager
	if authentication != nil {
		authorizer = authentication
		authService = authentication
	}
	handler := ipc.NewHandler(ipc.CurrentProtocol(buildinfo.SoftwareVersion()), authorizer, logger, func(ctx context.Context) ipc.Health {
		health := ipc.BootstrapHealth()
		status := store.Probe(ctx)
		health.Storage = &ipc.StorageHealth{Manager: &status}
		health.AuthenticationReady = authentication != nil && authentication.Ready(ctx)
		if !status.Writable {
			health.State = "degraded"
		}
		return health
	}).WithAuthentication(authService)
	if authentication != nil {
		handler.WithTLS(authentication)
		go authentication.MaintainTLS(ctx)
	}
	server := ipc.HTTPServer(handler)
	logger.Info("service_started", "scope", "runtime-bootstrap", "configuration_ready", false)
	if err := runtimehost.Run(ctx, server, func() error { return server.Serve(listener) }, logger); err != nil {
		logger.Error("service_stopped", "code", "HTTP_SERVE_FAILED")
		return 1
	}
	return 0
}
