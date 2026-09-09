package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

type connectionKey struct{}
type connectionState struct{ negotiated atomic.Bool }
type authenticatedConn interface {
	net.Conn
	state() *connectionState
}

type Handler struct {
	protocol   Protocol
	authorizer Authorizer
	budgets    map[Class]*budget
	inspect    func(context.Context) (Health, error)
	logger     *slog.Logger
}

func NewHandler(protocol Protocol, authorizer Authorizer, logger *slog.Logger) *Handler {
	if authorizer == nil {
		authorizer = DenyAllAuthorizer{}
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Handler{protocol: protocol, authorizer: authorizer, budgets: defaultBudgets(), logger: logger,
		inspect: func(context.Context) (Health, error) { return BootstrapHealth(), nil }}
}

func HTTPServer(handler *Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: HeaderTimeout, ReadTimeout: RequestTimeout,
		WriteTimeout: RequestTimeout, IdleTimeout: RequestTimeout, MaxHeaderBytes: MaxHeaderBytes,
		ErrorLog: log.New(io.Discard, "", 0),
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if trusted, ok := conn.(authenticatedConn); ok {
				return context.WithValue(ctx, connectionKey{}, trusted.state())
			}
			return ctx
		},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	state, trusted := r.Context().Value(connectionKey{}).(*connectionState)
	if !trusted {
		h.problem(w, r, 403, "IPC_PEER_DENIED")
		return
	}
	if r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.Header.Get("Upgrade") != "" {
		h.problem(w, r, 400, "IPC_INVALID_REQUEST")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	switch r.URL.Path {
	case "/ipc/v1/health":
		if r.Method != http.MethodGet || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			h.problem(w, r, 400, "IPC_INVALID_REQUEST")
			return
		}
		h.write(w, 200, BootstrapHealth())
	case "/ipc/v1/handshake":
		state.negotiated.Store(false)
		release, err := h.budgets[Auth].acquire(ctx)
		if err != nil {
			h.queueError(w, r, err)
			return
		}
		defer release()
		var offer Protocol
		if !h.decode(w, r, &offer) {
			return
		}
		if offer != h.protocol || offer.Software == "" {
			h.problem(w, r, 409, "IPC_VERSION_MISMATCH")
			return
		}
		state.negotiated.Store(true)
		h.write(w, 200, HandshakeReply{Accepted: true, Protocol: h.protocol})
	case "/ipc/v1/operations/runtime.inspect":
		if !state.negotiated.Load() {
			h.problem(w, r, 409, "IPC_HANDSHAKE_REQUIRED")
			return
		}
		release, err := h.budgets[inspectOperation.Class].acquire(ctx)
		if err != nil {
			h.queueError(w, r, err)
			return
		}
		defer release()
		var request InspectRequest
		if !h.decode(w, r, &request) {
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			h.problem(w, r, 401, "IPC_GRANT_REQUIRED")
			return
		}
		grant := strings.TrimPrefix(values[0], "Bearer ")
		if grant == "" || len(grant) > 4096 || strings.ContainsAny(grant, " \t\r\n") {
			h.problem(w, r, 401, "IPC_GRANT_REQUIRED")
			return
		}
		if err := h.authorizer.Authorize(ctx, grant, inspectOperation); err != nil {
			if ctx.Err() != nil {
				h.problem(w, r, 504, "IPC_DEADLINE_EXCEEDED")
			} else if errors.Is(err, ErrAuthUnavailable) {
				h.problem(w, r, 503, "IPC_AUTH_UNAVAILABLE")
			} else {
				h.problem(w, r, 403, "IPC_OPERATION_DENIED")
			}
			return
		}
		result, err := h.inspect(ctx)
		if ctx.Err() != nil {
			h.problem(w, r, 504, "IPC_DEADLINE_EXCEEDED")
			return
		}
		if err != nil {
			h.problem(w, r, 503, "IPC_OPERATION_UNAVAILABLE")
			return
		}
		h.write(w, 200, result)
	default:
		h.problem(w, r, 404, "IPC_OPERATION_UNKNOWN")
	}
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Method != http.MethodPost {
		h.problem(w, r, 405, "IPC_METHOD_NOT_ALLOWED")
		return false
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Encoding") != "" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		h.problem(w, r, 415, "IPC_CONTENT_TYPE_REQUIRED")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			h.problem(w, r, 413, "IPC_BODY_TOO_LARGE")
		} else {
			h.problem(w, r, 400, "IPC_INVALID_BODY")
		}
		return false
	}
	if DecodeStrict(data, target) != nil {
		h.problem(w, r, 400, "IPC_INVALID_BODY")
		return false
	}
	return true
}

func (h *Handler) queueError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrQueueFull) {
		w.Header().Set("Retry-After", "1")
		h.problem(w, r, 429, "IPC_QUEUE_FULL")
	} else {
		h.problem(w, r, 504, "IPC_DEADLINE_EXCEEDED")
	}
}

func (h *Handler) problem(w http.ResponseWriter, r *http.Request, status int, code string) {
	// Close instead of draining an untrusted/oversized body for another request.
	r.Close = true
	w.Header().Set("Connection", "close")
	h.logger.Warn("ipc_request_rejected", "code", code)
	h.write(w, status, Problem{Code: code})
}

func (h *Handler) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
