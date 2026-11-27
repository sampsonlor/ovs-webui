//go:build linux

package linux

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func devlinkTestU16(value uint16) []byte {
	return binary.NativeEndian.AppendUint16(nil, value)
}

func TestLinuxDevlinkRoleRequiresExactAttachedNetdevice(t *testing.T) {
	handle := devlinkPortHandle{bus: "pci", device: "0000:01:00.0", index: 3}
	request := inventory.DeviceRequest{Name: "local0", IfIndex: 7}
	base := genericAttribute(unix.DEVLINK_ATTR_BUS_NAME, []byte("pci\x00"))
	base = append(base, genericAttribute(unix.DEVLINK_ATTR_DEV_NAME, []byte("0000:01:00.0\x00"))...)
	base = append(base, genericAttribute(unix.DEVLINK_ATTR_PORT_INDEX, genericUint32(3))...)
	base = append(base, genericAttribute(unix.DEVLINK_ATTR_PORT_NETDEV_IFINDEX, genericUint32(7))...)
	base = append(base, genericAttribute(unix.DEVLINK_ATTR_PORT_NETDEV_NAME, []byte("local0\x00"))...)
	base = append(base, genericAttribute(unix.DEVLINK_ATTR_PORT_TYPE, devlinkTestU16(unix.DEVLINK_PORT_TYPE_ETH))...)
	for _, test := range []struct {
		flavour uint16
		role    string
	}{
		{unix.DEVLINK_PORT_FLAVOUR_PHYSICAL, "physical-port"},
		{unix.DEVLINK_PORT_FLAVOUR_PCI_PF, "pci-pf-representor"},
		{unix.DEVLINK_PORT_FLAVOUR_PCI_VF, "pci-vf-representor"},
		{devlinkPortFlavourPCISF, "pci-sf-representor"},
		{unix.DEVLINK_PORT_FLAVOUR_VIRTUAL, ""},
		{65535, ""},
	} {
		message := append(base, genericAttribute(unix.DEVLINK_ATTR_PORT_FLAVOUR, devlinkTestU16(test.flavour))...)
		if got := decodeDevlinkPortRole(message, handle, request); got != test.role {
			t.Errorf("port flavour %d yielded %q, wanted %q", test.flavour, got, test.role)
		}
	}
	message := append(base, genericAttribute(unix.DEVLINK_ATTR_PORT_FLAVOUR, devlinkTestU16(unix.DEVLINK_PORT_FLAVOUR_PHYSICAL))...)
	for _, mismatch := range []inventory.DeviceRequest{{Name: "local1", IfIndex: request.IfIndex}, {Name: request.Name, IfIndex: 8}} {
		if role := decodeDevlinkPortRole(message, handle, mismatch); role != "" {
			t.Errorf("mismatched request admitted role %s", role)
		}
	}
	mismatchedHandle := handle
	mismatchedHandle.index++
	if role := decodeDevlinkPortRole(message, mismatchedHandle, request); role != "" {
		t.Errorf("mismatched port admitted role %s", role)
	}
	foreign := append(slices.Clone(message), genericAttribute(unix.DEVLINK_ATTR_NETNS_ID, genericUint32(0))...)
	duplicate := append(slices.Clone(message), genericAttribute(unix.DEVLINK_ATTR_PORT_INDEX, genericUint32(3))...)
	for _, unproved := range [][]byte{base, foreign, duplicate, message[:len(message)-2]} {
		if role := decodeDevlinkPortRole(unproved, handle, request); role != "" {
			t.Errorf("unproved port data admitted role %s", role)
		}
	}
}
