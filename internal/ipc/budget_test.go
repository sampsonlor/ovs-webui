package ipc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueueBoundCancellationAndClassIsolation(t *testing.T) {
	budgets := defaultBudgets()
	heavy := newBudget(1, 1)
	budgets[Heavy] = heavy
	release, _ := heavy.acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		release, err := heavy.acquire(ctx)
		if release != nil {
			release()
		}
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(heavy.waiting) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("queue did not fill")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := heavy.acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatal("unbounded queue")
	}
	controlRelease, err := budgets[Control].acquire(context.Background())
	if err != nil {
		t.Fatal("heavy work starved control")
	}
	controlRelease()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
	if len(heavy.active) != 0 || len(heavy.waiting) != 0 {
		t.Fatal("cancelled waiter leaked capacity")
	}
}
