package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type probeFunc func(context.Context) (ipc.Health, error)

func (f probeFunc) Probe(ctx context.Context) (ipc.Health, error) { return f(ctx) }

func TestBootstrapDoesNotInventProductReadinessOrExposeErrors(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		h := BootstrapHandler(probeFunc(func(context.Context) (ipc.Health, error) {
			if unavailable {
				return ipc.Health{}, errors.New("SECRET /private/manager.sock")
			}
			return ipc.BootstrapHealth(), nil
		}))
		for _, path := range []string{"/", "/healthz", "/readyz", "/api/v1/runtime", "/api/v1/transactions", "/api/v1/sessions"} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			if strings.Contains(response.Body.String(), "SECRET") {
				t.Fatal("leaked private failure")
			}
			if path == "/api/v1/transactions" || path == "/api/v1/sessions" {
				if response.Code != 404 {
					t.Fatal("unimplemented API exposed")
				}
				continue
			}
			if path == "/readyz" || path == "/api/v1/runtime" {
				if unavailable && response.Code != 503 {
					t.Fatal("claimed unavailable manager ready")
				}
				if !unavailable && (!strings.Contains(response.Body.String(), `"configuration_ready":false`) || !strings.Contains(response.Body.String(), `"authentication_ready":false`)) {
					t.Fatal("invented product capability")
				}
			} else if response.Code != 200 {
				t.Fatal("manager failure took down independent web liveness")
			}
		}
	}
}

func TestBootstrapExplainsVersionMismatch(t *testing.T) {
	h := BootstrapHandler(probeFunc(func(context.Context) (ipc.Health, error) {
		return ipc.Health{}, &ipc.RemoteError{Code: "IPC_VERSION_MISMATCH", Status: 409}
	}))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "IPC_VERSION_MISMATCH") {
		t.Fatal("version mismatch lost its recovery reason")
	}
}

func TestStorageDegradationIsVisibleWithoutInventingReadiness(t *testing.T) {
	for _, component := range []string{"manager", "web"} {
		t.Run(component, func(t *testing.T) {
			h := BootstrapHandler(probeFunc(func(context.Context) (ipc.Health, error) {
				health := ipc.BootstrapHealth()
				status := repository.Status{State: "ready", Readable: true, Writable: true, SchemaVersion: 2}
				if component == "manager" {
					status = repository.Status{State: "degraded", Code: "STORAGE_MISSING"}
					health.State = "degraded"
				}
				health.Storage = &ipc.StorageHealth{Manager: &status}
				return health, nil
			}), func(context.Context) repository.Status {
				if component == "web" {
					return repository.Status{State: "degraded", Code: "STORAGE_INTEGRITY_FAILED"}
				}
				return repository.Status{State: "ready", Readable: true, Writable: true, SchemaVersion: 2}
			})
			for _, path := range []string{"/readyz", "/healthz"} {
				response := httptest.NewRecorder()
				h.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
				if path == "/healthz" {
					if response.Code != 200 {
						t.Fatal("storage failure killed web liveness")
					}
				} else if response.Code != 503 || !strings.Contains(response.Body.String(), "STORAGE_") || !strings.Contains(response.Body.String(), `"configuration_ready":false`) {
					t.Fatal("storage failure was hidden", response.Body.String())
				}
			}
		})
	}
}
