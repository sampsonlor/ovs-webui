package runtimecontrol

import (
	"context"
	"os"
	"testing"
	"time"
)

// The Python smoke fixture alone stages the canonical unit names and real executables on a disposable runner.
func TestNativeSystemdRestartInHostedUbuntu(t *testing.T) {
	if os.Getenv("OVS_CI_SYSTEMD_RESTART") != "1" || os.Getenv("GITHUB_ACTIONS") != "true" ||
		os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Geteuid() != 0 {
		t.Skip("requires canonical product units staged by the disposable hosted Ubuntu smoke fixture")
	}
	target := Target(os.Getenv("OVS_CI_SYSTEMD_RESTART_TARGET"))
	var other Target
	switch target {
	case TargetManager:
		other = TargetWeb
	case TargetWeb:
		other = TargetManager
	default:
		t.Fatal("an exact fixture target is required")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	supervisor := NewSystemd()
	inspect := func(target Target) UnitState {
		t.Helper()
		state, err := supervisor.Inspect(ctx, target)
		if err != nil || !state.Active || state.JobPending || state.InvocationID == "" || state.MainPID == 0 {
			t.Fatalf("inspect %s = %#v, %v", target, state, err)
		}
		return state
	}
	before := inspect(target)
	unaffected := inspect(other)
	job, err := supervisor.TryRestartAfter(ctx, target, before)
	if err != nil || !validJobPath(job) {
		t.Fatalf("restart %s = %q, %v", target, job, err)
	}

	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	var latest UnitState
	var latestErr error
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("restart job %s never produced a different active invocation; last = %#v, %v", job, latest, latestErr)
		case <-tick.C:
			if actual := inspect(other); actual != unaffected {
				t.Fatalf("restart of %s changed %s: before %#v, after %#v", target, other, unaffected, actual)
			}
			latest, latestErr = supervisor.Inspect(ctx, target)
			if latestErr == nil && latest.Active && !latest.JobPending &&
				latest.InvocationID != before.InvocationID && latest.MainPID != before.MainPID {
				if actual := inspect(other); actual != unaffected {
					t.Fatalf("restart of %s changed %s: before %#v, after %#v", target, other, unaffected, actual)
				}
				t.Logf("typed systemd job %s replaced only the %s invocation", job, target)
				return
			}
		}
	}
}
