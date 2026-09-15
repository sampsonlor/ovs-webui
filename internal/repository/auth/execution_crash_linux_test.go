//go:build linux

package auth

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

type executionCrashInput struct {
	Options      sqlite.Options
	Request      execution.Request
	Snapshot     plan.Snapshot
	Grant, Phase string
}

func TestExecutionCrashBoundariesNeverReplayProviderWrite(t *testing.T) {
	for _, phase := range []string{"admitted", "committing"} {
		t.Run(phase, func(t *testing.T) {
			r, options := fixture(t)
			g := login(t, r, "admin", false)
			in, snapshot := executionRequest(t, r, g)
			input := executionCrashInput{Options: options, Request: in, Snapshot: snapshot.snapshot, Grant: g.Grant, Phase: phase}
			b, _ := json.Marshal(input)
			path := filepath.Join(filepath.Dir(options.Path), "synthetic-crash-input.json")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.store.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestExecutionCrashChild$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "OVS_EXECUTION_CRASH_INPUT="+path)
			output, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatal("expected crash at durable boundary", err, string(output))
			}
			r = openFixture(t, options)
			r.WithInventory(snapshot)
			p := &executionProvider{observed: execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: "commit-evidence-insufficient"}}
			e, err := r.ConfigureExecution(p)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(testContext); err != nil {
				t.Fatal(err)
			}
			ack, err := e.Submit(testContext, in, r.ExecutionAuthorizer(g.Grant), nil)
			if err != nil || !ack.Replayed || p.prepares != 0 || p.sends != 0 {
				t.Fatal(err, ack)
			}
			record, err := e.Read(testContext, ack.Receipt.Resource.ID)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "admitted" && (record.State != "failed" || record.Outcome.Commit != "rejected") {
				t.Fatal(record)
			}
			if phase == "committing" && (record.State != "recovery-required" || record.Outcome.Commit != "unknown") {
				t.Fatal(record)
			}
		})
	}
}
func TestExecutionCrashChild(t *testing.T) {
	path := os.Getenv("OVS_EXECUTION_CRASH_INPUT")
	if path == "" {
		t.Skip("separate process SIGKILL helper")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var in executionCrashInput
	if err = json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	r := openFixture(t, in.Options)
	r.WithInventory(&planProvider{snapshot: in.Snapshot})
	p := &executionProvider{}
	crash := func() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); select {} }
	if in.Phase == "admitted" {
		p.before = crash
	} else {
		p.after = crash
	}
	e, err := r.ConfigureExecution(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Submit(testContext, in.Request, r.ExecutionAuthorizer(in.Grant), isolatedLease); err != nil {
		t.Fatal(err)
	}
	t.Fatal("missed crash boundary")
}
