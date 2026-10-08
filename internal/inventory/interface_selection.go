package inventory

import (
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

// Selection uses the same immutable view as the page. Names, raw type and OVS
// link observations are independent; none establishes a hardware role.
type interfaceSelection struct {
	Bridge, NativeType, Link string
	HasType                  bool
}

func selectInterfaces(v *view, q url.Values, configuration bool) (interfaceSelection, error) {
	out := interfaceSelection{Bridge: q.Get("bridge_id"), NativeType: q.Get("native_type"), HasType: q.Has("native_type"), Link: q.Get("link_state")}
	for _, key := range []string{"bridge_id", "native_type", "link_state"} {
		if len(q[key]) > 1 {
			return out, apitypes.Fail(400, "AMBIGUOUS_REQUEST")
		}
	}
	if out.HasType && !configuration {
		return out, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	if (q.Has("bridge_id") && !apitypes.ManagementID(out.Bridge)) ||
		utf8.RuneCountInString(out.NativeType) > 64 || !utf8.ValidString(out.NativeType) || strings.ContainsFunc(out.NativeType, func(r rune) bool { return r < 32 || r == 127 }) ||
		(q.Has("link_state") && !slices.Contains([]string{"up", "down", "unknown"}, out.Link)) {
		return out, apitypes.Fail(422, "INVALID_INTERFACE_FILTER")
	}
	if out.Bridge != "" {
		found := false
		for _, b := range v.decision.Bindings {
			_, exists := v.observation.Rows["Bridge"][b.UUID]
			if b.Table == "Bridge" && b.State == "active" && b.ManagementID == out.Bridge && exists {
				found = true
				break
			}
		}
		if !found {
			return out, apitypes.Fail(404, "BRIDGE_SCOPE_NOT_FOUND")
		}
	}
	for _, key := range []string{"type", "link_state"} {
		if key == "type" && !out.HasType || key == "link_state" && out.Link == "" {
			continue
		}
		monitored := false
		for _, table := range v.observation.Schema.Tables {
			if table.Name == "Interface" {
				for _, column := range table.Columns {
					monitored = monitored || column.Name == key && column.Monitored
				}
			}
		}
		if !monitored {
			return out, apitypes.Fail(422, "INTERFACE_FILTER_UNSUPPORTED")
		}
	}
	return out, nil
}

func (f interfaceSelection) matches(v *view, b Binding, row Row) (bool, error) {
	if b.State != "active" {
		return false, nil
	}
	if f.HasType {
		typ, known := row.Values["type"].(string)
		if !known || typ != f.NativeType {
			return false, nil
		}
	}
	if f.Link != "" {
		state := "unknown"
		values, ok := row.Values["link_state"].([]any)
		if ok && len(values) == 1 && (values[0] == "up" || values[0] == "down") {
			state, _ = values[0].(string)
		}
		if state != f.Link {
			return false, nil
		}
	}
	if f.Bridge != "" {
		port, portKnown := parent(v, "Port", "interfaces", b.UUID)
		bridge, bridgeKnown := parent(v, "Bridge", "ports", port.UUID)
		pb, bb := v.decision.Bindings[Key("Port", port.UUID)], v.decision.Bindings[Key("Bridge", bridge.UUID)]
		if !portKnown || !bridgeKnown || pb.State != "active" || bb.State != "active" {
			return false, apitypes.Fail(503, "INVENTORY_RELATION_UNKNOWN")
		}
		return bb.ManagementID == f.Bridge, nil
	}
	return true, nil
}

// Compare digit runs without parsing integers: native names may contain more
// digits than any machine integer. A total bytewise tie-break keeps pages stable.
func naturalNameCompare(a, b string) int {
	x, y := strings.ToLower(a), strings.ToLower(b)
	for i, j := 0, 0; i < len(x) || j < len(y); {
		if i == len(x) {
			return -1
		}
		if j == len(y) {
			return 1
		}
		digit := func(c byte) bool { return c >= '0' && c <= '9' }
		if digit(x[i]) && digit(y[j]) {
			endI, endJ := i, j
			for endI < len(x) && digit(x[endI]) {
				endI++
			}
			for endJ < len(y) && digit(y[endJ]) {
				endJ++
			}
			xn, yn := strings.TrimLeft(x[i:endI], "0"), strings.TrimLeft(y[j:endJ], "0")
			if len(xn) != len(yn) {
				if len(xn) < len(yn) {
					return -1
				}
				return 1
			}
			if n := strings.Compare(xn, yn); n != 0 {
				return n
			}
			if endI-i != endJ-j {
				if endI-i < endJ-j {
					return -1
				}
				return 1
			}
			i, j = endI, endJ
			continue
		}
		if x[i] != y[j] {
			if x[i] < y[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	return strings.Compare(a, b)
}
