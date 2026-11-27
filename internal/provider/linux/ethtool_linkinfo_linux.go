//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const (
	ethtoolLinkInfoBudget    = 25 * time.Millisecond
	ethtoolLinkInfoAuthority = "linux-ethtool-linkinfo"
	// PORT_* values from linux/ethtool.h. These describe the port type
	// reported by the driver, without mapping it to a chassis location.
	ethtoolPortTP    = 0x00
	ethtoolPortAUI   = 0x01
	ethtoolPortMII   = 0x02
	ethtoolPortFibre = 0x03
	ethtoolPortBNC   = 0x04
	ethtoolPortDA    = 0x05
)

func observeHardwareConnector(ctx context.Context, req inventory.DeviceRequest) inventory.DeviceHardwareField {
	ctx, cancel := context.WithTimeout(ctx, ethtoolLinkInfoBudget)
	defer cancel()
	result := inventory.DeviceHardwareField{
		Availability: "unknown", Reason: "LINUX_ETHTOOL_LINKINFO_UNAVAILABLE", Authority: ethtoolLinkInfoAuthority,
	}
	if req.IfIndex <= 0 || req.IfIndex > 1<<31-1 || !neighborInterfaceName(req.Name) || ctx.Err() != nil {
		return result
	}
	socket, err := openGenericNetlink()
	if err != nil {
		return result
	}
	defer socket.close()
	family, err := socket.family(ctx, "ethtool", unix.ETHTOOL_GENL_VERSION)
	if err != nil {
		return result
	}
	header := genericAttribute(unix.ETHTOOL_A_HEADER_DEV_INDEX, genericUint32(uint32(req.IfIndex)))
	header = append(header, genericAttribute(unix.ETHTOOL_A_HEADER_DEV_NAME, append([]byte(req.Name), 0))...)
	data, err := socket.request(ctx, family, unix.ETHTOOL_MSG_LINKINFO_GET, unix.ETHTOOL_GENL_VERSION,
		unix.ETHTOOL_MSG_LINKINFO_GET_REPLY, unix.ETHTOOL_GENL_VERSION,
		genericAttribute(unix.ETHTOOL_A_LINKINFO_HEADER|unix.NLA_F_NESTED, header))
	if err != nil || ctx.Err() != nil {
		return result
	}
	port, err := decodeEtherLinkInfo(data, req)
	if err != nil {
		result.Reason = "LINUX_ETHTOOL_LINKINFO_UNPROVEN"
		return result
	}
	result.ObservedAt = time.Now()
	if port == "" {
		result.Reason = "LINUX_ETHTOOL_PORT_TYPE_UNKNOWN"
		return result
	}
	result.Value, result.Availability, result.Reason = port, "known", ""
	return result
}

func decodeEtherLinkInfo(data []byte, req inventory.DeviceRequest) (string, error) {
	attrs, err := genericAttributes(data)
	if err != nil {
		return "", err
	}
	header := attrs[unix.ETHTOOL_A_LINKINFO_HEADER]
	if header.flags != 0 && header.flags != unix.NLA_F_NESTED {
		return "", errGenericNetlink
	}
	identity, err := genericAttributes(header.data)
	if err != nil {
		return "", err
	}
	wireIndex, wireName := identity[unix.ETHTOOL_A_HEADER_DEV_INDEX], identity[unix.ETHTOOL_A_HEADER_DEV_NAME]
	name, err := tcString(wireName.data)
	if err != nil || wireName.flags != 0 || name != req.Name || !wireIndex.scalar(4) ||
		binary.NativeEndian.Uint32(wireIndex.data) != uint32(req.IfIndex) {
		return "", errGenericNetlink
	}
	port, found := attrs[unix.ETHTOOL_A_LINKINFO_PORT]
	if !found {
		return "", nil
	}
	if !port.scalar(1) {
		return "", errGenericNetlink
	}
	switch port.data[0] {
	case ethtoolPortTP:
		return "twisted-pair", nil
	case ethtoolPortAUI:
		return "aui", nil
	case ethtoolPortMII:
		return "mii", nil
	case ethtoolPortFibre:
		return "fibre", nil
	case ethtoolPortBNC:
		return "bnc", nil
	case ethtoolPortDA:
		return "direct-attach", nil
	default:
		return "", nil
	}
}
