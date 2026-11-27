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
	hardwarePortBudget = 45 * time.Millisecond
	// Stable UAPI value for DEVLINK_PORT_FLAVOUR_PCI_SF, omitted by x/sys.
	devlinkPortFlavourPCISF = unix.DEVLINK_PORT_FLAVOUR_UNUSED + 1
)

type devlinkPortHandle struct {
	bus, device string
	index       uint32
}

// observeHardwareSwitchPort reads one explicitly identified port in this
// namespace. The same RTM_GETLINK handle must bracket the devlink and ethtool
// requests. The caller independently pins and rechecks the sysfs device.
func observeHardwareSwitchPort(ctx context.Context, req inventory.DeviceRequest) (inventory.DeviceHardwareField, inventory.DeviceHardwareField) {
	port := inventory.DeviceHardwareField{Availability: "unknown", Authority: "linux-devlink-port",
		Reason: "LINUX_DEVLINK_PORT_UNPROVEN", ObservedAt: time.Now()}
	medium := inventory.DeviceHardwareField{Availability: "unknown", Authority: "linux-ethtool-linkinfo",
		Reason: "LINUX_PHYSICAL_PORT_UNPROVEN", ObservedAt: port.ObservedAt}
	ctx, cancel := context.WithTimeout(ctx, hardwarePortBudget)
	defer cancel()
	route, err := openTCSocket()
	if err != nil {
		return port, medium
	}
	defer unix.Close(route.fd)
	first, ok := readDevlinkPortHandle(ctx, &route, req)
	if !ok {
		return port, medium
	}
	conn, err := openGenericNetlink()
	if err != nil {
		return port, medium
	}
	defer conn.close()
	family, err := conn.family(ctx, unix.DEVLINK_GENL_NAME, unix.DEVLINK_GENL_VERSION)
	if err != nil {
		return port, medium
	}
	attrs := genericAttribute(unix.DEVLINK_ATTR_BUS_NAME, append([]byte(first.bus), 0))
	attrs = append(attrs, genericAttribute(unix.DEVLINK_ATTR_DEV_NAME, append([]byte(first.device), 0))...)
	attrs = append(attrs, genericAttribute(unix.DEVLINK_ATTR_PORT_INDEX, genericUint32(first.index))...)
	data, err := conn.request(ctx, family, unix.DEVLINK_CMD_PORT_GET, unix.DEVLINK_GENL_VERSION,
		unix.DEVLINK_CMD_PORT_NEW, unix.DEVLINK_GENL_VERSION, attrs)
	if err != nil {
		return port, medium
	}
	role := decodeDevlinkPortRole(data, first, req)
	if role == "" {
		return port, medium
	}
	observedAt := time.Now()
	observedMedium := medium
	if role == "physical-port" {
		observedMedium = observeHardwareConnector(ctx, req)
	}
	last, ok := readDevlinkPortHandle(ctx, &route, req)
	if ctx.Err() != nil || !ok || last != first {
		return port, medium
	}
	port.Value, port.Availability, port.Reason, port.ObservedAt = role, "known", "", observedAt
	return port, observedMedium
}

func readDevlinkPortHandle(ctx context.Context, socket *tcSocket, req inventory.DeviceRequest) (devlinkPortHandle, bool) {
	link, err := socket.linkAttributes(ctx, req)
	if err != nil {
		return devlinkPortHandle{}, false
	}
	attrs, err := genericAttributes(link[unix.IFLA_DEVLINK_PORT])
	if err != nil {
		return devlinkPortHandle{}, false
	}
	return decodeDevlinkPortHandle(attrs)
}

func decodeDevlinkPortHandle(attrs map[uint16]genericAttr) (devlinkPortHandle, bool) {
	bus, device, index := attrs[unix.DEVLINK_ATTR_BUS_NAME], attrs[unix.DEVLINK_ATTR_DEV_NAME], attrs[unix.DEVLINK_ATTR_PORT_INDEX]
	busName, busErr := tcString(bus.data)
	deviceName, deviceErr := tcString(device.data)
	_, foreign := attrs[unix.DEVLINK_ATTR_NETNS_ID]
	if bus.flags != 0 || device.flags != 0 || busErr != nil || deviceErr != nil || !index.scalar(4) || foreign {
		return devlinkPortHandle{}, false
	}
	return devlinkPortHandle{busName, deviceName, binary.NativeEndian.Uint32(index.data)}, true
}

func decodeDevlinkPortRole(data []byte, target devlinkPortHandle, req inventory.DeviceRequest) string {
	attrs, err := genericAttributes(data)
	if err != nil {
		return ""
	}
	actual, ok := decodeDevlinkPortHandle(attrs)
	idx, name := attrs[unix.DEVLINK_ATTR_PORT_NETDEV_IFINDEX], attrs[unix.DEVLINK_ATTR_PORT_NETDEV_NAME]
	portType, flavour := attrs[unix.DEVLINK_ATTR_PORT_TYPE], attrs[unix.DEVLINK_ATTR_PORT_FLAVOUR]
	if !ok || actual != target || !idx.scalar(4) || int(binary.NativeEndian.Uint32(idx.data)) != req.IfIndex ||
		name.flags != 0 || string(name.data) != req.Name+"\x00" ||
		!portType.scalar(2) || binary.NativeEndian.Uint16(portType.data) != unix.DEVLINK_PORT_TYPE_ETH || !flavour.scalar(2) {
		return ""
	}
	switch binary.NativeEndian.Uint16(flavour.data) {
	case unix.DEVLINK_PORT_FLAVOUR_PHYSICAL:
		return "physical-port"
	case unix.DEVLINK_PORT_FLAVOUR_PCI_PF:
		return "pci-pf-representor"
	case unix.DEVLINK_PORT_FLAVOUR_PCI_VF:
		return "pci-vf-representor"
	case devlinkPortFlavourPCISF:
		return "pci-sf-representor"
	default:
		return ""
	}
}
