//go:build linux

package main

import (
	"flag"
	"io"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestOfflineVLANCheckpointCommandRequiresExactStandaloneOptions(t *testing.T) {
	tx, generation := repository.NewID(), repository.NewID()
	required := []string{"--checkpoint-generation", generation, "--checkpoint-root", "/var/lib/fixture-checkpoint"}
	for _, tc := range []struct {
		name           string
		args           []string
		enabled, valid bool
	}{
		{"normal service", []string{"--database", "/fixture/manager.db"}, false, true},
		{"publish", append([]string{"--publish-vlan-checkpoint", tx, "--database", "/fixture/manager.db"}, required...), true, true},
		{"independent verify", append([]string{"--verify-vlan-checkpoint", tx}, required...), true, true},
		{"missing action", required, true, false},
		{"no implicit generation", []string{"--publish-vlan-checkpoint", tx, "--checkpoint-root", "/fixture"}, true, false},
		{"read has no source database", append([]string{"--verify-vlan-checkpoint", tx, "--database", "/fixture/manager.db"}, required...), true, false},
		{"exclusive actions", append([]string{"--publish-vlan-checkpoint", tx, "--verify-vlan-checkpoint", tx}, required...), true, false},
		{"restore cannot be combined", append([]string{"--verify-vlan-checkpoint", tx, "--prepare-fresh-restore-stage", tx}, required...), true, false},
		{"display version cannot trigger publication", append([]string{"--publish-vlan-checkpoint", tx, "--version"}, required...), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet("checkpoint-test", flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			flags.String("database", "/var/lib/ovs-webui/manager/manager.db", "")
			flags.String("prepare-fresh-restore-stage", "", "")
			flags.Bool("version", false, "")
			opts := registerOfflineCheckpointFlags(flags)
			if err := flags.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			request, enabled, err := opts.command()
			if enabled != tc.enabled || (err == nil) != tc.valid {
				t.Fatalf("unexpected parsed command: %+v enabled=%t err=%v", request, enabled, err)
			}
			if enabled && err == nil && (request.Generation != generation || request.TransactionID != tx || request.Root != required[3]) {
				t.Fatalf("parsed wrong source or destination: %+v", request)
			}
		})
	}
}
