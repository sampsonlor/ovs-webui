package runtimehealth

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const (
	transitionPoll   = 2 * time.Second
	transitionWindow = 8 * time.Second
	transitionBudget = 150 * time.Millisecond
)

type State struct{ Key, State string }

type transition struct {
	state       string
	change      evidence.HealthChange
	opened      time.Time
	uncommitted *evidence.Record
}

var observedKeys = []string{"manager-db", "manager-secret-store", "ovsdb", "safety-watchdog", "cache-statistics", "cache-neighbors", "cache-lldp", "cache-fdb"}
var transitionStates = []string{"ready", "degraded", "stale", "unavailable"}

// RunTransitions samples already published state on the manager's background
// schedule. The evidence transaction uses a short budget and no reserved slots.
func RunTransitions(ctx context.Context, store *sqlite.Store, snapshot func() []State) {
	ticker := time.NewTicker(transitionPoll)
	defer ticker.Stop()
	states := make(map[string]transition, len(observedKeys))
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			trackStates(states, snapshot(), now)
			for _, key := range observedKeys {
				item := states[key]
				if item.uncommitted == nil {
					continue
				}
				try, cancel := context.WithTimeout(ctx, transitionBudget)
				err := store.Write(try, func(ctx context.Context, tx *sql.Tx) error {
					_, err := evidence.Append(ctx, tx, *item.uncommitted)
					return err
				})
				cancel()
				if err != nil {
					break
				}
				item.uncommitted = nil
				states[key] = item
			}
		}
	}
}

func trackStates(states map[string]transition, samples []State, now time.Time) {
	for _, sample := range samples {
		if !slices.Contains(observedKeys, sample.Key) || !slices.Contains(transitionStates, sample.State) {
			continue
		}
		item := states[sample.Key]
		if item.state == "" {
			item.state = sample.State
			states[sample.Key] = item
			continue
		}
		if sample.State != item.state {
			if item.change.Transitions == 0 {
				item.change = evidence.HealthChange{Component: sample.Key, Previous: item.state}
				item.opened = now
			}
			item.state, item.change.Current = sample.State, sample.State
			item.change.Transitions = min(9999, item.change.Transitions+1)
		}
		if item.uncommitted == nil && item.change.Transitions != 0 && now.Sub(item.opened) >= transitionWindow {
			change := item.change
			correlation := repository.NewID()
			reason := "health-state-changed"
			if change.Transitions > 1 {
				reason = "health-state-flapping"
			}
			item.uncommitted = &evidence.Record{Collection: "event", Origin: "Manager", Operation: "health.transition", Result: change.Current,
				Reason: reason, Correlation: correlation, DedupKey: correlation, Created: now, HealthChange: &change}
			item.change = evidence.HealthChange{}
		}
		states[sample.Key] = item
	}
}
