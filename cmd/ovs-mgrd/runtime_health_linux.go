//go:build linux

package main

import (
	"sync/atomic"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/runtimehealth"
)

const watchdogObservationMaxAge = 10 * time.Second

type managerRuntimeHealth struct {
	restartConfigured bool
	restartBootKnown  bool
	storage           func() repository.Status
	inventory         *inventory.Service
	secretCheckedAt   time.Time
	authConfigured    bool
	secretConfigured  bool
	safetyConfigured  bool
	debug             *runtimehealth.Debug
	lastWatchdog      atomic.Pointer[ipc.HealthComponent]
}

func healthTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

// snapshot is shared by the authorized IPC readout and operational observers.
// It reads the repository's latched state, not a fresh database probe, and does
// not poll OVS, opens no secrets, and cannot progress a recovery transaction.
func (m *managerRuntimeHealth) snapshot(now time.Time) ipc.Health {
	status := m.storage()
	database := ipc.HealthComponent{
		Key: "manager-db", State: "ready", Authority: "manager-repository-state", ObservedAt: healthTime(now),
	}
	switch {
	case !status.Readable:
		database.State, database.Reason = "unavailable", status.Code
		if database.Reason == "" {
			database.Reason = "STORAGE_UNAVAILABLE"
		}
	case !status.Writable:
		database.State, database.Reason = "degraded", status.Code
		if database.Reason == "" {
			database.Reason = "STORAGE_READ_ONLY"
		}
	case status.Warning != "":
		database.State, database.Reason = "degraded", status.Warning
	}
	secret := ipc.HealthComponent{
		Key: "manager-secret-store", State: "disabled", Reason: "SECRET_STORE_NOT_CONFIGURED",
		Authority: "manager-secretstore-initialization", ObservedAt: healthTime(m.secretCheckedAt),
	}
	if m.secretConfigured {
		// Startup verified the key ring and bound this partition to manager.db.
		// This does not establish decryptability of any specific record.
		secret.State, secret.Reason = "ready", ""
		if !status.Readable {
			secret.State, secret.Reason = "unavailable", "SECRET_STORE_STORAGE_UNAVAILABLE"
		}
	}
	watchdog := m.watchdog(now)
	restart := ipc.HealthComponent{Key: "management-restart", State: "disabled", Reason: "MANAGEMENT_RESTART_POLICY_DISABLED", Authority: "manager-runtime-policy", ObservedAt: healthTime(now)}
	if m.restartConfigured {
		restart.State, restart.Reason = "ready", "MANAGEMENT_RESTART_POLICY_ENABLED"
		if !m.restartBootKnown || !m.authConfigured {
			restart.State, restart.Reason = "unavailable", "MANAGEMENT_RESTART_UNAVAILABLE"
		}
	}
	view := ipc.Health{
		State: "ready", Scope: "runtime-inspection",
		AuthenticationReady: m.authConfigured && status.Writable,
		ConfigurationReady:  m.safetyConfigured && status.Writable && watchdog.State == "ready",
		Storage:             &ipc.StorageHealth{Manager: &status},
		Components: []ipc.HealthComponent{
			{Key: "manager", State: "ready", Authority: "manager-runtime", ObservedAt: healthTime(now)},
			database, secret, watchdog, restart,
		},
	}
	if database.State != "ready" || watchdog.State == "degraded" || watchdog.State == "stale" {
		view.State = "degraded"
	}
	if m.debug != nil {
		debug := ipc.HealthComponent{
			Key: "management-debug", State: "disabled", Reason: "DEBUG_INACTIVE",
			Authority: "manager-debug-lease", ObservedAt: healthTime(now),
		}
		if _, active := m.debug.State(); active {
			debug.State, debug.Reason = "ready", "DEBUG_ACTIVE"
		}
		view.Components = append(view.Components, debug)
	}
	diagnostics := ipc.HealthComponent{Key: "diagnostics-executor", State: "unavailable",
		Reason: "DIAGNOSTICS_EXECUTOR_UNAVAILABLE", Authority: "manager-diagnostic-executor", ObservedAt: healthTime(now)}
	if m.authConfigured && m.inventory != nil {
		diagnostics.State, diagnostics.Reason = "ready", ""
	}
	view.Components = append(view.Components, diagnostics)
	for _, observation := range m.inventory.RuntimeHealth() {
		view.Components = append(view.Components, ipc.HealthComponent{
			Key: observation.Key, State: observation.State, Reason: observation.Reason, Authority: observation.Authority,
			ObservedAt: healthTime(observation.ObservedAt),
		})
		if observation.Key == "ovsdb" && observation.State != "ready" {
			view.State = "degraded"
			view.ConfigurationReady = false
		}
	}
	return view
}

func (m *managerRuntimeHealth) watchdog(now time.Time) ipc.HealthComponent {
	if last := m.lastWatchdog.Load(); last != nil {
		out := *last
		if now.Before(*out.ObservedAt) || now.Sub(*out.ObservedAt) > watchdogObservationMaxAge {
			out.State, out.Reason = "stale", "SAFETY_WATCHDOG_STALE"
		}
		return out
	}
	out := ipc.HealthComponent{Key: "safety-watchdog", Authority: "manager-watchdog-cycle"}
	if m.safetyConfigured {
		out.State, out.Reason = "unknown", "SAFETY_WATCHDOG_PENDING"
	} else {
		out.State, out.Reason = "disabled", "SAFETY_WATCHDOG_NOT_CONFIGURED"
	}
	return out
}

func (m *managerRuntimeHealth) recordWatchdog(at time.Time, storageWritable bool, cycleErr, notifyErr error) {
	out := ipc.HealthComponent{
		Key: "safety-watchdog", State: "ready", Authority: "manager-watchdog-cycle", ObservedAt: healthTime(at),
	}
	switch {
	case !storageWritable:
		out.State, out.Reason = "unavailable", "SAFETY_WATCHDOG_STORAGE_UNAVAILABLE"
	case cycleErr != nil:
		out.State, out.Reason = "degraded", "SAFETY_WATCHDOG_CYCLE_FAILED"
	case notifyErr != nil:
		out.State, out.Reason = "degraded", "SAFETY_WATCHDOG_NOTIFY_FAILED"
	case !m.safetyConfigured:
		out.State, out.Reason = "disabled", "SAFETY_WATCHDOG_NOT_CONFIGURED"
	}
	m.lastWatchdog.Store(&out)
}
