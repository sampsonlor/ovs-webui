package runtimehealth

import (
	"log/slog"
	"sync"
	"time"
)

const ResourceID = "31fa3492-67e6-4a39-bfce-000000000001"

// DebugLease carries only the fixed audit identity of a temporary manager log level.
type DebugLease struct {
	JobID, Correlation, Principal, Credential, RequestID, RequestEpoch string
	StartedAt, ExpiresAt                                               time.Time
}

// Debug is a process-local log level with a bounded monotonic deadline. A new
// manager starts at Info and never reconstructs a lease from durable evidence.
type Debug struct {
	mu      sync.Mutex
	clock   func() time.Time
	lease   DebugLease
	expired bool
}

func NewDebug(clock func() time.Time) *Debug {
	return &Debug{clock: clock}
}

func (d *Debug) Prepare(seconds int) DebugLease {
	switch seconds {
	case 300, 600, 900:
		now := d.clock()
		if now.IsZero() {
			return DebugLease{}
		}
		return DebugLease{StartedAt: now, ExpiresAt: now.Add(time.Duration(seconds) * time.Second)}
	default:
		return DebugLease{}
	}
}

// Publish only follows a known durable commit. The caller serializes updates
// and expiration evidence via the manager authorization gate.
func (d *Debug) Publish(lease DebugLease) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lease = lease
	d.expired = false
}

func (d *Debug) State() (DebugLease, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lease.StartedAt.IsZero() || d.expired {
		return d.lease, false
	}
	now := d.clock()
	d.expired = now.IsZero() || now.Before(d.lease.StartedAt) || !now.Before(d.lease.ExpiresAt)
	return d.lease, !d.expired
}

func (d *Debug) Level() slog.Level {
	if _, active := d.State(); active {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
