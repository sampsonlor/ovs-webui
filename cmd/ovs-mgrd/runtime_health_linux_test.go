//go:build linux

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestManagerHealthRetainsActualWatchdogEvidence(t *testing.T) {
	now := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	health := &managerRuntimeHealth{
		storage:          func() repository.Status { return repository.Status{State: "ready", Readable: true, Writable: true} },
		safetyConfigured: true,
	}
	if before := health.watchdog(now); before.State != "unknown" || before.ObservedAt != nil {
		t.Fatalf("an unobserved watchdog was reported healthy: %+v", before)
	}
	health.recordWatchdog(now, true, nil, nil)
	if observed := health.watchdog(now.Add(time.Second)); observed.State != "ready" || !observed.ObservedAt.Equal(now) {
		t.Fatalf("watchdog read invented a fresh timestamp: %+v", observed)
	}
	failed := now.Add(2 * time.Second)
	health.recordWatchdog(failed, true, errors.New("synthetic arbitrary private error"), nil)
	if current := health.watchdog(failed); current.State != "degraded" || current.Reason != "SAFETY_WATCHDOG_CYCLE_FAILED" ||
		!current.ObservedAt.Equal(failed) {
		t.Fatalf("cycle failure was not isolated and sanitized: %+v", current)
	}
	if stale := health.watchdog(failed.Add(watchdogObservationMaxAge + time.Second)); stale.State != "stale" ||
		!stale.ObservedAt.Equal(failed) {
		t.Fatalf("old health evidence stayed current: %+v", stale)
	}
	result := health.snapshot(failed)
	if result.ConfigurationReady || result.State != "degraded" || result.Components[2].State != "disabled" ||
		result.Components[2].ObservedAt != nil {
		t.Fatalf("readiness exceeded verified manager and SecretStore evidence: %+v", result)
	}
}
