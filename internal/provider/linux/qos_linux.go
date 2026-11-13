//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

// OVS HTB/HFSC use qdisc 1:, parent class 1:fffe and a queue class
// numbered queue_id + 1. A matching layout is an observation, not ownership.
const (
	qosRootParent  = 0xffffffff
	qosRootHandle  = 0x10000
	qosParentClass = 0x1fffe
	statsBasic     = 1
	statsQueue     = 3
	statsPacket64  = 8
	htbInit        = 2
	htbOffload     = 9
)

func (p *Provider) ObserveQoS(ctx context.Context, req inventory.QoSRuntimeRequest) inventory.QoSRuntimeSample {
	select {
	case p.qosSlots <- struct{}{}:
		defer func() { <-p.qosSlots }()
	default:
		return inventory.QoSRuntimeSample{Scheduler: "unavailable", Reason: "LINUX_PROVIDER_BUSY"}
	}
	ctx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	sample, err := collectQoS(ctx, req)
	if err == nil {
		return sample
	}
	reason := "LINUX_QOS_READ_UNAVAILABLE"
	switch {
	case ctx.Err() != nil:
		reason = "LINUX_OBSERVATION_TIMEOUT"
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
		reason = "LINUX_QOS_ACCESS_DENIED"
	case errors.Is(err, unix.ENODEV):
		reason = "LINUX_IFINDEX_MISMATCH"
	case errors.Is(err, errPolicingWire):
		reason = "LINUX_QOS_DUMP_UNPROVEN"
	}
	return inventory.QoSRuntimeSample{Scheduler: "unavailable", Reason: reason}
}

type egressRoot struct {
	kind      string
	handle    uint32
	options   string
	offloaded bool
}

func collectQoS(ctx context.Context, req inventory.QoSRuntimeRequest) (inventory.QoSRuntimeSample, error) {
	var sample inventory.QoSRuntimeSample
	s, err := openTCSocket()
	if err != nil {
		return sample, err
	}
	defer unix.Close(s.fd)
	if err := s.binding(ctx, req.Device); err != nil {
		return sample, err
	}
	readRoot := func() (egressRoot, error) {
		rows, err := s.request(ctx, unix.RTM_GETQDISC, unix.RTM_NEWQDISC, req.Device.IfIndex, 0, true)
		if err != nil {
			return egressRoot{}, err
		}
		return findEgressRoot(rows, req.Device.IfIndex)
	}
	root, err := readRoot()
	if err != nil {
		return sample, err
	}
	sample.RootKind = root.kind
	switch {
	case root.kind == "":
		sample.Scheduler, sample.Reason = "not-observed", "LINUX_QOS_ROOT_NOT_OBSERVED"
	case root.kind != req.Kind || root.handle != qosRootHandle:
		sample.Scheduler, sample.Reason = "different", "LINUX_QOS_ROOT_DIFFERS"
	case root.offloaded:
		sample.Scheduler, sample.Reason = "different", "LINUX_QOS_ROOT_OFFLOADED"
	default:
		readClasses := func() ([][]byte, error) {
			return s.request(ctx, unix.RTM_GETTCLASS, unix.RTM_NEWTCLASS, req.Device.IfIndex, qosRootHandle, true)
		}
		rows, err := readClasses()
		if err != nil {
			return sample, err
		}
		sample.Queues, err = readQoSClasses(rows, req)
		if err != nil {
			return sample, err
		}
		sample.Scheduler = "observed"
		if rates := readQoSClassRates(rows, req, root); rates != nil {
			// Class options can change without replacing the root. Do not
			// publish rates from an unstable multipart class dump.
			if next, err := readClasses(); err == nil && reflect.DeepEqual(rates, readQoSClassRates(next, req, root)) {
				sample.Rates = rates
			}
		}
		if len(sample.Queues) != len(req.QueueIDs) {
			sample.Reason = "LINUX_QOS_CLASSES_INCOMPLETE"
		} else {
			for _, counters := range sample.Queues {
				if counters.Bytes == nil || counters.Packets == nil || counters.Drops == nil {
					sample.Reason = "LINUX_QOS_COUNTERS_INCOMPLETE"
					break
				}
			}
		}
	}
	if again, err := readRoot(); err != nil || again != root {
		if err != nil {
			return sample, err
		}
		return sample, errPolicingWire
	}
	if err := s.binding(ctx, req.Device); err != nil {
		return sample, err
	}
	sample.ObservedAt = time.Now().UTC()
	return sample, nil
}

