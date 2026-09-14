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
	"syscall"

	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/runtimehost"
	"github.com/sampsonlor/ovs-webui/internal/web"
)

func main() { os.Exit(run()) }
func run() int {
	address := flag.String("listen", "127.0.0.1:8443", "HTTPS listen address")
	socket := flag.String("manager-socket", "/run/ovs-webui/mgrd.sock", "Manager Unix socket path")
	cert := flag.String("tls-cert", "", "Required TLS certificate file")
	key := flag.String("tls-key", "", "Required TLS private key file")
	version := flag.Bool("version", false, "Print build version")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.SoftwareVersion())
		return 0
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "ovs-webd")
	if flag.NArg() != 0 || os.Geteuid() == 0 || *cert == "" || *key == "" {
		logger.Error("invalid_process_configuration", "code", "NONROOT_AND_TLS_REQUIRED")
		return 2
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
	client := ipc.NewClient(*socket, ipc.CurrentProtocol(buildinfo.SoftwareVersion()))
	server := runtimehost.NewHTTPServer(web.BootstrapHandler(client))
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}
	server.Protocols = new(http.Protocols)
	server.Protocols.SetHTTP1(true)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	logger.Info("service_started", "scope", "runtime-bootstrap", "configuration_ready", false)
	if err := runtimehost.Run(ctx, server, func() error { return server.ServeTLS(runtimehost.LimitConnections(listener, 64), "", "") }, logger); err != nil {
		logger.Error("service_stopped", "code", "HTTPS_SERVE_FAILED")
		return 1
	}
	return 0
}
