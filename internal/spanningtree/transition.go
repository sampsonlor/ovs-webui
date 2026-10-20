package spanningtree

import "strconv"

// Configuration is private transition-analysis input, not a public write DTO.
// Nil maps/flags remain unknown; absent keys retain native defaults for analysis.
type Configuration struct {
	STP, RSTP *bool
	Other     map[string]any
}

type Transition struct {
	State  string   `json:"state"`
	Checks []string `json:"checks"`
}

// AssessTransition only admits a single same-protocol parameter transition
// whose original local parameters match independently observed daemon values,
// and whose forward AND reverse setter order can install the exact endpoints.
// It grants no authority and does not qualify identity, freshness or Applied.
// Protocol enable/disable/switch and multi-step timer recovery remain separate.
func AssessTransition(before, after Configuration, installed Runtime) Transition {
	out := Transition{State: "blocked", Checks: []string{}}
	for _, image := range []Configuration{before, after} {
		if Validate(image.STP, image.RSTP, image.Other).State != "valid" {
			out.Checks = append(out.Checks, "SPANNING_TREE_TRANSITION_PARAMETERS_UNPROVEN")
			return out
		}
	}
	protocol := func(c Configuration) string {
		if *c.STP {
			return "stp"
		}
		if *c.RSTP {
			return "rstp"
		}
		return "disabled"
	}
	p := protocol(before)
	if p == "disabled" || protocol(after) != p || installed.Protocol != p || !RuntimeName(installed.BridgeName) {
		out.Checks = append(out.Checks, "SPANNING_TREE_PROTOCOL_TRANSITION_UNPROVEN")
		return out
	}
	values := func(c Configuration) LocalParameters {
		number := func(key string, fallback int) int {
			if raw, ok := c.Other[key].(string); ok {
				n, _ := strconv.Atoi(raw) // Validate already bounded this exact value.
				return n
			}
			return fallback
		}
		hello := 2
		if p == "stp" {
			hello = number("stp-hello-time", 2)
		}
		return LocalParameters{Priority: number(p+"-priority", 32768), Timers: Timers{hello, number(p+"-max-age", 20), number(p+"-forward-delay", 15)}}
	}
	b, a := values(before), values(after)
	b.SystemID = installed.Local.SystemID
	if b != installed.Local || installed.Local.SystemID == "" {
		out.Checks = append(out.Checks, "SPANNING_TREE_INSTALLED_ORIGINAL_MISMATCH")
		return out
	}
	if p == "rstp" {
		// Native order is max_age then forward_delay. Each setter checks the
		// CURRENT peer, so valid final pairs alone cannot qualify rollback.
		if a.MaxAge > 2*(b.ForwardDelay-1) {
			out.Checks = append(out.Checks, "RSTP_FORWARD_TIMER_ORDER_UNPROVEN")
		}
		if b.MaxAge > 2*(a.ForwardDelay-1) {
			out.Checks = append(out.Checks, "RSTP_ROLLBACK_TIMER_ORDER_UNPROVEN")
		}
	}
	if len(out.Checks) == 0 {
		out.State = "compatible"
	}
	return out
}
