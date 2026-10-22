package spanningtree

import "strconv"

func Protocol(c Configuration) string {
	if c.STP == nil || c.RSTP == nil || *c.STP && *c.RSTP {
		return "unknown"
	}
	if *c.STP {
		return "stp"
	}
	if *c.RSTP {
		return "rstp"
	}
	return "disabled"
}

func ExpectedLocal(c Configuration) LocalParameters {
	p := Protocol(c)
	number := func(key string, fallback int) int {
		if raw, ok := c.Other[key].(string); ok {
			n, _ := strconv.Atoi(raw)
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

// AssessExecutionTransition extends the qualified same-engine setter analysis
// to complete native protocol changes. It never promises packet continuity or
// network convergence. Each newly created RSTP engine starts with forward delay
// 15; its max-age setter runs first and can silently retain the default. Both
// forward and compensating creation must therefore be installable in one pass.
func AssessExecutionTransition(before, after Configuration, installed Runtime) Transition {
	blocked := func(code string) Transition { return Transition{State: "blocked", Checks: []string{code}} }
	for _, c := range []Configuration{before, after} {
		if Validate(c.STP, c.RSTP, c.Other).State != "valid" {
			return blocked("SPANNING_TREE_TRANSITION_PARAMETERS_UNPROVEN")
		}
	}
	b, a := Protocol(before), Protocol(after)
	if b != "disabled" {
		if check := AssessTransition(before, before, installed); check.State != "compatible" {
			return check
		}
	} else if installed.Protocol != "disabled" || !RuntimeName(installed.BridgeName) {
		return blocked("SPANNING_TREE_INSTALLED_ORIGINAL_MISMATCH")
	}
	if b == a && b != "disabled" {
		return AssessTransition(before, after, installed)
	}
	return AssessSetterOrder(before, after)
}

// AssessSetterOrder is configuration-only validation. It never manufactures an
// installed sample; execution must separately match the real native original.
func AssessSetterOrder(before, after Configuration) Transition {
	blocked := func(code string) Transition { return Transition{State: "blocked", Checks: []string{code}} }
	for _, c := range []Configuration{before, after} {
		if Validate(c.STP, c.RSTP, c.Other).State != "valid" {
			return blocked("SPANNING_TREE_TRANSITION_PARAMETERS_UNPROVEN")
		}
	}
	b, a := Protocol(before), Protocol(after)
	if b == a {
		if b == "rstp" {
			old, next := ExpectedLocal(before), ExpectedLocal(after)
			if next.MaxAge > 2*(old.ForwardDelay-1) {
				return blocked("RSTP_FORWARD_TIMER_ORDER_UNPROVEN")
			}
			if old.MaxAge > 2*(next.ForwardDelay-1) {
				return blocked("RSTP_ROLLBACK_TIMER_ORDER_UNPROVEN")
			}
		}
		return Transition{State: "compatible", Checks: []string{}}
	}
	for _, c := range []Configuration{before, after} {
		if Protocol(c) == "rstp" && ExpectedLocal(c).MaxAge > 28 {
			return blocked("RSTP_INITIAL_TIMER_ORDER_UNPROVEN")
		}
	}
	return Transition{State: "compatible", Checks: []string{}}
}
