package runtimehealth

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/redact"
)

func TestManagementDebugDeadlineAndRedaction(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	level := NewDebug(clock)
	var output bytes.Buffer
	logger := slog.New(redact.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: level})))
	const secret = "ovsg_synthetic-secret"
	log := func() {
		logger.Debug("management_debug_sample", "code", "DEBUG_MANAGER_STORAGE_WRITABLE",
			"scope", "manager", "grant", secret, "unexpected", secret)
	}
	log()
	if output.Len() != 0 {
		t.Fatal("manager booted with Debug")
	}
	lease := level.Prepare(300)
	level.Publish(lease)
	log()
	value := output.String()
	if !strings.Contains(value, `"level":"DEBUG"`) ||
		!strings.Contains(value, `"msg":"management_debug_sample"`) ||
		!strings.Contains(value, `"code":"DEBUG_MANAGER_STORAGE_WRITABLE"`) ||
		strings.Contains(value, secret) {
		t.Fatal("unexpected redacted diagnostic entry", value)
	}
	now = lease.ExpiresAt.Add(-time.Nanosecond)
	if level.Level() != slog.LevelDebug {
		t.Fatal("lease expired early")
	}
	now = lease.ExpiresAt
	log()
	if level.Level() != slog.LevelInfo || output.String() != value {
		t.Fatal("expired lease emitted Debug before cleanup")
	}
	now = lease.ExpiresAt.Add(-time.Second)
	if level.Level() != slog.LevelInfo {
		t.Fatal("an expired lease was reactivated after a clock discontinuity")
	}
	level.Publish(level.Prepare(900))
	if level.Level() != slog.LevelDebug || NewDebug(clock).Level() != slog.LevelInfo {
		t.Fatal("restart restored a live Debug lease")
	}
	level.Publish(level.Prepare(0))
	if level.Level() != slog.LevelInfo {
		t.Fatal("explicit disable did not return to Info")
	}
}
