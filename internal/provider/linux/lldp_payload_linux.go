//go:build linux

package linux

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"unicode"
	"unicode/utf8"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const (
	lldpEtherHeader = 14
	maxLLDPFrame    = lldpEtherHeader + 1500
)

var lldpNearestBridge = [6]byte{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e}

func decodeLLDP(frame []byte) (inventory.LLDPFrame, bool) {
	var result inventory.LLDPFrame
	if len(frame) < lldpEtherHeader || len(frame) > maxLLDPFrame || !bytes.Equal(frame[:6], lldpNearestBridge[:]) ||
		binary.BigEndian.Uint16(frame[12:14]) != unix.ETH_P_LLDP {
		return result, false
	}
	data := frame[lldpEtherHeader:]
	mandatory := byte(1)
	systemName := false
	for len(data) >= 2 {
		header := binary.BigEndian.Uint16(data[:2])
		kind, size := byte(header>>9), int(header&0x1ff)
		if size > len(data)-2 || mandatory <= 3 && kind != mandatory {
			return inventory.LLDPFrame{}, false
		}
		value := data[2 : 2+size]
		data = data[2+size:]
		switch kind {
		case 0:
			return result, size == 0 // Ignore Ethernet padding after End of LLDPDU.
		case 1, 2:
			if kind != mandatory {
				return inventory.LLDPFrame{}, false
			}
			id, ok := decodeLLDPID(value, kind == 1)
			if !ok {
				return inventory.LLDPFrame{}, false
			}
			if kind == 1 {
				result.ChassisID = id
			} else {
				result.PortID = id
			}
			mandatory++
		case 3:
			if mandatory != 3 || size != 2 {
				return inventory.LLDPFrame{}, false
			}
			result.TTL = binary.BigEndian.Uint16(value)
			mandatory++
		case 5:
			if systemName || size > 255 {
				return inventory.LLDPFrame{}, false
			}
			systemName = true
			if size != 0 && lldpPrintableText(value) {
				result.SystemName = string(value)
			}
		}
	}
	return inventory.LLDPFrame{}, false // An explicit end TLV is required.
}

func decodeLLDPID(data []byte, chassis bool) (inventory.LLDPID, bool) {
	if len(data) < 2 || len(data) > 256 || data[0] < 1 || data[0] > 7 {
		return inventory.LLDPID{}, false
	}
	result := inventory.LLDPID{Subtype: data[0]}
	value := data[1:]
	macSubtype, networkSubtype := uint8(3), uint8(4)
	if chassis {
		macSubtype, networkSubtype = 4, 5
	}
	switch result.Subtype {
	case macSubtype:
		if len(value) != 6 || value[0]&1 != 0 || bytes.Equal(value, []byte{0, 0, 0, 0, 0, 0}) {
			return inventory.LLDPID{}, false
		}
		result.Encoding = "mac"
		result.Value = net.HardwareAddr(value).String()
	case networkSubtype:
		if len(value) < 2 || value[0] == 0 {
			return inventory.LLDPID{}, false
		}
		if value[0] != 1 && value[0] != 2 {
			result.Encoding, result.Value = "hex", hex.EncodeToString(value)
			break
		}
		address, ok := netip.AddrFromSlice(value[1:])
		if !ok || !(value[0] == 1 && address.Is4() || value[0] == 2 && address.Is6() && !address.Is4In6()) {
			return inventory.LLDPID{}, false
		}
		result.Encoding, result.Value = "text", address.String()
	default:
		if lldpPrintableText(value) {
			result.Encoding, result.Value = "text", string(value)
		} else {
			result.Encoding, result.Value = "hex", hex.EncodeToString(value)
		}
	}
	return result, true
}

func lldpPrintableText(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for _, r := range string(data) {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
