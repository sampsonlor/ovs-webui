package spanningtree

import "testing"

func TestExecutionProtocolChangesCheckInstalledStateAndBothCreationDirections(t *testing.T) {
	configuration := func(p string) Configuration {
		s, r := p == "stp", p == "rstp"
		return Configuration{STP: &s, RSTP: &r, Other: map[string]any{}}
	}
	for _, before := range []string{"disabled", "stp", "rstp"} {
		for _, after := range []string{"disabled", "stp", "rstp"} {
			b, a := configuration(before), configuration(after)
			r := Runtime{Protocol: before, BridgeName: "br-local"}
			if before != "disabled" {
				r.Local = ExpectedLocal(b)
				r.Local.SystemID = "02:00:00:00:00:01"
			}
			if got := AssessExecutionTransition(b, a, r); got.State != "compatible" {
				t.Fatal(before, after, got)
			}
			if before != "disabled" {
				r.Local.MaxAge++
			} else {
				r.Protocol = "stp"
			}
			if got := AssessExecutionTransition(b, a, r); got.State != "blocked" {
				t.Fatal("unproven installed original", before, after, got)
			}
		}
	}
	for _, reverse := range []bool{false, true} {
		b, a := configuration("disabled"), configuration("rstp")
		a.Other["rstp-max-age"], a.Other["rstp-forward-delay"] = "40", "21"
		r := Runtime{Protocol: "disabled", BridgeName: "br-local"}
		if reverse {
			b, a = a, b
			r.Protocol = "rstp"
			r.Local = ExpectedLocal(b)
			r.Local.SystemID = "02:00:00:00:00:01"
		}
		if got := AssessExecutionTransition(b, a, r); got.State != "blocked" || got.Checks[0] != "RSTP_INITIAL_TIMER_ORDER_UNPROVEN" {
			t.Fatal("silent native initialization clamp admitted", reverse, got)
		}
	}
}
