//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"encoding/hex"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const qosIngressParent = 0xfffffff1 // TC_H_INGRESS

func qosLinkProfile(attrs map[uint16][]byte, expectedMTU int) string {
	mtu := attrs[unix.IFLA_MTU]
	mac := attrs[unix.IFLA_ADDRESS]
	if len(mtu) != 4 || uint32(expectedMTU) != binary.NativeEndian.Uint32(mtu) || len(mac) != 6 {
		return ""
	}
	return inventory.Digest([][]byte{attrs[unix.IFLA_IFNAME], mtu, mac, attrs[unix.IFLA_LINK], attrs[unix.IFLA_LINK_NETNSID], attrs[unix.IFLA_PERM_ADDRESS]})
}

func qosVethQueueLength(attrs map[uint16][]byte) []byte {
	info, err := tcAttributes(attrs[unix.IFLA_LINKINFO])
	if err != nil {
		return nil
	}
	kind, err := tcString(info[unix.IFLA_INFO_KIND])
	length := attrs[unix.IFLA_TXQLEN]
	if err != nil || kind != "veth" || len(length) != 4 {
		return nil
	}
	return length
}

// A veth returns to its built-in, classless noqueue root. An ingress-only
// qdisc can coexist with it. Any other qdisc (including clsact) invalidates
// the proof that no egress shaping or filter is installed.
func qosNoqueueRoot(rows [][]byte, index int, root egressRoot) bool {
	if root.kind != "noqueue" || root.handle != 0 || root.options != "" || root.offloaded {
		return false
	}
	for _, row := range rows {
		if len(row) < 20 {
			return false
		}
		if int(int32(binary.NativeEndian.Uint32(row[4:8]))) != index {
			continue
		}
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return false
		}
		kind, err := tcString(attrs[unix.TCA_KIND])
		if err != nil {
			return false
		}
		if kind == "ingress" && binary.NativeEndian.Uint32(row[12:16]) == qosIngressParent {
			continue
		}
		if kind != "noqueue" || binary.NativeEndian.Uint32(row[12:16]) != qosRootParent ||
			attrs[unix.TCA_EGRESS_BLOCK] != nil || attrs[unix.TCA_STAB] != nil || attrs[unix.TCA_RATE] != nil {
			return false
		}
	}
	return true
}

// Called only for two complete, verified class trees. Store the kernel's exact
// static options for dispatch fencing, excluding all traffic counters.
func qosClassProfile(rows [][]byte, req inventory.QoSRuntimeRequest) map[uint32]map[uint16]string {
	profile := make(map[uint32]map[uint16]string, len(rows))
	for _, row := range rows {
		handle := binary.NativeEndian.Uint32(row[8:12])
		if handle&0xffff0000 != qosRootHandle || handle == qosRootHandle && req.Kind == "hfsc" {
			continue
		}
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return nil
		}
		if req.ObserveDefault && (attrs[unix.TCA_STAB] != nil || attrs[unix.TCA_RATE] != nil) {
			return nil
		}
		options, err := tcAttributes(attrs[unix.TCA_OPTIONS])
		if err != nil {
			return nil
		}
		values := make(map[uint16]string, len(options))
		for id, value := range options {
			if req.Kind == "htb" {
				switch id {
				case htbParameters:
					if len(value) != 44 || binary.NativeEndian.Uint32(value[40:44]) != 0 {
						return nil
					}
				case htbRate64, htbCeiling64:
				case 8: // alignment padding
					continue
				default:
					return nil
				}
			} else if id != hfscRealtime && id != hfscLinkShare && id != hfscUpper {
				return nil
			}
			values[id] = hex.EncodeToString(value)
		}
		profile[handle] = values
	}
	return profile
}

func qosKernelProfile(ctx context.Context, s *tcSocket, rows [][]byte, req inventory.QoSRuntimeRequest, root egressRoot, classes map[uint32]map[uint16]string, queueLength []byte) (string, string) {
	if req.ObserveDefault && (root.kind != "hfsc" || queueLength == nil) {
		return "", ""
	}
	qdiscs := map[uint32][]string{}
	rootSeen := false
	for _, row := range rows {
		if len(row) < 20 {
			return "", ""
		}
		if int(int32(binary.NativeEndian.Uint32(row[4:8]))) != req.Device.IfIndex {
			continue
		}
		parent := binary.NativeEndian.Uint32(row[12:16])
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return "", ""
		}
		kind, err := tcString(attrs[unix.TCA_KIND])
		if err != nil {
			return "", ""
		}
		if req.ObserveDefault && kind != "ingress" && (attrs[unix.TCA_STAB] != nil || attrs[unix.TCA_RATE] != nil) {
			return "", ""
		}
		if parent == qosRootParent {
			got, err := findEgressRoot([][]byte{row}, req.Device.IfIndex)
			block, hasBlock := attrs[unix.TCA_EGRESS_BLOCK]
			if rootSeen || err != nil || got != root || hasBlock && (len(block) != 4 || binary.NativeEndian.Uint32(block) != 0) {
				return "", ""
			}
			rootSeen = true
			options := attrs[unix.TCA_OPTIONS]
			if kind == "htb" {
				opts, err := tcAttributes(options)
				if err != nil {
					return "", ""
				}
				for id := range opts {
					if id != htbInit && id != 5 { // TCA_HTB_DIRECT_QLEN
						return "", ""
					}
				}
				// Direct packet count changes with traffic and is excluded.
				options = append([]byte(root.options), opts[5]...)
			}
			qdiscs[parent] = []string{kind, hex.EncodeToString(options)}
			continue
		}
		if kind == "ingress" {
			continue
		}
		if _, externalBlock := attrs[unix.TCA_EGRESS_BLOCK]; externalBlock {
			return "", ""
		}
		if classes[parent] == nil || parent == qosParentClass || qdiscs[parent] != nil || kind != "pfifo" && kind != "pfifo_fast" {
			return "", ""
		}
		// HFSC creates a handle-zero pfifo for its default leaves. An explicit
		// graft or foreign limit could not be recreated from the OVSDB row.
		if req.ObserveDefault && (kind != "pfifo" || binary.NativeEndian.Uint32(row[8:12]) != 0 ||
			string(attrs[unix.TCA_OPTIONS]) != string(queueLength)) {
			return "", ""
		}
		qdiscs[parent] = []string{kind, hex.EncodeToString(row[8:12]), hex.EncodeToString(attrs[unix.TCA_OPTIONS])}
	}
	if !rootSeen {
		return "", ""
	}
	parents := []uint32{qosRootHandle, qosParentClass}
	for _, id := range req.QueueIDs {
		parents = append(parents, qosRootHandle+id+1)
	}
	for _, parent := range parents {
		filters, err := s.request(ctx, unix.RTM_GETTFILTER, unix.RTM_NEWTFILTER, req.Device.IfIndex, parent, true)
		if err != nil || len(filters) != 0 {
			return "", ""
		}
	}
	static := make(map[uint32]string, len(classes))
	for handle, class := range classes {
		static[handle] = ""
		if req.Kind == "htb" {
			// Preserve link parameters, hierarchy and priority. Rates and
			// rate-dependent token buffers and quantum change with the intent.
			options := class[htbParameters]
			if len(options) != 88 {
				return "", ""
			}
			static[handle] = options[:16] + options[24:40] + options[72:]
		}
	}
	return inventory.Digest([]any{classes, qdiscs}), inventory.Digest([]any{static, qdiscs})
}
