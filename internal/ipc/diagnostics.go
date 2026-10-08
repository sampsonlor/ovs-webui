package ipc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

// Diagnostic values come from compiled vocabularies, never error messages,
// credentials, request bodies, raw URLs or SQL. Public rejection codes remain
// unchanged; these fields belong only to the private manager journal.
func diagnosticOperation(path string) string {
	op := ""
	if strings.HasPrefix(path, "/ipc/v1/operations/") {
		op = strings.TrimPrefix(path, "/ipc/v1/operations/")
	}
	switch op {
	case "runtime.inspect", "auth.authenticate", "auth.inspect", "auth.check",
		"auth.reauthenticate", "auth.revoke", "security.read", "security.execute",
		"inventory.read", "candidate.prepare", "candidate.read", "candidate.validate",
		"safe.admit", "safe.resolve", "safe.decide", "tls.execute", "tls.state":
		return op
	}
	switch path {
	case "/ipc/v1/handshake":
		return "handshake"
	case "/ipc/v1/health":
		return "health"
	}
	return "unknown"
}

func diagnosticClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrQueueFull):
		return "ipc_queue_full"
	case errors.Is(err, repository.ErrBusy):
		return "storage_busy"
	case errors.Is(err, repository.ErrCanceled):
		return "storage_canceled"
	case errors.Is(err, repository.ErrCommitUnknown):
		return "storage_commit_unknown"
	case errors.Is(err, repository.ErrUnavailable):
		return "storage_unavailable"
	case errors.Is(err, repository.ErrNotFound):
		return "storage_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var problem *apitypes.Problem
	if errors.As(err, &problem) {
		return "domain_rejection"
	}
	var transport net.Error
	if errors.As(err, &transport) && transport.Timeout() {
		return "transport_timeout"
	}
	return "unclassified"
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request, status int, code, phase string, err error) {
	requestState := "active"
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		requestState = "deadline_exceeded"
	} else if r.Context().Err() != nil {
		requestState = "canceled"
	}
	// Close instead of draining an untrusted/oversized body for another request.
	r.Close = true
	w.Header().Set("Connection", "close")
	h.logger.Warn("ipc_request_rejected", "code", code,
		"operation", diagnosticOperation(r.URL.Path), "phase", phase,
		"error_class", diagnosticClass(err), "request_state", requestState)
	h.write(w, status, Problem{Code: code})
}
