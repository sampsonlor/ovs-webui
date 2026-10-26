// Package mirror models native Mirror selectors and output configuration.
// Port references use immutable management IDs; only the provider translates
// them into OVSDB UUIDs after checking the captured Bridge graph.
package mirror

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const MaxSelectedPorts = 32
const MaxSelectedVLANs = 256

type Configuration struct {
	SelectAll        bool     `json:"select_all"`
	SourcePorts      []string `json:"source_port_ids"`
	DestinationPorts []string `json:"destination_port_ids"`
	VLANs            []string `json:"select_vlans"`
	OutputPort       *string  `json:"output_port_id"`
	OutputVLAN       *string  `json:"output_vlan"`
	Snaplen          *string  `json:"snaplen"`
}

func ValidName(name string) bool {
	return name != "" && len(name) <= 64 && utf8.ValidString(name) && strings.TrimSpace(name) == name && !strings.ContainsAny(name, ",:") && !strings.ContainsFunc(name, unicode.IsControl)
}

// Numeric strings preserve the native decimal value without rounding,
// clamping, default insertion or a lossy floating-point conversion.
func decimal(value string, min, max uint64) bool {
	if value == "" || len(value) > 5 || len(value) > 1 && value[0] == '0' {
		return false
	}
	var n uint64
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + uint64(r-'0')
	}
	return n >= min && n <= max
}

func Validate(c Configuration) error {
	if c.SourcePorts == nil || c.DestinationPorts == nil || c.VLANs == nil || len(c.SourcePorts) > MaxSelectedPorts || len(c.DestinationPorts) > MaxSelectedPorts || len(c.VLANs) > MaxSelectedVLANs {
		return errors.New("MIRROR_SELECTOR_BUDGET_OR_UNKNOWN")
	}
	for _, ports := range [][]string{c.SourcePorts, c.DestinationPorts} {
		seen := map[string]bool{}
		for _, id := range ports {
			if !apitypes.ManagementID(id) || seen[id] {
				return errors.New("MIRROR_PORT_REFERENCE_INVALID")
			}
			seen[id] = true
		}
	}
	if !c.SelectAll && len(c.SourcePorts)+len(c.DestinationPorts) == 0 {
		return errors.New("MIRROR_SELECTION_EMPTY")
	}
	// select_all overrides the explicit Port lists. VLAN selection still applies
	// independently in the native implementation.
	if c.SelectAll && len(c.SourcePorts)+len(c.DestinationPorts) != 0 {
		return errors.New("MIRROR_SELECT_ALL_CONFLICT")
	}
	seen := map[string]bool{}
	for _, value := range c.VLANs {
		// Native VLAN zero selects untagged traffic. Reserved VLAN 4095 remains
		// observable but is outside the qualified management range.
		if !decimal(value, 0, 4094) || seen[value] {
			return errors.New("MIRROR_VLAN_SELECTOR_INVALID")
		}
		seen[value] = true
	}
	if (c.OutputPort == nil) == (c.OutputVLAN == nil) {
		return errors.New("MIRROR_EXACTLY_ONE_OUTPUT_REQUIRED")
	}
	if c.OutputPort != nil && (!apitypes.ManagementID(*c.OutputPort) || slices.Contains(c.SourcePorts, *c.OutputPort) || slices.Contains(c.DestinationPorts, *c.OutputPort)) {
		return errors.New("MIRROR_OUTPUT_PORT_CONFLICT")
	}
	if c.OutputVLAN != nil && (!decimal(*c.OutputVLAN, 1, 4094) || slices.Contains(c.VLANs, *c.OutputVLAN)) {
		return errors.New("MIRROR_OUTPUT_VLAN_CONFLICT")
	}
	if c.Snaplen != nil && !decimal(*c.Snaplen, 14, 65535) {
		return errors.New("MIRROR_SNAPLEN_INVALID")
	}
	return nil
}

func Clone(c Configuration) Configuration {
	c.SourcePorts = slices.Clone(c.SourcePorts)
	c.DestinationPorts = slices.Clone(c.DestinationPorts)
	c.VLANs = slices.Clone(c.VLANs)
	if c.OutputPort != nil {
		v := *c.OutputPort
		c.OutputPort = &v
	}
	if c.OutputVLAN != nil {
		v := *c.OutputVLAN
		c.OutputVLAN = &v
	}
	if c.Snaplen != nil {
		v := *c.Snaplen
		c.Snaplen = &v
	}
	return c
}

func PortIDs(c Configuration) []string {
	out := append(slices.Clone(c.SourcePorts), c.DestinationPorts...)
	if c.OutputPort != nil {
		out = append(out, *c.OutputPort)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
