//go:build linux

package ovsdb

import (
	"context"
	"net"
	"sync"
)

type treeSourceScopeKey struct{}

// Keep this preflight's own read-only probe connections alive until its last
// source inspection. Closing a probe between inspections asynchronously closes
// the server's accepted FD while the next inspection enumerates /proc/PID/fd.
// The strict FD/peer checks remain unchanged; no vanished FD is ignored.
type treeSourceScope struct {
	mu          sync.Mutex
	closed      bool
	connections []net.Conn
}

func treeSourceContext(ctx context.Context) (context.Context, func()) {
	if _, ok := ctx.Value(treeSourceScopeKey{}).(*treeSourceScope); ok {
		return ctx, func() {}
	}
	scope := &treeSourceScope{}
	stop := context.AfterFunc(ctx, scope.close)
	return context.WithValue(ctx, treeSourceScopeKey{}, scope), func() { stop(); scope.close() }
}

func (s *treeSourceScope) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, conn := range s.connections {
		_ = conn.Close()
	}
	s.connections = nil
}

// A fixed per-attempt resource budget, independent of provider FD budgets.
// No connection or trust survives an attempt, its cancellation or deadline.
func retainTreeSourceConnection(ctx context.Context, conn net.Conn) error {
	scope, ok := ctx.Value(treeSourceScopeKey{}).(*treeSourceScope)
	if !ok {
		_ = conn.Close()
		return sourceUnproven
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.closed || ctx.Err() != nil || len(scope.connections) >= 12 {
		_ = conn.Close()
		return sourceUnproven
	}
	scope.connections = append(scope.connections, conn)
	return nil
}
