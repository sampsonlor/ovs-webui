//go:build linux

package ipc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/redact"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type diagnosticLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (l *diagnosticLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.Write(p)
}

func (l *diagnosticLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.String()
}

func TestUnixAuthFailureDiagnosticPreservesRemoteStatus(t *testing.T) {
	var logs diagnosticLog
	h := NewHandler(CurrentProtocol("test"), nil, slog.New(redact.New(slog.NewJSONHandler(&logs, nil)))).WithAuthentication(&diagnosticManager{err: repository.ErrBusy})
	client, _ := testServer(t, h, uint32(os.Geteuid()))
	credential := authn.Secret("ovsg_")
	_, err := client.ReadAuth(context.Background(), credential, authn.Query{Method: "GET", URI: "/api/v1/session"})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Status != 503 || remote.Code != "AUTH_UNAVAILABLE" {
		t.Fatal("private diagnostic changed wire error", err)
	}
	if !strings.Contains(logs.String(), `"operation":"security.read"`) || !strings.Contains(logs.String(), `"error_class":"storage_busy"`) || strings.Contains(logs.String(), credential) {
		t.Fatal("real Unix rejection lost bounded metadata")
	}
}
