//go:build linux

package linux

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

func TestLinuxEthtoolLinkInfoMayReportOnlyKnownPortTypes(t *testing.T) {
	req := inventory.DeviceRequest{Name: "egress0", IfIndex: 7}
	for _, test := range []struct {
		port []byte
		want string
	}{
		{[]byte{ethtoolPortTP}, "twisted-pair"},
		{[]byte{ethtoolPortAUI}, "aui"},
		{[]byte{ethtoolPortMII}, "mii"},
		{[]byte{ethtoolPortFibre}, "fibre"},
		{[]byte{ethtoolPortBNC}, "bnc"},
		{[]byte{ethtoolPortDA}, "direct-attach"},
		{[]byte{0xef}, ""}, // PORT_NONE
		{[]byte{0xff}, ""}, // PORT_OTHER
		{[]byte{0xfe}, ""},
		{nil, ""},
	} {
		payload := linkInfoIdentity(req.Name, req.IfIndex)
		if test.port != nil {
			payload = append(payload, genericAttribute(unix.ETHTOOL_A_LINKINFO_PORT, test.port)...)
		}
		got, err := decodeEtherLinkInfo(payload, req)
		if err != nil || got != test.want {
			t.Fatalf("port %x returned %q, %v, want %q", test.port, got, err, test.want)
		}
	}
}

func TestLinuxEthtoolLinkInfoRequiresExactReplyIdentity(t *testing.T) {
	req := inventory.DeviceRequest{Name: "egress0", IfIndex: 7}
	port := genericAttribute(unix.ETHTOOL_A_LINKINFO_PORT, []byte{ethtoolPortTP})
	for name, test := range map[string][]byte{
		"missing header":  port,
		"wrong name":      append(linkInfoIdentity("other0", req.IfIndex), port...),
		"wrong index":     append(linkInfoIdentity(req.Name, req.IfIndex+1), port...),
		"duplicate port":  bytes.Join([][]byte{linkInfoIdentity(req.Name, req.IfIndex), port, port}, nil),
		"wrong wire type": append(linkInfoIdentity(req.Name, req.IfIndex), genericAttribute(unix.ETHTOOL_A_LINKINFO_PORT|unix.NLA_F_NET_BYTEORDER, []byte{ethtoolPortTP})...),
		"truncated":       append(linkInfoIdentity(req.Name, req.IfIndex), port[:len(port)-1]...),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeEtherLinkInfo(test, req); err == nil || got != "" {
				t.Fatalf("accepted unproven port type %q, %v", got, err)
			}
		})
	}
}

func TestLinuxGenericNetlinkRejectsUnexpectedReplyAndFamilyVersion(t *testing.T) {
	const family, sequence = 25, 3
	header := []byte{unix.ETHTOOL_MSG_LINKINFO_GET_REPLY, unix.ETHTOOL_GENL_VERSION, 0, 0}
	match := tcMessage(family, 0, sequence, header)
	if _, err := decodeGenericReply(match, family, sequence, unix.ETHTOOL_MSG_LINKINFO_GET_REPLY, unix.ETHTOOL_GENL_VERSION); err != nil {
		t.Fatal("matching reply was rejected", err)
	}
	for _, bad := range [][]byte{
		tcMessage(family, unix.NLM_F_MULTI, sequence, header),
		tcMessage(family+1, 0, sequence, header),
		tcMessage(family, 0, sequence+1, header),
		tcMessage(family, 0, sequence, []byte{unix.ETHTOOL_MSG_LINKINFO_NTF, unix.ETHTOOL_GENL_VERSION, 0, 0}),
		tcMessage(family, 0, sequence, []byte{unix.ETHTOOL_MSG_LINKINFO_GET_REPLY, unix.ETHTOOL_GENL_VERSION + 1, 0, 0}),
		append(bytes.Clone(match), match...),
	} {
		if _, err := decodeGenericReply(bad, family, sequence, unix.ETHTOOL_MSG_LINKINFO_GET_REPLY, unix.ETHTOOL_GENL_VERSION); err == nil {
			t.Fatalf("accepted unexpected reply %x", bad)
		}
	}
	id := make([]byte, 2)
	binary.NativeEndian.PutUint16(id, family)
	lookup := bytes.Join([][]byte{
		genericAttribute(unix.CTRL_ATTR_FAMILY_ID, id),
		genericAttribute(unix.CTRL_ATTR_FAMILY_NAME, []byte("ethtool\x00")),
		genericAttribute(unix.CTRL_ATTR_VERSION, genericUint32(unix.ETHTOOL_GENL_VERSION)),
		genericAttribute(unix.CTRL_ATTR_HDRSIZE, genericUint32(0)),
	}, nil)
	if got, err := decodeGenericFamily(lookup, "ethtool", unix.ETHTOOL_GENL_VERSION); err != nil || got != family {
		t.Fatalf("expected exact family, got %d, %v", got, err)
	}
	if _, err := decodeGenericFamily(lookup, "ethtool", unix.ETHTOOL_GENL_VERSION+1); err == nil {
		t.Fatal("accepted different family ABI")
	}
}

func TestLinuxEthtoolLinkInfoLoopbackDoesNotProveConnector(t *testing.T) {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("no loopback interface in this namespace")
	}
	got := observeHardwareConnector(t.Context(), inventory.DeviceRequest{Name: lo.Name, IfIndex: lo.Index})
	if got.Value != "" || got.Availability != "unknown" || got.Reason == "" || got.Authority != ethtoolLinkInfoAuthority {
		t.Fatalf("loopback produced unexpected port evidence %+v", got)
	}
}

func linkInfoIdentity(name string, index int) []byte {
	attrs := genericAttribute(unix.ETHTOOL_A_HEADER_DEV_NAME, append([]byte(name), 0))
	attrs = append(attrs, genericAttribute(unix.ETHTOOL_A_HEADER_DEV_INDEX, genericUint32(uint32(index)))...)
	return genericAttribute(unix.ETHTOOL_A_LINKINFO_HEADER|unix.NLA_F_NESTED, attrs)
}
