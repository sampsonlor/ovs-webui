//go:build linux

package linux

import (
	"encoding/binary"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Narrow write qualification is deliberately stricter than the diagnostic
// parser: no clsact/egress, shared block, classifier selection or extra actions.
func policingIngressProfile(rows [][]byte, index int) (string, bool) {
	kind := "none"
	for _, row := range rows {
		if len(row) < 20 {
			return "unknown", false
		}
		if int(int32(binary.NativeEndian.Uint32(row[4:8]))) != index {
			continue
		}
		a, err := tcAttributes(row[20:])
		if err != nil {
			return "unknown", false
		}
		name, err := tcString(a[1])
		if err != nil {
			return "unknown", false
		}
		if name != "ingress" && name != "clsact" {
			if binary.NativeEndian.Uint32(row[12:16]) == 0xfffffff1 {
				return "unknown", false
			}
			continue
		}
		if kind != "none" || name != "ingress" || binary.NativeEndian.Uint32(row[8:12]) != 0xffff0000 || binary.NativeEndian.Uint32(row[12:16]) != 0xfffffff1 || len(a[2]) != 0 {
			return name, false
		}
		for key, value := range a {
			switch key {
			case 1, 2, 3, 4, 5, 6, 7, 9:
			case 12:
				if len(value) != 1 || value[0] != 0 {
					return name, false
				}
			case 13, 14:
				if len(value) != 4 || binary.NativeEndian.Uint32(value) != 0 {
					return name, false
				}
			default:
				return name, false
			}
		}
		kind = name
	}
	return kind, true
}

func policingFilterProfile(row []byte, actions []inventory.PolicingAction) (any, bool) {
	if len(row) < 20 {
		return nil, false
	}
	a, err := tcAttributes(row[20:])
	if err != nil {
		return nil, false
	}
	name, err := tcString(a[1])
	if err != nil || name != "matchall" {
		return nil, false
	}
	for key, value := range a {
		switch key {
		case 1, 2, 3, 4, 5, 6, 7, 9:
		case 11:
			if len(value) != 4 || binary.NativeEndian.Uint32(value) != 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	options, err := tcAttributes(a[2])
	if err != nil {
		return nil, false
	}
	handle, info := binary.NativeEndian.Uint32(row[8:12]), binary.NativeEndian.Uint32(row[16:20])
	if len(actions) == 0 {
		return []any{name, handle, info}, handle == 0 && len(options) == 0
	}
	if len(actions) != 1 || handle != 1 || info>>16 != 1 || len(actions[0].ParameterDigest) != 64 {
		return nil, false
	}
	for key, value := range options {
		switch key {
		case 2, 4, 5: // action table, per-CPU statistics and alignment padding
		case 3:
			// Only a software rule explicitly reported NOT_IN_HW or skip_hw.
			if len(value) != 4 || binary.NativeEndian.Uint32(value)&^uint32(1|8) != 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	table, err := tcAttributes(options[2])
	if err != nil || len(table) != 1 || table[1] == nil {
		return nil, false
	}
	action, err := tcAttributes(table[1])
	if err != nil {
		return nil, false
	}
	for key, value := range action {
		switch key {
		case 1, 2, 4, 5:
		case 10: // TCA_ACT_IN_HW_COUNT is always dumped by the Linux action API.
			if len(value) != 4 || binary.NativeEndian.Uint32(value) != 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return []any{name, handle, info, actions[0].Index, actions[0].ParameterDigest}, true
}
