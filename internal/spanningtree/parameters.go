// Package spanningtree validates native Bridge parameters. It cannot grant
// authority, stage a Candidate, execute a transaction or prove daemon state.
package spanningtree

import "strconv"

const Version = "bridge-basic-v1"
const Scope = "bridge-basic-parameters"

type Check struct {
	Code   string   `json:"code"`
	Fields []string `json:"fields"`
}

type Validation struct {
	Version string  `json:"version"`
	Scope   string  `json:"scope"`
	State   string  `json:"state"`
	Checks  []Check `json:"checks"`
}

func Unavailable(state string) Validation {
	return Validation{Version: Version, Scope: Scope, State: state, Checks: []Check{}}
}

// Validate checks both protocols, including inactive configuration. Defaults
// are inputs to parameter rules only; they are never written into the observed
// map, and a valid result is not evidence of installed parameters. In particular
// OVS may round RSTP priority, clamp STP timers or retain previous RSTP timers.
// Those cases are rejected rather than silently normalizing the user's intent.
// The seven basic Bridge keys are the complete scope of this validator. Port
// parameters, advanced Bridge keys, graph eligibility and authority are separate
// gates, as are transitions from the actual installed RSTP timer values.
func Validate(stp, rstp *bool, other map[string]any) Validation {
	v := Unavailable("valid")
	add := func(code string, fields ...string) {
		v.Checks = append(v.Checks, Check{Code: code, Fields: fields})
		v.State = "invalid"
	}
	if stp == nil || rstp == nil || other == nil {
		v.State = "unknown"
		return v
	}
	if *stp && *rstp {
		add("STP_RSTP_MUTUALLY_EXCLUSIVE", "stp_enable", "rstp_enable")
	}
	read := func(key string, fallback, min, max int) (int, bool) {
		raw, present := other[key]
		if !present {
			return fallback, true
		}
		text, ok := raw.(string)
		if !ok || len(text) == 0 || len(text) > 128 {
			add("SPANNING_TREE_INTEGER_REQUIRED", key)
			return 0, false
		}
		// OVS parses several alternative numeric representations. Basic Manage
		// deliberately requires decimal digits, preserving the raw observation.
		for _, c := range text {
			if c < '0' || c > '9' {
				add("SPANNING_TREE_INTEGER_REQUIRED", key)
				return 0, false
			}
		}
		n, err := strconv.ParseUint(text, 10, 64)
		if err != nil || n < uint64(min) || n > uint64(max) {
			add("SPANNING_TREE_PARAMETER_RANGE", key)
			return 0, false
		}
		return int(n), true
	}
	read("stp-priority", 32768, 0, 65535)
	hello, hOK := read("stp-hello-time", 2, 1, 10)
	age, aOK := read("stp-max-age", 20, 6, 40)
	delay, dOK := read("stp-forward-delay", 15, 4, 30)
	if hOK && aOK && age < 2*(hello+1) {
		add("STP_MAX_AGE_HELLO_RELATION", "stp-max-age", "stp-hello-time")
	}
	if aOK && dOK && age > 2*(delay-1) {
		add("STP_FORWARD_DELAY_MAX_AGE_RELATION", "stp-forward-delay", "stp-max-age")
	}
	priority, pOK := read("rstp-priority", 32768, 0, 61440)
	if pOK && priority%4096 != 0 {
		add("RSTP_PRIORITY_MULTIPLE_4096", "rstp-priority")
	}
	age, aOK = read("rstp-max-age", 20, 6, 40)
	delay, dOK = read("rstp-forward-delay", 15, 4, 30)
	if aOK && dOK && age > 2*(delay-1) {
		add("RSTP_FORWARD_DELAY_MAX_AGE_RELATION", "rstp-forward-delay", "rstp-max-age")
	}
	return v
}
