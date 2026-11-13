//go:build linux

package linux

import (
	"encoding/binary"
	"math"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const (
	htbParameters = 1
	htbRate64     = 6
	htbCeiling64  = 7
	hfscRealtime  = 1
	hfscLinkShare = 2
	hfscUpper     = 3
)

type classRates struct{ minimum, maximum uint64 }

// readQoSClassRates accepts a complete OVS-shaped tree with Queue 0 as its
// default. It is independent of the counter decoder and never uses OVSDB
// rates to fill gaps in the kernel observation.
func readQoSClassRates(rows [][]byte, req inventory.QoSRuntimeRequest, root egressRoot) *inventory.QoSInstalledRates {
	if root.kind != req.Kind || !qosDefaultQueue(root) {
		return nil
	}
	classes := make(map[uint32]classRates, len(req.QueueIDs)+1)
	for _, row := range rows {
		if len(row) < 20 || int(int32(binary.NativeEndian.Uint32(row[4:8]))) != req.Device.IfIndex {
			return nil
		}
		handle, parent := binary.NativeEndian.Uint32(row[8:12]), binary.NativeEndian.Uint32(row[12:16])
		if handle&0xffff0000 != qosRootHandle {
			continue
		}
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return nil
		}
		kind, err := tcString(attrs[unix.TCA_KIND])
		if err != nil || kind != req.Kind {
			return nil
		}
		if offload, ok := attrs[unix.TCA_HW_OFFLOAD]; ok && (len(offload) != 1 || offload[0] != 0) {
			return nil
		}
		// Linux HFSC includes an implicit, unshaped class at handle 1:.
		if kind == "hfsc" && handle == qosRootHandle && parent == qosRootParent && len(attrs[unix.TCA_OPTIONS]) == 0 {
			continue
		}
		if handle == qosParentClass {
			if parent != qosRootParent && parent != qosRootHandle {
				return nil
			}
		} else if parent != qosParentClass || handle <= qosRootHandle || handle > qosRootHandle+inventory.QoSMaxQueueID+1 {
			return nil
		}
		rates := parseClassRates(kind, attrs[unix.TCA_OPTIONS])
		if _, duplicate := classes[handle]; duplicate || rates.minimum == 0 || rates.minimum > rates.maximum || rates.maximum > math.MaxUint64/8 {
			return nil
		}
		classes[handle] = rates
	}
	parent := classes[qosParentClass]
	if parent.maximum == 0 || parent.minimum != parent.maximum || len(classes) != len(req.QueueIDs)+1 {
		return nil
	}
	bps := func(bytesPerSecond uint64) string { return strconv.FormatUint(bytesPerSecond*8, 10) }
	out := &inventory.QoSInstalledRates{MaximumBPS: bps(parent.maximum), Queues: make([]inventory.QoSInstalledQueueRate, 0, len(req.QueueIDs))}
	for _, id := range req.QueueIDs {
		rates := classes[qosRootHandle+id+1]
		if rates.minimum == 0 || rates.maximum > parent.maximum {
			return nil
		}
		out.Queues = append(out.Queues, inventory.QoSInstalledQueueRate{QueueID: id, MinimumBPS: bps(rates.minimum), MaximumBPS: bps(rates.maximum)})
	}
	return out
}

func qosDefaultQueue(root egressRoot) bool {
	if root.offloaded || root.handle != qosRootHandle {
		return false
	}
	switch root.kind {
	case "htb":
		return len(root.options) == 16 && binary.NativeEndian.Uint32([]byte(root.options[8:12])) == 1
	case "hfsc":
		return len(root.options) == 2 && binary.NativeEndian.Uint16([]byte(root.options)) == 1
	default:
		return false
	}
}

func parseClassRates(kind string, data []byte) classRates {
	attrs, err := tcAttributes(data)
	if err != nil {
		return classRates{}
	}
	switch kind {
	case "htb":
		// struct tc_htb_opt starts with two 12-byte tc_ratespecs.
		// Linux reports these rates in bytes per second.
		p := attrs[htbParameters]
		if len(p) != 44 {
			return classRates{}
		}
		if _, offloaded := attrs[htbOffload]; offloaded {
			return classRates{}
		}
		return classRates{htbRate(p[8:12], attrs[htbRate64]), htbRate(p[20:24], attrs[htbCeiling64])}
	case "hfsc":
		// OVS configures matching linear real-time and link-share
		// curves, with a separate linear upper-limit curve.
		minimum := hfscLinearSlope(attrs[hfscLinkShare])
		if minimum != hfscLinearSlope(attrs[hfscRealtime]) {
			return classRates{}
		}
		return classRates{minimum, hfscLinearSlope(attrs[hfscUpper])}
	default:
		return classRates{}
	}
}

func htbRate(rate, extended []byte) uint64 {
	switch len(extended) {
	case 0:
		if extended == nil {
			return uint64(binary.NativeEndian.Uint32(rate))
		}
	case 8:
		return binary.NativeEndian.Uint64(extended)
	}
	return 0
}

func hfscLinearSlope(curve []byte) uint64 {
	if len(curve) != 12 || binary.NativeEndian.Uint64(curve[:8]) != 0 {
		return 0
	}
	return uint64(binary.NativeEndian.Uint32(curve[8:12]))
}
