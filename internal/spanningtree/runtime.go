package spanningtree

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxRuntimeBytes = 128 << 10

// Timers are seconds reported by the daemon, not configured map strings.
type Timers struct {
	HelloTime    int `json:"hello_time"`
	MaxAge       int `json:"max_age"`
	ForwardDelay int `json:"forward_delay"`
}

type LocalParameters struct {
	Priority int    `json:"priority"`
	SystemID string `json:"system_id"`
	Timers
}

// Runtime is a name-scoped daemon observation, not immutable OVSDB identity,
// execution authority, or Applied proof. STP's output labels are misleading:
// its Root ID section contains LOCAL bridge timers but ROOT identity; its
// Bridge ID section contains LOCAL identity but operational (possibly learned)
// timers. RSTP's Bridge ID section contains local identity AND local timers.
type Runtime struct {
	Protocol          string          `json:"protocol"`
	BridgeName        string          `json:"bridge_name"`
	Local             LocalParameters `json:"local"`
	OperationalTimers *Timers         `json:"operational_timers,omitempty"`
}

// RuntimeName deliberately admits the basic Linux Bridge-name scope only.
// The value is always one JSON argument, never command text or a shell word.
func RuntimeName(name string) bool {
	if len(name) == 0 || len(name) > 15 {
		return false
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '_' || c == '-' || c == '.') {
			continue
		}
		return false
	}
	return true
}

// ParseRuntime accepts one complete, bounded upstream text response. Missing,
// duplicate, malformed or ambiguous fields fail closed; no configured default
// fills a missing native value. Root and local identities are never combined.
func ParseRuntime(protocol, name, text string) (Runtime, error) {
	bad := errors.New("SPANNING_TREE_RUNTIME_INVALID")
	if protocol != "stp" && protocol != "rstp" || !RuntimeName(name) || len(text) > MaxRuntimeBytes || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\r") {
		return Runtime{}, bad
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 18 || len(lines) > 1024 || lines[0] != "---- "+name+" ----" {
		return Runtime{}, bad
	}
	sections := map[string]map[string]string{}
	section := ""
	footer := false
	for _, line := range lines[1:] {
		if len(line) > 1024 || strings.HasPrefix(line, "---- ") {
			return Runtime{}, bad
		}
		if line == "Root ID:" || line == "Bridge ID:" {
			if footer || sections[line] != nil || line == "Root ID:" && len(sections) != 0 || line == "Bridge ID:" && sections["Root ID:"] == nil {
				return Runtime{}, bad
			}
			section = line
			sections[section] = map[string]string{}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "Interface" {
			if footer || section != "Bridge ID:" || strings.Join(fields, " ") != "Interface Role State Cost Pri.Nbr" {
				return Runtime{}, bad
			}
			footer = true
			continue
		}
		if footer {
			// Port display rows are bounded but never interpreted as timers.
			if strings.HasPrefix(fields[0], "stp-") || strings.HasSuffix(line, "ID:") {
				return Runtime{}, bad
			}
			continue
		}
		if section == "" || !strings.HasPrefix(line, "  ") {
			return Runtime{}, bad
		}
		if section == "Root ID:" && (strings.TrimSpace(line) == "This bridge is the root" || fields[0] == "root-port" || fields[0] == "root-path-cost") {
			continue
		}
		if len(fields) != 2 || sections[section][fields[0]] != "" {
			return Runtime{}, bad
		}
		switch fields[0] {
		case "stp-priority", "stp-system-id", "stp-hello-time", "stp-max-age", "stp-fwd-delay":
			sections[section][fields[0]] = fields[1]
		default:
			return Runtime{}, bad
		}
	}
	if !footer || len(sections) != 2 {
		return Runtime{}, bad
	}
	decode := func(values map[string]string) (LocalParameters, error) {
		var out LocalParameters
		if len(values) != 5 {
			return out, bad
		}
		mac, err := net.ParseMAC(values["stp-system-id"])
		if err != nil || len(mac) != 6 || mac.String() != values["stp-system-id"] {
			return out, bad
		}
		out.SystemID = mac.String()
		for key, target := range map[string]*int{"stp-priority": &out.Priority, "stp-hello-time": &out.HelloTime, "stp-max-age": &out.MaxAge, "stp-fwd-delay": &out.ForwardDelay} {
			raw := values[key]
			if key != "stp-priority" {
				if !strings.HasSuffix(raw, "s") {
					return LocalParameters{}, bad
				}
				raw = strings.TrimSuffix(raw, "s")
			}
			if raw == "" || len(raw) > 5 {
				return LocalParameters{}, bad
			}
			for _, c := range raw {
				if c < '0' || c > '9' {
					return LocalParameters{}, bad
				}
			}
			n, err := strconv.ParseUint(raw, 10, 16)
			if err != nil {
				return LocalParameters{}, bad
			}
			*target = int(n)
		}
		return out, nil
	}
	root, err := decode(sections["Root ID:"])
	if err != nil {
		return Runtime{}, err
	}
	bridge, err := decode(sections["Bridge ID:"])
	if err != nil {
		return Runtime{}, err
	}
	out := Runtime{Protocol: protocol, BridgeName: name, Local: bridge}
	if protocol == "stp" {
		out.Local.Timers = root.Timers
		out.OperationalTimers = &bridge.Timers
	}
	return out, nil
}
