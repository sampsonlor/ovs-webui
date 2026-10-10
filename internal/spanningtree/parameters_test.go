package spanningtree

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestBasicBridgeNativeParameterBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		values     map[string]any
	}{
		{"defaults", "", map[string]any{}},
		{"minimum STP timers", "", map[string]any{"stp-priority": "0", "stp-hello-time": "1", "stp-max-age": "6", "stp-forward-delay": "4"}},
		{"maximum STP timers with native hello default", "", map[string]any{"stp-priority": "65535", "stp-max-age": "40", "stp-forward-delay": "30"}},
		{"minimum RSTP timers", "", map[string]any{"rstp-priority": "0", "rstp-max-age": "6", "rstp-forward-delay": "4"}},
		{"maximum RSTP timers", "", map[string]any{"rstp-priority": "61440", "rstp-max-age": "40", "rstp-forward-delay": "30"}},
		{"STP priority overflow", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"stp-priority": "65536"}},
		{"RSTP priority overflow", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"rstp-priority": "65535"}},
		{"RSTP priority rounding", "RSTP_PRIORITY_MULTIPLE_4096", map[string]any{"rstp-priority": "4097"}},
		{"RSTP zero rounding", "RSTP_PRIORITY_MULTIPLE_4096", map[string]any{"rstp-priority": "1"}},
		{"STP hello low", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"stp-hello-time": "0"}},
		{"STP hello high", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"stp-hello-time": "11"}},
		{"STP age low", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"stp-max-age": "5"}},
		{"RSTP age high", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"rstp-max-age": "41"}},
		{"STP delay low", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"stp-forward-delay": "3"}},
		{"RSTP delay high", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"rstp-forward-delay": "31"}},
		{"STP clamps max age", "STP_MAX_AGE_HELLO_RELATION", map[string]any{"stp-hello-time": "10", "stp-max-age": "20"}},
		{"STP clamps delay", "STP_FORWARD_DELAY_MAX_AGE_RELATION", map[string]any{"stp-forward-delay": "4", "stp-max-age": "20"}},
		{"RSTP retains delay", "RSTP_FORWARD_DELAY_MAX_AGE_RELATION", map[string]any{"rstp-forward-delay": "4", "rstp-max-age": "20"}},
		{"fraction", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"stp-priority": "1.5"}},
		{"negative", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"rstp-max-age": "-6"}},
		{"hexadecimal", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"rstp-priority": "0x1000"}},
		{"whitespace", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"stp-max-age": " 20"}},
		{"non string", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"stp-priority": 4096}},
		{"empty string", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"rstp-priority": ""}},
		{"exact integer overflow", "SPANNING_TREE_PARAMETER_RANGE", map[string]any{"stp-priority": "18446744073709551616"}},
		{"oversized value", "SPANNING_TREE_INTEGER_REQUIRED", map[string]any{"stp-priority": strings.Repeat("1", 129)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stp, rstp := true, false
			v := Validate(&stp, &rstp, tc.values)
			if tc.code == "" {
				if v.State != "valid" || len(v.Checks) != 0 {
					t.Fatal(v)
				}
			} else if v.State != "invalid" || !slices.ContainsFunc(v.Checks, func(c Check) bool { return c.Code == tc.code }) {
				t.Fatal(v)
			}
		})
	}
}

func TestBasicValidationPreservesDefaultsRawValuesAndIndependentScope(t *testing.T) {
	stp, rstp := false, true
	other := map[string]any{"rstp-priority": "04096", "stp-system-id": "synthetic-unvalidated", "rstp-force-protocol-version": "unverified", "private-label": "must-not-appear"}
	before, _ := json.Marshal(other)
	v := Validate(&stp, &rstp, other)
	after, _ := json.Marshal(other)
	if string(before) != string(after) || len(other) != 4 || v.State != "valid" || v.Scope != Scope || v.Version != Version {
		t.Fatal("raw configuration changed or advanced keys claimed", v, other)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "must-not-appear") || strings.Contains(string(b), "04096") {
		t.Fatal("raw or unrelated configuration leaked", string(b))
	}
	other["stp-hello-time"] = "10"
	if Validate(&stp, &rstp, other).State != "invalid" {
		t.Fatal("inactive STP timers silently accepted")
	}
}

func TestMissingNativeConfigurationAndMutualExclusion(t *testing.T) {
	f, tr := false, true
	for _, v := range []Validation{Validate(nil, &f, map[string]any{}), Validate(&f, nil, map[string]any{}), Validate(&f, &tr, nil)} {
		if v.State != "unknown" || len(v.Checks) != 0 {
			t.Fatal("unknown became a parameter oracle", v)
		}
	}
	v := Validate(&tr, &tr, map[string]any{})
	if v.State != "invalid" || len(v.Checks) != 1 || v.Checks[0].Code != "STP_RSTP_MUTUALLY_EXCLUSIVE" {
		t.Fatal(v)
	}
	if v = Validate(&f, &f, map[string]any{}); v.State != "valid" {
		t.Fatal("disabled is a known native configuration", v)
	}
}

func TestExplicitSTPHelloTimeCannotInferInstalledUnits(t *testing.T) {
	stp, rstp := true, false
	for _, hello := range []string{"2", "10"} {
		v := Validate(&stp, &rstp, map[string]any{"stp-hello-time": hello, "stp-max-age": "22", "stp-forward-delay": "12"})
		if v.State != "runtime-unverified" || len(v.Checks) != 1 || v.Checks[0].Code != "STP_HELLO_TIME_NATIVE_UNIT_CAVEAT" {
			t.Fatal("documented seconds confused with installed timer support", v)
		}
	}
	stp, rstp = false, true
	if v := Validate(&stp, &rstp, map[string]any{"stp-hello-time": "2"}); v.State != "runtime-unverified" {
		t.Fatal("inactive hello time bypassed runtime gate", v)
	}
}
