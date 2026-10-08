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

func TestAuthDiagnosticFieldsStayWithinCompiledVocabulary(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"operation", "security.read"}, {"phase", "operation"},
		{"error_class", "storage_busy"}, {"request_state", "canceled"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			var out bytes.Buffer
			logger := slog.New(New(slog.NewJSONHandler(&out, nil)))
			logger.With(tc.key, tc.value).Warn("ipc_request_rejected")
			var record map[string]any
			if json.Unmarshal(out.Bytes(), &record) != nil || record[tc.key] != tc.value {
				t.Fatal("compiled diagnostic lost in production redaction")
			}
			for _, invalid := range []any{
				"synthetic-private-label", "ovsg_synthetic", "https://synthetic.invalid/private",
				"SELECT private FROM synthetic", true, 7, slog.GroupValue(slog.String("service", "synthetic-private-label")),
			} {
				out.Reset()
				logger.Warn("ipc_request_rejected", tc.key, invalid)
				if json.Unmarshal(out.Bytes(), &record) != nil || record[tc.key] != Marker {
					t.Fatal("unregistered diagnostic value or shape admitted")
				}
				out.Reset()
				logger.With(tc.key, invalid).Warn("ipc_request_rejected")
				if json.Unmarshal(out.Bytes(), &record) != nil || record[tc.key] != Marker {
					t.Fatal("bound diagnostic bypassed redaction")
				}
			}
		})
	}
}
