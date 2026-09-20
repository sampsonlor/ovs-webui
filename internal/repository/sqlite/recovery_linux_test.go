//go:build linux

package sqlite

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

func TestRecoveryHasReservedSQLiteReader(t *testing.T) {
	s := initialized(t, options(t))
	ready := make(chan struct{}, cap(s.readSlots))
	release := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < cap(s.readSlots); i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			err := s.Read(background, func(ctx context.Context, q *sql.Conn) error {
				var n int
				if err := q.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil {
					return err
				}
				ready <- struct{}{}
				<-release
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	defer func() { close(release); readers.Wait() }()
	for i := 0; i < cap(s.readSlots); i++ {
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("ordinary reader failed to start")
		}
	}
	if err := s.Read(RecoveryContext(background), func(ctx context.Context, q *sql.Conn) error {
		var n int
		return q.QueryRowContext(ctx, "SELECT 1").Scan(&n)
	}); err != nil {
		t.Fatal("recovery blocked by ordinary readers", err)
	}
}
