package spanningtree

import (
	"encoding/json"
	"reflect"
	"testing"
)

func image(protocol string, values map[string]any) Configuration {
	stp, rstp := protocol == "stp", protocol == "rstp"
	return Configuration{&stp, &rstp, values}
}

func TestSpanningTreeTransitionRequiresBothNativeTimerDirections(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, code string
		before, after        map[string]any
		installed            LocalParameters
	}{
		{"RSTP safe overlap", "rstp", "", map[string]any{}, map[string]any{"rstp-priority": "04096", "rstp-max-age": "22", "rstp-forward-delay": "16"}, LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 20, 15}}},
		{"RSTP forward order", "rstp", "RSTP_FORWARD_TIMER_ORDER_UNPROVEN", map[string]any{"rstp-max-age": "6", "rstp-forward-delay": "4"}, map[string]any{"rstp-max-age": "40", "rstp-forward-delay": "30"}, LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 6, 4}}},
		{"RSTP rollback order", "rstp", "RSTP_ROLLBACK_TIMER_ORDER_UNPROVEN", map[string]any{"rstp-max-age": "40", "rstp-forward-delay": "30"}, map[string]any{"rstp-max-age": "6", "rstp-forward-delay": "4"}, LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 40, 30}}},
		{"STP safe native defaults", "stp", "", map[string]any{}, map[string]any{"stp-priority": "0", "stp-max-age": "40", "stp-forward-delay": "30"}, LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 20, 15}}},
		{"STP explicit hello caveat", "stp", "SPANNING_TREE_TRANSITION_PARAMETERS_UNPROVEN", map[string]any{}, map[string]any{"stp-hello-time": "2"}, LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 20, 15}}},
		{"RSTP rounded original", "rstp", "SPANNING_TREE_TRANSITION_PARAMETERS_UNPROVEN", map[string]any{"rstp-priority": "4097"}, map[string]any{}, LocalParameters{4096, "02:00:00:00:00:02", Timers{2, 20, 15}}},
		{"RSTP retained installed timer", "rstp", "SPANNING_TREE_INSTALLED_ORIGINAL_MISMATCH", map[string]any{"rstp-max-age": "40", "rstp-forward-delay": "30"}, map[string]any{}, LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 6, 30}}},
		{"unset not zero", "stp", "SPANNING_TREE_INSTALLED_ORIGINAL_MISMATCH", map[string]any{}, map[string]any{}, LocalParameters{0, "02:00:00:00:00:02", Timers{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := image(tc.protocol, tc.before), image(tc.protocol, tc.after)
			rawBefore, _ := json.Marshal([]Configuration{before, after})
			got := AssessTransition(before, after, Runtime{Protocol: tc.protocol, BridgeName: "br-tree", Local: tc.installed})
			want := Transition{"compatible", []string{}}
			if tc.code != "" {
				want = Transition{"blocked", []string{tc.code}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
			rawAfter, _ := json.Marshal([]Configuration{before, after})
			if string(rawBefore) != string(rawAfter) {
				t.Fatal("analysis mutated raw strings or materialized native defaults")
			}
		})
	}
}

func TestSpanningTreeTransitionNeverInfersProtocolOrInstalledState(t *testing.T) {
	for _, tc := range []struct {
		before, after Configuration
		runtime       Runtime
	}{
		{image("disabled", map[string]any{}), image("rstp", map[string]any{}), Runtime{}},
		{image("stp", map[string]any{}), image("rstp", map[string]any{}), Runtime{Protocol: "stp", BridgeName: "br-tree"}},
		{image("rstp", map[string]any{}), image("disabled", map[string]any{}), Runtime{Protocol: "rstp", BridgeName: "br-tree"}},
		{image("rstp", nil), image("rstp", map[string]any{}), Runtime{}},
		{Configuration{}, image("rstp", map[string]any{}), Runtime{}},
		{image("rstp", map[string]any{}), image("rstp", map[string]any{}), Runtime{}},
	} {
		if got := AssessTransition(tc.before, tc.after, tc.runtime); got.State != "blocked" || len(got.Checks) != 1 {
			t.Fatal("unproven protocol/state became compatible", got)
		}
	}
}
