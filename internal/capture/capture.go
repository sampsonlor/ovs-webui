// Package capture provides the bounded Ethernet-header capture vocabulary.
package capture

import (
	"context"
	"encoding/binary"
	"regexp"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	MaxBytes   = 1 << 20
	MaxPackets = 4096
	headerSize = 24
	frameBytes = 14
	recordSize = 16 + frameBytes
)

type Profile string

const (
	ARP  Profile = "arp-ethernet-headers"
	LACP Profile = "slow-protocol-ethernet-headers"
	LLDP Profile = "lldp-ethernet-headers"
)

type Request struct {
	InterfaceID string  `json:"interface_id"`
	Seconds     int     `json:"duration_seconds"`
	MaxBytes    int     `json:"max_bytes"`
	Profile     Profile `json:"filter"`
}

func (p Profile) etherType() uint16 {
	switch p {
	case ARP:
		return 0x0806
	case LACP:
		return 0x8809
	case LLDP:
		return 0x88cc
	}
	return 0
}

func (r Request) Validate() error {
	if !apitypes.ManagementID(r.InterfaceID) || r.Seconds < 1 || r.Seconds > 300 ||
		r.MaxBytes < headerSize+recordSize || r.MaxBytes > MaxBytes || r.Profile.etherType() == 0 {
		return apitypes.Fail(422, "INVALID_CAPTURE_PROFILE_OR_LIMIT")
	}
	return nil
}

// Binding is a fresh witness from the manager's root-approved active native
// Interface association. The reader verifies the same witness before and after
// using the socket and at intervals while the capture remains active.
type Binding struct {
	InterfaceID string
	Generation  string
	NativeUUID  string
	IfIndex     int
	Name        string
	KernelEpoch uint64
}

var linuxName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,14}$`)

func (b Binding) ValidFor(id string) bool {
	return b.InterfaceID == id && apitypes.ManagementID(b.InterfaceID) &&
		apitypes.ManagementID(b.Generation) && apitypes.UUID(b.NativeUUID) &&
		b.IfIndex > 0 && b.KernelEpoch > 0 && linuxName.MatchString(b.Name) && b.Name != "lo"
}

type Resolver func(context.Context, string) (Binding, error)

type Result struct {
	State     string
	Reason    string
	Packets   int
	Bytes     []byte
	StartedAt time.Time
	EndedAt   time.Time
}

func initialPCAP() []byte {
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[0:], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(header[4:], 2)
	binary.LittleEndian.PutUint16(header[6:], 4)
	binary.LittleEndian.PutUint32(header[16:], frameBytes)
	binary.LittleEndian.PutUint32(header[20:], 1) // LINKTYPE_ETHERNET
	return header
}

func emitEthernetHeader(out []byte, observed time.Time, data []byte, original int, typ uint16) ([]byte, bool) {
	if len(data) != frameBytes || original < frameBytes || original > 1<<18 ||
		observed.Unix() < 0 || observed.Unix() > 1<<32-1 || binary.BigEndian.Uint16(data[12:]) != typ {
		return out, false
	}
	var record [recordSize]byte
	binary.LittleEndian.PutUint32(record[0:], uint32(observed.Unix()))
	binary.LittleEndian.PutUint32(record[4:], uint32(observed.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(record[8:], frameBytes)
	binary.LittleEndian.PutUint32(record[12:], uint32(original))
	copy(record[16:], data)
	return append(out, record[:]...), true
}
