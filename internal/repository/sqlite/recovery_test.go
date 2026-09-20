package sqlite

import (
	"context"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"testing"
	"time"
)

func TestRecoveryWriterDoesNotQueueBehindOrdinaryWaiters(t *testing.T) {
	s := newStore(Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	hold, err := s.acquireWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	normal := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			release, err := s.acquireWrite(ctx)
			if release != nil {
				release()
			}
			normal <- err
		}()
	}
	for len(s.writeQueue) != 2 {
		if ctx.Err() != nil {
			hold()
			t.Fatal("ordinary waiters absent")
		}
		time.Sleep(time.Millisecond)
	}
	control := make(chan error, 1)
	go func() {
		release, err := s.acquireWrite(RecoveryContext(ctx))
		if release != nil {
			release()
		}
		control <- err
	}()
	for s.recoveryWriters.Load() != 1 {
		if ctx.Err() != nil {
			hold()
			t.Fatal("recovery waiter absent")
		}
		time.Sleep(time.Millisecond)
	}
	hold()
	for i := 0; i < 2; i++ {
		if err := <-normal; !errors.Is(err, repository.ErrBusy) {
			t.Fatal("ordinary writer bypassed waiting recovery", err)
		}
	}
	if err := <-control; err != nil {
		t.Fatal(err)
	}
}
