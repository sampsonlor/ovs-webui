//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const neighborReadBudget = 2 * time.Second

type neighborLink struct {
	device   inventory.DeviceRequest
	hardware uint16
}

type neighborKey struct {
	index int
	ip    string
}

// ObserveNeighbors reads only the Linux IP neighbor tables in mgrd's network
// namespace. The requests describe native Interface bindings from inventory.
// Link checks bracket the two dumps; these are independent kernel observations.
func (*Provider) ObserveNeighbors(ctx context.Context, interfaces []inventory.DeviceRequest) (inventory.NeighborObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, neighborReadBudget)
	defer cancel()
	if ctx.Err() != nil {
		return inventory.NeighborObservation{}, apitypes.Fail(503, "NEIGHBORS_DUMP_TIMEOUT")
	}
	scope, err := neighborScope(interfaces)
	if err != nil {
		return inventory.NeighborObservation{}, err
	}
	result := inventory.NeighborObservation{Neighbors: []inventory.Neighbor{}, Covered: []inventory.DeviceRequest{}}
	if len(scope) == 0 {
		result.ObservedAt = time.Now()
		return result, nil
	}
	socket, err := openTCSocket()
	if err != nil {
		return inventory.NeighborObservation{}, apitypes.Fail(503, "NEIGHBORS_DUMP_UNAVAILABLE")
	}
	defer unix.Close(socket.fd)
	route := neighborRoute{fd: socket.fd, buffer: make([]byte, 64<<10)}
	before, err := route.links(ctx, scope)
	if err != nil {
		return inventory.NeighborObservation{}, err
	}
	if len(before) == 0 {
		result.ObservedAt = time.Now()
		return result, nil
	}
	seen := map[neighborKey]bool{}
	for _, family := range []byte{unix.AF_INET, unix.AF_INET6} {
		err = route.dump(ctx, unix.RTM_GETNEIGH, family, func(row []byte) error {
			neighbor, present, err := decodeNeighbor(row, family, before)
			if err != nil || !present {
				return err
			}
			key := neighborKey{index: neighbor.Interface.IfIndex, ip: neighbor.IP}
			if seen[key] {
				return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
			}
			if len(result.Neighbors) == inventory.MaxNeighbors {
				result.Truncated = true
				return nil
			}
			seen[key] = true
			result.Neighbors = append(result.Neighbors, neighbor)
			return nil
		})
		if err != nil {
			return inventory.NeighborObservation{}, err
		}
	}
	observedAt := time.Now()
	after, err := route.links(ctx, scope)
	if err != nil {
		return inventory.NeighborObservation{}, err
	}
	result.ObservedAt = observedAt
	retainConfirmedNeighbors(&result, interfaces, before, after)
	return result, nil
}

func retainConfirmedNeighbors(result *inventory.NeighborObservation, interfaces []inventory.DeviceRequest, before, after map[int]neighborLink) {
	for _, req := range interfaces {
		if first, ok := before[req.IfIndex]; ok && after[req.IfIndex] == first {
			result.Covered = append(result.Covered, req)
		}
	}
	result.Neighbors = slices.DeleteFunc(result.Neighbors, func(n inventory.Neighbor) bool {
		return before[n.Interface.IfIndex] != after[n.Interface.IfIndex]
	})
}

func neighborScope(interfaces []inventory.DeviceRequest) (map[int]inventory.DeviceRequest, error) {
	if len(interfaces) > inventory.MaxRows {
		return nil, apitypes.Fail(503, "NEIGHBORS_DUMP_BUDGET")
	}
	scope := make(map[int]inventory.DeviceRequest, len(interfaces))
	names := make(map[string]bool, len(interfaces))
	for _, req := range interfaces {
		_, indexRepeated := scope[req.IfIndex]
		if req.IfIndex <= 0 || req.IfIndex > 1<<31-1 || !neighborInterfaceName(req.Name) || names[req.Name] || indexRepeated {
			return nil, apitypes.Fail(503, "NEIGHBORS_INVALID_BINDINGS")
		}
		scope[req.IfIndex] = req
		names[req.Name] = true
	}
	return scope, nil
}

func neighborInterfaceName(name string) bool {
	return len(name) > 0 && len(name) < unix.IFNAMSIZ && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00:\t\r\n ")
}

func (s *neighborRoute) links(ctx context.Context, scope map[int]inventory.DeviceRequest) (map[int]neighborLink, error) {
	links := make(map[int]neighborLink, len(scope))
	seen := make(map[int]bool, len(scope))
	err := s.dump(ctx, unix.RTM_GETLINK, unix.AF_UNSPEC, func(row []byte) error {
		if len(row) < unix.SizeofIfInfomsg {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		}
		index := int(int32(binary.NativeEndian.Uint32(row[4:8])))
		req, eligible := scope[index]
		if !eligible {
			return nil
		}
		if seen[index] {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		}
		seen[index] = true
		attrs, err := tcAttributes(row[unix.SizeofIfInfomsg:])
		if err != nil {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		}
		name, err := tcString(attrs[unix.IFLA_IFNAME])
		if err != nil || !neighborInterfaceName(name) {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		}
		if name == req.Name {
			links[index] = neighborLink{device: req, hardware: binary.NativeEndian.Uint16(row[2:4])}
		}
		return nil
	})
	return links, err
}

func decodeNeighbor(data []byte, family byte, scope map[int]neighborLink) (inventory.Neighbor, bool, error) {
	var result inventory.Neighbor
	if len(data) < unix.SizeofNdMsg || data[0] != family {
		return result, false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
	}
	index := int(int32(binary.NativeEndian.Uint32(data[4:8])))
	link, eligible := scope[index]
	if !eligible || data[10]&unix.NTF_PROXY != 0 {
		return result, false, nil
	}
	attrs, err := tcAttributes(data[unix.SizeofNdMsg:])
	if err != nil {
		return result, false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
	}
	ip, valid := netip.AddrFromSlice(attrs[unix.NDA_DST])
	if !valid || !(family == unix.AF_INET && ip.Is4() || family == unix.AF_INET6 && ip.Is6()) {
		return result, false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.Is4In6() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return result, false, nil
	}
	state := neighborState(binary.NativeEndian.Uint16(data[8:10]))
	result = inventory.Neighbor{Interface: link.device, IP: ip.String(), State: state}
	if mac := attrs[unix.NDA_LLADDR]; link.hardware == unix.ARPHRD_ETHER && len(mac) == 6 && mac[0]&1 == 0 && slices.ContainsFunc(mac, func(b byte) bool { return b != 0 }) {
		// The kernel's NUD_VALID mask covers these states. Failed and incomplete
		// entries never advertise a previous link-layer address to clients.
		switch state {
		case "reachable", "stale", "delay", "probe", "noarp", "permanent":
			text := net.HardwareAddr(mac).String()
			result.MAC = &text
		}
	}
	return result, true, nil
}

func neighborState(state uint16) string {
	switch state {
	case unix.NUD_INCOMPLETE:
		return "incomplete"
	case unix.NUD_REACHABLE:
		return "reachable"
	case unix.NUD_STALE:
		return "stale"
	case unix.NUD_DELAY:
		return "delay"
	case unix.NUD_PROBE:
		return "probe"
	case unix.NUD_FAILED:
		return "failed"
	case unix.NUD_NOARP:
		return "noarp"
	case unix.NUD_PERMANENT:
		return "permanent"
	default:
		return "unknown"
	}
}
