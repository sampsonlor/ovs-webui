//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sessions"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/runtimehost"
	"github.com/sampsonlor/ovs-webui/internal/web"
)

func main() { os.Exit(run()) }
func run() int {
	address := flag.String("listen", "127.0.0.1:8443", "HTTPS listen address")
	socket := flag.String("manager-socket", "/run/ovs-webui/mgrd.sock", "Manager Unix socket path")
	cert := flag.String("tls-cert", "", "Required TLS certificate file")
	key := flag.String("tls-key", "", "Required TLS private key file")
	origin := flag.String("public-origin", "", "Canonical HTTPS origin for browser writes and WebSocket access")
	version := flag.Bool("version", false, "Print build version")
	database := flag.String("database", "/var/lib/ovs-webui/web/web.db", "Private web database path")
	initialize := flag.Bool("init-database", false, "Explicitly initialize a new database and exit; never overwrite")
	sessionKeyPath := flag.String("session-key-file", "", "Private session key (default: session.key beside web.db)")
	initSessionKey := flag.Bool("init-session-key", false, "Explicitly create a new session key and exit; never overwrite")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.SoftwareVersion())
		return 0
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "ovs-webd")
	if flag.NArg() != 0 || os.Geteuid() == 0 || (!*initialize && !*initSessionKey && (*cert == "" || *key == "")) {
		logger.Error("invalid_process_configuration", "code", "NONROOT_AND_TLS_REQUIRED")
		return 2
	}
	syscall.Umask(0077)
	if *sessionKeyPath == "" {
		*sessionKeyPath = filepath.Join(filepath.Dir(*database), "session.key")
	}
	if *initSessionKey {
		if _, err := authn.KeyFile(*sessionKeyPath, true); err != nil {
			logger.Error("session_key_initialization_failed", "code", "AUTH_KEY_UNAVAILABLE")
			return 1
		}
		logger.Info("session_key_initialized")
		return 0
	}
	options := sqlite.Options{Path: *database, Kind: repository.Web, SoftwareVersion: buildinfo.SoftwareVersion()}
	if *initialize {
		if err := sqlite.Initialize(context.Background(), options); err != nil {
			logger.Error("database_initialization_failed", "code", "STORAGE_INITIALIZATION_FAILED")
			return 1
		}
		logger.Info("database_initialized")
		return 0
	}
	info, err := os.Lstat(*key)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 {
		logger.Error("service_start_failed", "code", "TLS_KEY_PERMISSIONS")
		return 2
	}
	pair, err := tls.LoadX509KeyPair(*cert, *key)
	if err != nil {
		logger.Error("service_start_failed", "code", "TLS_IDENTITY_INVALID")
		return 2
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("service_start_failed", "code", "HTTPS_LISTENER_UNAVAILABLE")
		return 1
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	store, storageErr := sqlite.Open(ctx, options)
	defer store.Close()
	if storageErr != nil {
		logger.Error("storage_degraded", "code", store.Status().Code)
	}
	go store.Maintain(ctx)
	client := ipc.NewClient(*socket, ipc.CurrentProtocol(buildinfo.SoftwareVersion()))
	var authentication *web.Authentication
	if sessionKey, keyErr := authn.KeyFile(*sessionKeyPath, false); keyErr == nil && storageErr == nil && *origin != "" {
		if sessionStore, err := sessions.New(ctx, store, sessionKey); err == nil {
			authentication = web.NewAuthentication(client, sessionStore)
		}
	}
	if authentication == nil {
		logger.Warn("authentication_degraded", "code", "SESSION_AUTH_UNAVAILABLE")
	}
	handler, closeAPI, err := web.PublicHandler(ctx, client, store.Probe, *origin, authentication)
	if err != nil {
		logger.Error("invalid_public_api_configuration", "code", "API_CONFIGURATION_INVALID")
		return 2
	}
	defer closeAPI()
	server := runtimehost.NewHTTPServer(handler)
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}
	server.Protocols = new(http.Protocols)
	server.Protocols.SetHTTP1(true)
	logger.Info("service_started", "scope", "runtime-bootstrap", "configuration_ready", false)
	if err := runtimehost.Run(ctx, server, func() error { return server.ServeTLS(runtimehost.LimitConnections(listener, 64), "", "") }, logger); err != nil {
		logger.Error("service_stopped", "code", "HTTPS_SERVE_FAILED")
		return 1
	}
	return 0
}
