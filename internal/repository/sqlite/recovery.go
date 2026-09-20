package sqlite

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type recoveryKey struct{}

// RecoveryContext is used only by compiled transaction recovery/decision paths.
// No HTTP/IPC field accepts a priority flag. It reserves a reader connection and
// makes ordinary writers yield before starting their SQL transaction.
func RecoveryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, recoveryKey{}, true)
}
func isRecovery(ctx context.Context) bool { yes, _ := ctx.Value(recoveryKey{}).(bool); return yes }
func (s *Store) acquireWrite(ctx context.Context) (func(), error) {
	if isRecovery(ctx) {
		s.recoveryWriters.Add(1)
		defer s.recoveryWriters.Add(-1)
		return acquire(ctx, s.writeSlot, s.recoveryWriteQueue)
	}
	if s.recoveryWriters.Load() > 0 {
		return nil, repository.ErrBusy
	}
	release, err := acquire(ctx, s.writeSlot, s.writeQueue)
	if err != nil {
		return nil, err
	}
	if s.recoveryWriters.Load() > 0 {
		release()
		return nil, repository.ErrBusy
	}
	return release, nil
}
