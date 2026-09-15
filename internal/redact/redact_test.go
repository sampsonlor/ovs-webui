package redact

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestOperationalLogsAndDiagnosticDocumentsRedactSecrets(t *testing.T) {
	raw := "synthetic-password-35"
	var out bytes.Buffer
	logger := slog.New(New(slog.NewJSONHandler(&out, nil))).With("service", "ovs-webd", "password", raw)
	logger.Info(raw, "url", raw, "error", raw, "cookie", "ovss_synthetic", "nested", slog.GroupValue(slog.String("private_key", raw)))
	logger.Info("security_action_completed", "code", "KEY_ROTATED")
	if strings.Contains(out.String(), raw) || strings.Contains(out.String(), "ovss_synthetic") {
		t.Fatal("log secret leak")
	}
	if !strings.Contains(out.String(), "KEY_ROTATED") {
		t.Fatal("stable error code lost")
	}
	doc := Document(map[string]any{"private_key_pem": raw, "nested": []any{map[string]any{"token": raw, "note": "ovsg_synthetic"}}, "label": "public", "password": raw, "certificate_pem": "public-cert"})
	blob, _ := json.Marshal(doc)
	if bytes.Contains(blob, []byte(raw)) || bytes.Contains(blob, []byte("ovsg_synthetic")) || !bytes.Contains(blob, []byte("public-cert")) {
		t.Fatal("export redaction failed")
	}
}
