package inventory

import (
	"context"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestSpanningTreeExecutionRootGrantNeverInheritsPreparationOrTopology(t *testing.T) {
	s, _, _, _, b := treePreparationFixture(t)
	ctx := context.Background()
	for _, grant := range []func([]string) error{s.SetLocalSpanningTreeBridges, s.SetLocalTopologyObjects} {
		if err := grant([]string{b.ManagementID}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SpanningTreeExecutionView(ctx, b); err == nil || !strings.Contains(err.Error(), "SPANNING_TREE_EXECUTION_AUTHORITY_REQUIRED") {
			t.Fatal("existing grant became execution authority", err)
		}
	}
	if err := s.SetLocalSpanningTreeExecutionBridges([]string{b.ManagementID}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SpanningTreeExecutionView(ctx, b)
	if err != nil || v.Candidate.SpanningTrees[b.ManagementID].Binding != b {
		t.Fatal(v, err)
	}
	ordinary, err := s.ExecutionView(ctx, nil)
	if err != nil || ordinary.Candidate.Policy == v.Candidate.Policy {
		t.Fatal("execution policy missing from private fingerprint", err)
	}
	for _, bad := range [][]string{{b.ManagementID, b.ManagementID}, {"invalid"}, make([]string, 129)} {
		if s.SetLocalSpanningTreeExecutionBridges(bad) == nil {
			t.Fatal("invalid policy accepted")
		}
		if _, err := s.SpanningTreeExecutionView(ctx, b); err != nil {
			t.Fatal("invalid replacement destroyed the previous policy", err)
		}
	}
	if err := s.SetLocalSpanningTreeExecutionBridges(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SpanningTreeExecutionView(ctx, b); err == nil {
		t.Fatal("revocation lost")
	}
}

func TestSpanningTreeExecutionViewRejectsIdentityGraphAndProviderChanges(t *testing.T) {
	for _, scenario := range []string{"generation", "uuid", "management-id", "table", "root-grant-only", "controller", "stale", "provider"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, d, _, b := treePreparationFixture(t)
			if err := s.SetLocalSpanningTreeExecutionBridges([]string{b.ManagementID}); err != nil {
				t.Fatal(err)
			}
			if scenario != "root-grant-only" {
				if err := s.SetLocalSpanningTreeBridges([]string{b.ManagementID}); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "generation":
				b.Generation = repository.NewID()
			case "uuid":
				b.OVSUUID = repository.NewID()
			case "management-id":
				b.ManagementID = repository.NewID()
			case "table":
				b.Table = "Port"
			case "controller":
				o.Rows["Bridge"][b.OVSUUID].Configuration["controller"] = []any{repository.NewID()}
				s.install(o, d)
			case "stale":
				o.Evidence.ObservedAt = o.Evidence.ObservedAt.Add(-2 * FreshFor)
				s.install(o, d)
			case "provider":
				s.Unavailable("OVSDB_UNAVAILABLE")
			}
			if _, err := s.SpanningTreeExecutionView(context.Background(), b); err == nil {
				t.Fatal("unqualified immutable scope accepted", scenario)
			}
		})
	}
}

func TestSpanningTreeExecutionPolicyDetectsRevokeAndRegrant(t *testing.T) {
	s, _, _, _, b := treePreparationFixture(t)
	ctx := context.Background()
	for _, grant := range []func([]string) error{s.SetLocalSpanningTreeBridges, s.SetLocalSpanningTreeExecutionBridges} {
		if err := grant([]string{b.ManagementID}); err != nil {
			t.Fatal(err)
		}
	}
	for _, grant := range []func([]string) error{s.SetLocalSpanningTreeBridges, s.SetLocalSpanningTreeExecutionBridges} {
		before, err := s.SpanningTreeExecutionView(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		if err := grant(nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SpanningTreeExecutionView(ctx, b); err == nil {
			t.Fatal("revocation was not enforced")
		}
		if err := grant([]string{b.ManagementID}); err != nil {
			t.Fatal(err)
		}
		after, err := s.SpanningTreeExecutionView(ctx, b)
		if err != nil || before.Candidate.Policy == after.Candidate.Policy {
			t.Fatal("regrant resurrected an in-flight scope", err)
		}
	}
}
