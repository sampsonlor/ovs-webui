package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

// PublicHandler composes the versioned transport with bootstrap probes. Domain
// operations remain denied until mgrd authentication and services are wired.
func PublicHandler(ctx context.Context, manager ManagerProbe, storage func(context.Context) repository.Status, origin string, authentication ...*Authentication) (http.Handler, func(), error) {
	options := publicapi.Options{Context: ctx, PublicOrigin: origin, Runtime: func(ctx context.Context) (any, error) {
		health, err := manager.Probe(ctx)
		if err != nil {
			code := "MANAGER_UNAVAILABLE"
			var remote *ipc.RemoteError
			if errors.As(err, &remote) && remote.Code == "IPC_VERSION_MISMATCH" {
				code = remote.Code
			}
			return nil, apitypes.Fail(503, code)
		}
		if len(authentication) == 0 || authentication[0] == nil {
			health.AuthenticationReady = false
		}
		if storage != nil {
			status := storage(ctx)
			if health.Storage == nil {
				health.Storage = &ipc.StorageHealth{}
			}
			health.Storage.Web = &status
			if !status.Writable {
				health.State = "degraded"
			}
		}
		if health.State != "ready" {
			p := apitypes.Fail(503, "STORAGE_DEGRADED")
			p.Details["runtime"] = health
			return nil, p
		}
		return health, nil
	}}
	if len(authentication) > 0 && authentication[0] != nil {
		options.Authorizer = authentication[0]
		options.Gateway = authentication[0]
	}
	api, err := publicapi.New(options)
	if err != nil {
		return nil, nil, err
	}
	bootstrap := BootstrapHandler(manager, storage)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
		} else {
			bootstrap.ServeHTTP(w, r)
		}
	}), api.Close, nil
}
