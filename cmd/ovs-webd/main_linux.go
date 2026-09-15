//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"errors"
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
	"github.com/sampsonlor/ovs-webui/internal/redact"
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
	cert := flag.String("tls-cert", "", "Explicit legacy static TLS certificate file")
	key := flag.String("tls-key", "", "Explicit legacy static TLS private key file")
	origin := flag.String("public-origin", "", "Canonical HTTPS origin for browser writes and WebSocket access")
	version := flag.Bool("version", false, "Print build version")
	database := flag.String("database", "/var/lib/ovs-webui/web/web.db", "Private web database path")
	initialize := flag.Bool("init-database", false, "Explicitly initialize a new database and exit; never overwrite")
	sessionKeyPath := flag.String("session-key-file", "", "Private session key (default: session.key beside web.db)")
	initSessionKey := flag.Bool("init-session-key", false, "Explicitly create a new session key and exit; never overwrite")
	tlsDir := flag.String("tls-key-directory", "", "Private versioned TLS keys (default: tls-keys beside web.db)")
	sessionDir := flag.String("session-key-directory", "", "Private versioned session keys (default: session-keys beside web.db)")
	tlsRoots := flag.String("tls-trust-file", "", "Private owner-only PEM trust anchors for imported server certificates (default: system roots)")
	bootstrapTLS := flag.Bool("bootstrap-tls", false, "Explicitly create encrypted bootstrap HTTPS identity and exit")
	printCertificate := flag.Bool("print-tls-certificate", false, "Print only the current managed public certificate and exit")
	rotate := flag.String("rotate-secret-key", "", "Offline key rotation: tls or session; retains old key versions")
	restore := flag.Bool("prepare-restore-security", false, "Offline: revoke restored browser contexts and discard pending TLS trial; run with mgrd reconciliation")
	proxyPeers := flag.String("trusted-proxy-cidrs", "", "Comma-separated immediate trusted proxy CIDRs")
	proxyHeaders := flag.String("trusted-proxy-headers", "", "Explicit allowlist: X-Forwarded-Proto,X-Forwarded-Host")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.SoftwareVersion())
		return 0
	}
	logger := slog.New(redact.New(slog.NewJSONHandler(os.Stderr, nil))).With("service", "ovs-webd")
	actions := 0
	for _, enabled := range []bool{*initialize, *initSessionKey, *bootstrapTLS, *printCertificate, *restore, *rotate != ""} {
		if enabled {
			actions++
		}
	}
	if flag.NArg() != 0 || os.Geteuid() == 0 || actions > 1 || (*rotate != "" && *rotate != "tls" && *rotate != "session") {
		logger.Error("invalid_process_configuration", "code", "NONROOT_AND_TLS_REQUIRED")
		return 2
	}
	syscall.Umask(0077)
	if *sessionKeyPath == "" {
		*sessionKeyPath = filepath.Join(filepath.Dir(*database), "session.key")
	}
	if *tlsDir == "" {
		*tlsDir = defaultDirectory(*database, "tls-keys")
	}
	if *sessionDir == "" {
		*sessionDir = defaultDirectory(*database, "session-keys")
	}
	security := securityOptions{*origin, *tlsDir, *sessionDir, *sessionKeyPath, *tlsRoots}
	proxy, err := web.NewProxyPolicy(*origin, *proxyPeers, *proxyHeaders)
	if err != nil {
		logger.Error("invalid_process_configuration", "code", "PROXY_CONFIGURATION_INVALID")
		return 2
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	store, storageErr := sqlite.Open(ctx, options)
	defer store.Close()
	if storageErr != nil {
		logger.Error("storage_degraded", "code", store.Status().Code)
	}
	if actions > 0 {
		if storageErr != nil {
			logger.Error("security_action_failed", "code", "STORAGE_UNAVAILABLE")
			return 1
		}
		if err = security.offline(ctx, store, *bootstrapTLS, *printCertificate, *restore, *rotate); err != nil {
			logger.Error("security_action_failed", "code", "SECURITY_MATERIAL_UNAVAILABLE")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	go store.Maintain(ctx)
	client := ipc.NewClient(*socket, ipc.CurrentProtocol(buildinfo.SoftwareVersion()))
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, SessionTicketsDisabled: true}
	var managed *web.ManagedTLS
	if _, err = os.Lstat(*tlsDir); err == nil {
		if storageErr != nil {
			logger.Error("service_start_failed", "code", "TLS_STORE_UNAVAILABLE")
			return 1
		}
		certs, e := security.certificates(ctx, store, false)
		if e == nil {
			managed, e = web.NewManagedTLS(ctx, certs, client)
		}
		if e != nil {
			logger.Error("service_start_failed", "code", "TLS_IDENTITY_UNAVAILABLE")
			return 1
		}
		tlsConfig.GetCertificate = managed.GetCertificate
		go managed.Run(ctx)
	} else if !errors.Is(err, os.ErrNotExist) {
		logger.Error("service_start_failed", "code", "TLS_KEY_UNAVAILABLE")
		return 1
	} else {
		info, e := os.Lstat(*key)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 {
			logger.Error("service_start_failed", "code", "TLS_KEY_PERMISSIONS")
			return 2
		}
		pair, e := tls.LoadX509KeyPair(*cert, *key)
		if e != nil {
			logger.Error("service_start_failed", "code", "TLS_IDENTITY_INVALID")
			return 2
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	var authentication *web.Authentication
	if sessionKeys, keyErr := security.sessionKeys(false); keyErr == nil && storageErr == nil && *origin != "" {
		if sessionStore, err := sessions.NewWithKeys(ctx, store, sessionKeys); err == nil {
			authentication = web.NewAuthentication(client, sessionStore).WithCertificates(managed)
			if workspace, e := web.NewWorkspace(store, client); e == nil {
				authentication.WithWorkspace(workspace)
			}
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
	server := runtimehost.NewHTTPServer(proxy.Handler(handler))
	server.TLSConfig = tlsConfig
	if managed != nil {
		server.ConnContext = managed.ConnContext
	}
	server.Protocols = new(http.Protocols)
	server.Protocols.SetHTTP1(true)
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("service_start_failed", "code", "HTTPS_LISTENER_UNAVAILABLE")
		return 1
	}
	defer listener.Close()
	logger.Info("service_started", "scope", "runtime-bootstrap", "configuration_ready", false)
	if err := runtimehost.Run(ctx, server, func() error { return server.ServeTLS(runtimehost.LimitConnections(listener, 64), "", "") }, logger); err != nil {
		logger.Error("service_stopped", "code", "HTTPS_SERVE_FAILED")
		return 1
	}
	return 0
}
