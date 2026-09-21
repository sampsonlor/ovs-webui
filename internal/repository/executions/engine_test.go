package executions

import (
	"context"
	"errors"
	"testing"
)

func TestCancelledDecisionCannotTakeAnObservationLock(t *testing.T) {
	e := &Engine{active: map[string]bool{}}
	if !e.claim("transaction") {
		t.Fatal("initial claim")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.claimDecision(ctx, "transaction"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.claim("transaction") {
		t.Fatal("cancelled waiter released someone else's lock")
	}
	e.release("transaction")
	if err := e.claimDecision(ctx, "transaction"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled waiter acquired free lock", err)
	}
	if !e.claim("transaction") {
		t.Fatal("cancelled waiter retained lock")
	}
}
