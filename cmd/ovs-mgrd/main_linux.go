//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/runtimehost"
)

func main() { os.Exit(run()) }
func run() int {
	socket := flag.String("socket", "/run/ovs-webui/mgrd.sock", "Root-owned Unix socket path")
	uid := flag.Uint("webd-uid", 0, "Required fixed non-root ovs-webd UID")
	gid := flag.Uint("webd-gid", 0, "Required ovs-webd socket group GID")
	version := flag.Bool("version", false, "Print build version")
	database := flag.String("database", "/var/lib/ovs-webui/manager/manager.db", "Private manager database path")
	initialize := flag.Bool("init-database", false, "Explicitly initialize a new database and exit; never overwrite")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.SoftwareVersion())
		return 0
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "ovs-mgrd")
	if flag.NArg() != 0 || os.Geteuid() != 0 || (!*initialize && (*uid == 0 || *gid == 0 || uint64(*uid) >= 1<<32-1 || uint64(*gid) >= 1<<32-1)) {
		logger.Error("invalid_process_configuration", "code", "ROOT_AND_FIXED_WEBD_ID_REQUIRED")
		return 2
	}
	syscall.Umask(0077)
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
	go store.Maintain(ctx)
	listener, err := ipc.ListenUnix(ipc.SocketOptions{Path: *socket, OwnerUID: 0, GroupGID: uint32(*gid), PeerUID: uint32(*uid)})
	if err != nil {
		logger.Error("service_start_failed", "code", "IPC_LISTENER_UNAVAILABLE")
		return 1
	}
	defer listener.Release()
	server := ipc.HTTPServer(ipc.NewHandler(ipc.CurrentProtocol(buildinfo.SoftwareVersion()), nil, logger, func(ctx context.Context) ipc.Health {
		health := ipc.BootstrapHealth()
		status := store.Probe(ctx)
		health.Storage = &ipc.StorageHealth{Manager: &status}
		if !status.Writable {
			health.State = "degraded"
		}
		return health
	}))
	logger.Info("service_started", "scope", "runtime-bootstrap", "configuration_ready", false)
	if err := runtimehost.Run(ctx, server, func() error { return server.Serve(listener) }, logger); err != nil {
		logger.Error("service_stopped", "code", "HTTP_SERVE_FAILED")
		return 1
	}
	return 0
}
