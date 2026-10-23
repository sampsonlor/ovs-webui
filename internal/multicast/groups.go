package multicast

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxGroupWire = 64 << 10
const MaxGroups = 256

type Group struct {
	Port     uint32 `json:"ofport"`
	VLAN     uint16 `json:"vlan"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Router   bool   `json:"router"`
	Age      uint32 `json:"age_seconds"`
}

// DecodeGroups accepts one bounded native mdb/show reply. A rejected or
// over-budget reply never becomes an empty or silently partial group list.
func DecodeGroups(data []byte) ([]Group, error) {
	invalid := errors.New("MULTICAST_GROUP_REPLY_UNPROVEN")
	if len(data) == 0 || len(data) > MaxGroupWire || !utf8.Valid(data) {
		return nil, invalid
	}
	for _, c := range data {
		if c >= 127 || c < 32 && c != '\n' && c != '\t' {
			return nil, invalid
		}
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "port VLAN protocol GROUP Age" {
		return nil, invalid
	}
	out := []Group{}
	seen := map[string]bool{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 5 || len(out) >= MaxGroups {
			return nil, invalid
		}
		port := uint64(65534)
		var err error
		if fields[0] != "LOCAL" {
			port, err = strconv.ParseUint(fields[0], 10, 16)
			if err != nil || port == 0 || port >= 65280 || strconv.FormatUint(port, 10) != fields[0] {
				return nil, invalid
			}
		}
		vlan, e1 := strconv.ParseUint(fields[1], 10, 12)
		age, e2 := strconv.ParseUint(fields[4], 10, 32)
		if e1 != nil || vlan > 4094 || e2 != nil || strconv.FormatUint(vlan, 10) != fields[1] || strconv.FormatUint(age, 10) != fields[4] {
			return nil, invalid
		}
		g := Group{Port: uint32(port), VLAN: uint16(vlan), Protocol: fields[2], Age: uint32(age), Router: fields[3] == "querier"}
		if g.Router {
			if g.Protocol != "UNKNOWN" {
				return nil, invalid
			}
		} else {
			switch g.Protocol {
			case "IGMPv1", "IGMPv2", "IGMPv3", "MLDv1", "MLDv2":
			default:
				return nil, invalid
			}
			ip, err := netip.ParseAddr(fields[3])
			if err != nil || ip.Zone() != "" || !ip.IsMulticast() {
				return nil, invalid
			}
			if strings.HasPrefix(g.Protocol, "IGMP") != ip.Is4() {
				return nil, invalid
			}
			g.Address = ip.String()
		}
		key := strconv.FormatUint(port, 10) + "/" + strconv.FormatUint(vlan, 10) + "/" + g.Address
		if g.Router {
			key += "/router"
		}
		if seen[key] {
			return nil, invalid
		}
		seen[key] = true
		out = append(out, g)
	}
	return out, nil
}
