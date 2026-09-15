package inventory

import (
	domain "github.com/sampsonlor/ovs-webui/internal/inventory"
	"testing"
	"time"
)

func evidence() domain.Evidence {
	return domain.Evidence{Endpoint: "/synthetic/db.sock", Database: "Open_vSwitch", Schema: "schema-digest", Root: "root", Anchors: []string{"Bridge/anchor"}, Boot: "boot", Peer: "pid:start", ObservedAt: time.Unix(100, 0), Continuous: true, File: domain.FileWitness{Available: true, Device: 1, Inode: 2, Size: 300, Offset: 0, Length: 300, Digest: "digest", PriorMatches: true, ServerHasFile: true}}
}
func TestLifecycleEvidenceClassification(t *testing.T) {
	old := domain.Decision{Generation: "generation", State: "confirmed", Evidence: evidence()}
	cases := []struct {
		name          string
		change        func(*domain.Evidence)
		state, reason string
	}{
		{"normal", func(e *domain.Evidence) { e.ObservedAt = e.ObservedAt.Add(time.Second) }, "continued", "monitor-continuity"},
		{"process restart", func(e *domain.Evidence) { e.Peer = "other-pid"; e.Continuous = false }, "continued", "process-restarted-database-continued"},
		{"same peer reconnect", func(e *domain.Evidence) { e.Continuous = false }, "continued", "database-reconnected"},
		{"same root restored copy", func(e *domain.Evidence) { e.File.Inode = 42 }, "reconciliation-required", "database-file-replaced-or-rewound"},
		{"same inode rewind", func(e *domain.Evidence) { e.File.PriorMatches = false }, "reconciliation-required", "database-file-replaced-or-rewound"},
		{"root and anchor break", func(e *domain.Evidence) { e.Root = "new-root"; e.Anchors = []string{"Bridge/new"} }, "new", "native-identity-break"},
		{"root conflicts with anchors", func(e *domain.Evidence) { e.Root = "new-root" }, "reconciliation-required", "root-anchor-conflict"},
		{"schema change", func(e *domain.Evidence) { e.Schema = "other" }, "reconciliation-required", "schema-changed"},
		{"long loss", func(e *domain.Evidence) { e.ObservedAt = e.ObservedAt.Add(31 * time.Second) }, "reconciliation-required", "observation-gap"},
		{"clock backwards", func(e *domain.Evidence) { e.ObservedAt = e.ObservedAt.Add(-time.Second) }, "reconciliation-required", "observation-gap"},
		{"host boot", func(e *domain.Evidence) { e.Boot = "other" }, "reconciliation-required", "host-restarted"},
		{"unproven file", func(e *domain.Evidence) { e.File.ServerHasFile = false }, "reconciliation-required", "file-evidence-unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := evidence()
			c.change(&e)
			state, reason := Classify(old, e, false)
			if state != c.state || reason != c.reason {
				t.Fatalf("%s %s", state, reason)
			}
		})
	}
	if state, _ := Classify(old, evidence(), true); state != "reconciliation-required" {
		t.Fatal("restore reused identity")
	}
	old.State = "reconciliation-required"
	old.Reason = "latched"
	if state, reason := Classify(old, evidence(), false); state != "reconciliation-required" || reason != "latched" {
		t.Fatal("uncertain identity silently healed")
	}
}
