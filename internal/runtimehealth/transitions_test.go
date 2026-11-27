package runtimehealth

import (
	"testing"
	"time"
)

func TestHealthTransitionsCoalesceAndKeepFailedWriteStable(t *testing.T) {
	start := time.Now()
	states := map[string]transition{}
	observe := func(seconds int, state string) {
		trackStates(states, []State{{"ovsdb", state}, {"non-product-source", "ready"}}, start.Add(time.Duration(seconds)*time.Second))
	}
	observe(0, "ready")
	observe(2, "ready")
	if len(states) != 1 || states["ovsdb"].uncommitted != nil || states["ovsdb"].change.Transitions != 0 {
		t.Fatal("startup and identical reads must not produce events")
	}
	observe(4, "unavailable")
	observe(6, "ready")
	observe(8, "unavailable")
	observe(10, "unavailable")
	if states["ovsdb"].uncommitted != nil {
		t.Fatal("published before the coalescing window elapsed")
	}
	observe(12, "unavailable")
	first := states["ovsdb"].uncommitted
	if first == nil || first.Reason != "health-state-flapping" || first.HealthChange.Transitions != 3 ||
		first.HealthChange.Previous != "ready" || first.HealthChange.Current != "unavailable" || first.DedupKey == "" {
		t.Fatalf("bad coalesced transition: %+v", first)
	}
	observe(14, "ready")
	observe(22, "ready")
	item := states["ovsdb"]
	if item.uncommitted != first || first.HealthChange.Current != "unavailable" || item.change.Transitions != 1 {
		t.Fatal("new activity modified the pending idempotent write")
	}
	item.uncommitted = nil
	states["ovsdb"] = item
	observe(24, "ready")
	second := states["ovsdb"].uncommitted
	if second == nil || second.DedupKey == first.DedupKey || second.HealthChange.Current != "ready" || second.HealthChange.Transitions != 1 {
		t.Fatal("the subsequent real transition was lost")
	}
}