func findEgressRoot(rows [][]byte, index int) (egressRoot, error) {
	var out egressRoot
	for _, row := range rows {
		if len(row) < 20 {
			return out, errPolicingWire
		}
		if int(int32(binary.NativeEndian.Uint32(row[4:8]))) != index || binary.NativeEndian.Uint32(row[12:16]) != qosRootParent {
			continue
		}
		if out.kind != "" {
			return out, errPolicingWire
		}
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return out, err
		}
		kind, err := tcString(attrs[unix.TCA_KIND])
		if err != nil || strings.Trim(kind, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
			return out, errPolicingWire
		}
		out = egressRoot{kind: kind, handle: binary.NativeEndian.Uint32(row[8:12]), options: string(attrs[unix.TCA_OPTIONS])}
		if value, ok := attrs[unix.TCA_HW_OFFLOAD]; ok {
			if len(value) != 1 {
				return out, errPolicingWire
			}
			out.offloaded = value[0] != 0
		}
		if kind == "htb" {
			options, err := tcAttributes(attrs[unix.TCA_OPTIONS])
			if err != nil {
				return out, err
			}
			init := options[htbInit]
			if len(init) != 20 {
				return out, errPolicingWire
			}
			// The last word is direct_pkts, a live counter rather than identity.
			out.options = string(init[:16])
			_, offload := options[htbOffload]
			out.offloaded = out.offloaded || offload
		}
	}
	return out, nil
}

func readQoSClasses(rows [][]byte, req inventory.QoSRuntimeRequest) ([]inventory.QoSQueueCounters, error) {
	classes := map[uint32]inventory.QoSQueueCounters{}
	parentFound := false
	for _, row := range rows {
		if len(row) < 20 || int(int32(binary.NativeEndian.Uint32(row[4:8]))) != req.Device.IfIndex {
			return nil, errPolicingWire
		}
		handle := binary.NativeEndian.Uint32(row[8:12])
		if handle&0xffff0000 != qosRootHandle {
			continue
		}
		parent := binary.NativeEndian.Uint32(row[12:16])
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return nil, err
		}
		kind, err := tcString(attrs[unix.TCA_KIND])
		if err != nil || kind != req.Kind {
			return nil, errPolicingWire
		}
		if handle == qosParentClass {
			if parentFound || parent != qosRootParent && parent != qosRootHandle {
				return nil, errPolicingWire
			}
			parentFound = true
			continue
		}
		if parent != qosParentClass || handle <= qosRootHandle || handle > qosRootHandle+inventory.QoSMaxQueueID+1 {
			continue
		}
		queueID := handle - qosRootHandle - 1
		if _, duplicate := classes[queueID]; duplicate {
			return nil, errPolicingWire
		}
		counters, err := qosCounters(queueID, attrs[unix.TCA_STATS2])
		if err != nil {
			return nil, err
		}
		classes[queueID] = counters
	}
	out := []inventory.QoSQueueCounters{}
	if parentFound {
		for _, id := range req.QueueIDs {
			if c, ok := classes[id]; ok {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func qosCounters(queueID uint32, data []byte) (inventory.QoSQueueCounters, error) {
	out := inventory.QoSQueueCounters{QueueID: queueID}
	stats, err := tcAttributes(data)
	if err != nil {
		return out, err
	}
	decimal := func(value uint64) *string {
		s := strconv.FormatUint(value, 10)
		return &s
	}
	if basic, ok := stats[statsBasic]; ok {
		if len(basic) != 12 && len(basic) != 16 {
			return out, errPolicingWire
		}
		out.Bytes = decimal(binary.NativeEndian.Uint64(basic[:8]))
		out.Packets = decimal(uint64(binary.NativeEndian.Uint32(basic[8:12])))
	}
	if packets, ok := stats[statsPacket64]; ok {
		if len(packets) != 8 || out.Packets == nil {
			return out, errPolicingWire
		}
		out.Packets = decimal(binary.NativeEndian.Uint64(packets))
	}
	if queue, ok := stats[statsQueue]; ok {
		if len(queue) != 20 {
			return out, errPolicingWire
		}
		out.Drops = decimal(uint64(binary.NativeEndian.Uint32(queue[8:12])))
	}
	return out, nil
}
