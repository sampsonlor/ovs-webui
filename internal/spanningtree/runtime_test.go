package spanningtree

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func runtimeText(root, bridge LocalParameters) string {
	section := func(p LocalParameters) string {
		return fmt.Sprintf("  stp-priority    %d\n  stp-system-id   %s\n  stp-hello-time  %ds\n  stp-max-age     %ds\n  stp-fwd-delay   %ds\n", p.Priority, p.SystemID, p.HelloTime, p.MaxAge, p.ForwardDelay)
	}
	return "---- br-tree ----\nRoot ID:\n" + section(root) + "  root-port       upstream\n  root-path-cost  2000\n\nBridge ID:\n" + section(bridge) + "\n  Interface  Role       State      Cost  Pri.Nbr\n  ---------- ---------- ---------- ----- -------\n  upstream   root       forwarding 2000  128.1\n\n"
}

func TestSpanningTreeRuntimeSeparatesLocalIdentityAndTimerSources(t *testing.T) {
	root := LocalParameters{4096, "02:00:00:00:00:01", Timers{1, 6, 4}}
	bridge := LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 20, 15}}
	for _, protocol := range []string{"stp", "rstp"} {
		r, err := ParseRuntime(protocol, "br-tree", runtimeText(root, bridge))
		if err != nil {
			t.Fatal(err)
		}
		want := bridge
		if protocol == "stp" {
			want.Timers = root.Timers
			if r.OperationalTimers == nil || *r.OperationalTimers != bridge.Timers {
				t.Fatal("operational STP timers lost", r)
			}
		} else if r.OperationalTimers != nil {
			t.Fatal("RSTP operational timers invented", r)
		}
		if r.Local != want || r.Protocol != protocol || r.BridgeName != "br-tree" {
			t.Fatal("root identity or learned timers became local configuration", r)
		}
	}
}

func TestSpanningTreeRuntimeRejectsAmbiguityAndMissingNativeValues(t *testing.T) {
	p := LocalParameters{32768, "02:00:00:00:00:02", Timers{2, 20, 15}}
	valid := runtimeText(p, p)
	for name, text := range map[string]string{
		"wrong bridge":       strings.Replace(valid, "br-tree", "br-other", 1),
		"extra bridge":       valid + valid,
		"missing timer":      strings.ReplaceAll(valid, "  stp-max-age     20s\n", ""),
		"duplicate timer":    strings.Replace(valid, "  stp-max-age     20s", "  stp-max-age     20s\n  stp-max-age     6s", 1),
		"duplicate section":  valid + "Bridge ID:\n",
		"reordered sections": strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(valid, "Root ID:", "Temp ID:"), "Bridge ID:", "Root ID:"), "Temp ID:", "Bridge ID:"),
		"wrong units":        strings.ReplaceAll(valid, "20s", "20000ms"),
		"signed":             strings.ReplaceAll(valid, "20s", "+20s"),
		"overflow":           strings.ReplaceAll(valid, "32768", "65536"),
		"wrong mac":          strings.ReplaceAll(valid, p.SystemID, "02:00:00:00:00:02:00:00"),
		"unknown field":      strings.ReplaceAll(valid, "stp-priority", "rstp-priority"),
		"no table":           strings.Split(valid, "  Interface")[0],
		"timers after table": valid + "  stp-max-age 20s\n",
		"unknown root":       "---- br-tree ----\nRoot ID:\nunknown root port\n",
		"nul":                valid + "\x00",
		"invalid utf8":       valid + "\xff",
		"oversize":           valid + strings.Repeat(" ", MaxRuntimeBytes),
		"long line":          valid + strings.Repeat("x", 1025),
		"too many lines":     valid + strings.Repeat("\n", 1025),
	} {
		t.Run(name, func(t *testing.T) {
			if r, err := ParseRuntime("stp", "br-tree", text); err == nil || !reflect.DeepEqual(r, Runtime{}) {
				t.Fatal("malformed native data became a partial/default sample", r, err)
			}
		})
	}
	for _, name := range []string{"", "-all", "br x", "br\nBridge ID:", "br;x", strings.Repeat("x", 16)} {
		if RuntimeName(name) {
			t.Fatal("argument outside basic name scope admitted", name)
		}
	}
	if _, err := ParseRuntime("stp/tcn", "br-tree", valid); err == nil {
		t.Fatal("write method admitted")
	}
}
