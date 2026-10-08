package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/redact"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type diagnosticManager struct {
	authn.Manager
	err error
}

func (m *diagnosticManager) ReadAuth(context.Context, string, authn.Query) (authn.Response, error) {
	return authn.Response{}, m.err
}

func TestAuthFailureDiagnosticsPreservePublicRejection(t *testing.T) {
	for _, tc := range []struct {
		name, class, code string
		err               error
		status            int
	}{
		{"busy", "storage_busy", "AUTH_UNAVAILABLE", repository.ErrBusy, 503},
		{"canceled", "storage_canceled", "AUTH_UNAVAILABLE", repository.ErrCanceled, 503},
		{"unknown commit", "storage_commit_unknown", "AUTH_UNAVAILABLE", repository.ErrCommitUnknown, 503},
		{"revoked", "domain_rejection", "CREDENTIAL_EXPIRED_OR_REVOKED", apitypes.Fail(401, "CREDENTIAL_EXPIRED_OR_REVOKED"), 401},
		{"unknown", "unclassified", "AUTH_UNAVAILABLE", errors.New("private driver failure"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const private = "synthetic-password-cookie-csrf-SQL-host-path"
			var logs bytes.Buffer
			err := fmt.Errorf("%s: %w", private, tc.err)
			h := NewHandler(CurrentProtocol("test"), nil, slog.New(redact.New(slog.NewJSONHandler(&logs, nil)))).WithAuthentication(&diagnosticManager{err: err})
			r := httptest.NewRequest("POST", "/ipc/v1/operations/security.read", strings.NewReader(`{"method":"GET","uri":"/api/v1/session?private=`+private+`"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+authn.Secret("ovsg_"))
			w := httptest.NewRecorder()
			h.authOperation(w, r)
			if w.Code != tc.status || w.Body.String() != `{"code":"`+tc.code+`"}`+"\n" || w.Header().Get("Connection") != "close" {
				t.Fatal("public response or connection policy changed", w.Code, w.Body.String())
			}
			var record map[string]any
			if json.Unmarshal(logs.Bytes(), &record) != nil || record["operation"] != "security.read" || record["phase"] != "operation" || record["error_class"] != tc.class || record["request_state"] != "active" || record["code"] != tc.code {
				t.Fatal("missing bounded rejection metadata", logs.String())
			}
			if len(record) != 8 || strings.Contains(logs.String(), private) || strings.Contains(logs.String(), r.Header.Get("Authorization")) {
				t.Fatal("diagnostic retained request or raw cause")
			}
		})
	}
}

func TestAuthDiagnosticClassifiesWrappedCausesWithoutMessages(t *testing.T) {
	for _, tc := range []struct {
		err   error
		class string
	}{
		{nil, "none"}, {ErrQueueFull, "ipc_queue_full"},
		{repository.ErrBusy, "storage_busy"}, {repository.ErrCanceled, "storage_canceled"},
		{repository.ErrCommitUnknown, "storage_commit_unknown"}, {repository.ErrUnavailable, "storage_unavailable"},
		{repository.ErrNotFound, "storage_not_found"}, {context.Canceled, "canceled"},
		{context.DeadlineExceeded, "deadline_exceeded"}, {apitypes.Fail(403, "CAPABILITY_DENIED"), "domain_rejection"},
		{&net.DNSError{Err: "private network detail", IsTimeout: true}, "transport_timeout"},
		{errors.New("private arbitrary error"), "unclassified"},
	} {
		err := tc.err
		if err != nil {
			err = fmt.Errorf("never logged: %w", err)
		}
		if got := diagnosticClass(err); got != tc.class {
			t.Fatalf("class %s, want %s", got, tc.class)
		}
	}
}

func TestAuthDiagnosticQueueSeparatesRequestDeadlineAndRepositoryCause(t *testing.T) {
	var logs bytes.Buffer
	h := NewHandler(CurrentProtocol("test"), nil, slog.New(redact.New(slog.NewJSONHandler(&logs, nil))))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/ipc/v1/operations/auth.inspect", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.queueError(w, r, context.Canceled)
	var record map[string]any
	if json.Unmarshal(logs.Bytes(), &record) != nil || w.Code != 504 || record["phase"] != "queue" || record["request_state"] != "canceled" || record["error_class"] != "canceled" {
		t.Fatal("queue cancellation lost its stage")
	}
	logs.Reset()
	w = httptest.NewRecorder()
	h.queueError(w, r, ErrQueueFull)
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" || !strings.Contains(logs.String(), `"error_class":"ipc_queue_full"`) {
		t.Fatal("queue bound or Retry-After changed")
	}
}

func TestAuthDiagnosticNeverLogsUnknownPath(t *testing.T) {
	const private = "synthetic-private-host-path"
	if diagnosticOperation("auth.inspect") != "unknown" || diagnosticOperation("/ipc/v1/operations/auth.inspect/"+private) != "unknown" {
		t.Fatal("unregistered operation admitted")
	}
	var logs bytes.Buffer
	h := NewHandler(CurrentProtocol("test"), nil, slog.New(redact.New(slog.NewJSONHandler(&logs, nil))))
	r := httptest.NewRequest("POST", "/ipc/v1/operations/"+private, nil)
	h.problem(httptest.NewRecorder(), r, 404, "IPC_OPERATION_UNKNOWN")
	if strings.Contains(logs.String(), private) || !strings.Contains(logs.String(), `"operation":"unknown"`) {
		t.Fatal("raw path retained")
	}
}
