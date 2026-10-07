//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

// Linux UAPI: rtnetlink.h, pkt_cls.h and pkt_sched.h. No tc process, shell,
// mutation flags or write requests are used. Buffers, dumps and concurrency are
// bounded independently of configuration execution and the sysfs observer.
const (
	maxDumpBytes = 256 * 1024
	maxFilters   = 64
	maxActions   = 16
)

var errPolicingWire = errors.New("invalid or incomplete netlink evidence")

func policingFailure(reason string) inventory.PolicingSample {
	return inventory.PolicingSample{Availability: "unavailable", Reason: reason, Actions: []inventory.PolicingAction{}}
}

func (p *Provider) ObservePolicing(ctx context.Context, req inventory.DeviceRequest) inventory.PolicingSample {
	if req.IfIndex <= 0 || req.IfIndex > 2147483647 || len(req.Name) == 0 || len(req.Name) > 15 || req.Name == "." || req.Name == ".." || strings.ContainsAny(req.Name, "/\\\x00:\t\r\n ") {
		return policingFailure("LINUX_DEVICE_NAME_INVALID")
	}
	if ctx.Err() != nil {
		return policingFailure("LINUX_OBSERVATION_CANCELLED")
	}
	select {
	case p.policingSlots <- struct{}{}:
		defer func() { <-p.policingSlots }()
	default:
		return policingFailure("LINUX_PROVIDER_BUSY")
	}
	ctx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	sample, err := collectPolicing(ctx, req)
	if err != nil {
		reason := "LINUX_POLICING_READ_UNAVAILABLE"
		switch {
		case ctx.Err() != nil:
			reason = "LINUX_OBSERVATION_TIMEOUT"
		case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
			reason = "LINUX_POLICING_ACCESS_DENIED"
		case errors.Is(err, errPolicingWire):
			reason = "LINUX_POLICING_DUMP_UNPROVEN"
		case errors.Is(err, unix.ENODEV):
			reason = "LINUX_IFINDEX_MISMATCH"
		}
		return policingFailure(reason)
	}
	return sample
}

type tcSocket struct {
	fd       int
	sequence uint32
	bytes    int
	messages int
}

func collectPolicing(ctx context.Context, req inventory.DeviceRequest) (inventory.PolicingSample, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return inventory.PolicingSample{}, err
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return inventory.PolicingSample{}, err
	}
	s := tcSocket{fd: fd}
	if err := s.binding(ctx, req); err != nil {
		return inventory.PolicingSample{}, err
	}
	qdiscs, err := s.request(ctx, unix.RTM_GETQDISC, unix.RTM_NEWQDISC, req.IfIndex, 0, true)
	if err != nil {
		return inventory.PolicingSample{}, err
	}
	parents, partial, err := ingressParents(qdiscs, req.IfIndex)
	if err != nil {
		return inventory.PolicingSample{}, err
	}
	sample := inventory.PolicingSample{Availability: "known", IfIndex: req.IfIndex, Actions: []inventory.PolicingAction{}}
	for _, parent := range parents {
		filters, err := s.request(ctx, unix.RTM_GETTFILTER, unix.RTM_NEWTFILTER, req.IfIndex, parent, true)
		if err != nil {
			return inventory.PolicingSample{}, err
		}
		for _, filter := range filters {
			if !filterBinding(filter, req.IfIndex, parent) {
				return inventory.PolicingSample{}, errPolicingWire
			}
			sample.FilterCount++
			if sample.FilterCount > maxFilters {
				return inventory.PolicingSample{}, errPolicingWire
			}
			actions, incomplete, err := filterPolicing(filter)
			if err != nil || len(sample.Actions)+len(actions) > maxActions {
				return inventory.PolicingSample{}, errPolicingWire
			}
			partial = partial || incomplete
			sample.Actions = append(sample.Actions, actions...)
		}
	}
	if err := s.binding(ctx, req); err != nil {
		return inventory.PolicingSample{}, err
	}
	if partial {
		sample.Availability, sample.Reason = "partial", "LINUX_POLICING_COVERAGE_PARTIAL"
	}
	sample.ObservedAt = time.Now().UTC()
	return sample, nil
}

func (s *tcSocket) binding(ctx context.Context, req inventory.DeviceRequest) error {
	rows, err := s.request(ctx, unix.RTM_GETLINK, unix.RTM_NEWLINK, req.IfIndex, 0, false)
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) < 16 || int(int32(binary.NativeEndian.Uint32(rows[0][4:8]))) != req.IfIndex {
		return unix.ENODEV
	}
	attrs, err := tcAttributes(rows[0][16:])
	if err != nil {
		return err
	}
	name, err := tcString(attrs[unix.IFLA_IFNAME])
	if err != nil || name != req.Name {
		return unix.ENODEV
	}
	return nil
}

func (s *tcSocket) request(ctx context.Context, op, expected uint16, index int, parent uint32, dump bool) ([][]byte, error) {
	s.sequence++
	size := 20
	if op == unix.RTM_GETLINK {
		size = 16
	}
	request := make([]byte, 16+size)
	binary.NativeEndian.PutUint32(request[0:4], uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:6], op)
	flags := uint16(unix.NLM_F_REQUEST)
	if dump {
		flags |= unix.NLM_F_DUMP
	}
	binary.NativeEndian.PutUint16(request[6:8], flags)
	binary.NativeEndian.PutUint32(request[8:12], s.sequence)
	binary.NativeEndian.PutUint32(request[20:24], uint32(index))
	if size == 20 {
		binary.NativeEndian.PutUint32(request[28:32], parent)
	}
	if err := unix.Sendto(s.fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	var rows [][]byte
	buffer := make([]byte, 65536)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		poll := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 10)
		if err == unix.EINTR || n == 0 && err == nil {
			continue
		}
		if err != nil {
			return nil, err
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return nil, errPolicingWire
		}
		n, _, recvFlags, from, err := unix.Recvmsg(s.fd, buffer, nil, 0)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		peer, ok := from.(*unix.SockaddrNetlink)
		if !ok || peer.Pid != 0 || recvFlags&unix.MSG_TRUNC != 0 || n == 0 {
			return nil, errPolicingWire
		}
		s.bytes += n
		if s.bytes > maxDumpBytes {
			return nil, errPolicingWire
		}
		batch, done, messages, err := tcMessages(buffer[:n], s.sequence, expected, dump)
		s.messages += messages
		if err != nil {
			return nil, err
		}
		if s.messages > 512 {
			return nil, errPolicingWire
		}
		rows = append(rows, batch...)
		if done {
			return rows, nil
		}
	}
}

func align4(n int) int { return (n + 3) &^ 3 }

func filterBinding(row []byte, index int, parent uint32) bool {
	if len(row) < 20 || int(int32(binary.NativeEndian.Uint32(row[4:8]))) != index {
		return false
	}
	actual := binary.NativeEndian.Uint32(row[12:16])
	// Legacy ingress has one block. Linux returns block->classid, set from the
	// last insertion's parent alias (OVS matchall uses ffff:fff2; tc uses ffff:).
	// A clsact hook has distinct ingress/egress blocks and remains exact.
	if parent == 0xffff0000 {
		return actual&0xffff0000 == parent
	}
	return actual == parent
}

func tcMessages(data []byte, sequence uint32, expected uint16, dump bool) ([][]byte, bool, int, error) {
	var rows [][]byte
	done, count := false, 0
	for len(data) > 0 {
		if len(data) < 16 || done {
			return nil, false, count, errPolicingWire
		}
		n := int(binary.NativeEndian.Uint32(data[:4]))
		if n < 16 || n > len(data) || align4(n) > len(data) || binary.NativeEndian.Uint32(data[8:12]) != sequence || binary.NativeEndian.Uint16(data[6:8])&unix.NLM_F_DUMP_INTR != 0 {
			return nil, false, count, errPolicingWire
		}
		kind, payload := binary.NativeEndian.Uint16(data[4:6]), data[16:n]
		count++
		switch kind {
		case unix.NLMSG_ERROR:
			if len(payload) < 4 {
				return nil, false, count, errPolicingWire
			}
			code := int32(binary.NativeEndian.Uint32(payload[:4]))
			if code < 0 {
				return nil, false, count, unix.Errno(-code)
			}
			return nil, false, count, errPolicingWire // No ACK was requested.
		case unix.NLMSG_DONE:
			if !dump || len(payload) != 0 && (len(payload) < 4 || binary.NativeEndian.Uint32(payload[:4]) != 0) {
				return nil, false, count, errPolicingWire
			}
			done = true
		default:
			if kind != expected {
				return nil, false, count, errPolicingWire
			}
			rows = append(rows, append([]byte(nil), payload...))
			if !dump {
				done = true
			}
		}
		data = data[align4(n):]
	}
	return rows, done, count, nil
}

func tcAttributes(data []byte) (map[uint16][]byte, error) {
	attrs := map[uint16][]byte{}
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, errPolicingWire
		}
		n, kind := int(binary.NativeEndian.Uint16(data[:2])), binary.NativeEndian.Uint16(data[2:4])&0x3fff
		if n < 4 || n > len(data) || align4(n) > len(data) {
			return nil, errPolicingWire
		}
		if previous, exists := attrs[kind]; exists && (len(previous) != 0 || n != 4) {
			return nil, errPolicingWire
		}
		attrs[kind] = data[4:n]
		data = data[align4(n):]
	}
	return attrs, nil
}

func tcString(data []byte) (string, error) {
	if len(data) < 2 || len(data) > 32 || data[len(data)-1] != 0 || strings.ContainsRune(string(data[:len(data)-1]), 0) {
		return "", errPolicingWire
	}
	return string(data[:len(data)-1]), nil
}

func ingressParents(rows [][]byte, index int) ([]uint32, bool, error) {
	var parents []uint32
	partial := false
	for _, row := range rows {
		if len(row) < 20 {
			return nil, false, errPolicingWire
		}
		if int(int32(binary.NativeEndian.Uint32(row[4:8]))) != index {
			continue // RTM_GETQDISC dump can include other interfaces.
		}
		attrs, err := tcAttributes(row[20:])
		if err != nil {
			return nil, false, err
		}
		kind, err := tcString(attrs[1])
		if err != nil {
			return nil, false, err
		}
		if kind != "ingress" && kind != "clsact" {
			if binary.NativeEndian.Uint32(row[12:16]) == 0xfffffff1 {
				partial = true
			}
			continue
		}
		if block, ok := attrs[13]; ok {
			if len(block) != 4 {
				return nil, false, errPolicingWire
			}
			if binary.NativeEndian.Uint32(block) != 0 {
				partial = true // Shared ingress blocks need independent traversal.
				continue
			}
		}
		parent := binary.NativeEndian.Uint32(row[8:12]) & 0xffff0000
		if kind == "clsact" {
			parent |= 0xfff2
		}
		if parent&0xffff0000 != 0xffff0000 || len(parents) != 0 {
			return nil, false, errPolicingWire
		}
		parents = append(parents, parent)
	}
	return parents, partial, nil
}

func filterPolicing(row []byte) ([]inventory.PolicingAction, bool, error) {
	if len(row) < 20 {
		return nil, false, errPolicingWire
	}
	attrs, err := tcAttributes(row[20:])
	if err != nil {
		return nil, false, err
	}
	kind, err := tcString(attrs[1])
	if err != nil {
		return nil, false, err
	}
	var actionKey, policeKey uint16
	switch kind {
	case "basic":
		actionKey, policeKey = 3, 4
	case "matchall":
		actionKey = 2
	case "u32":
		actionKey, policeKey = 7, 6
	default:
		return nil, true, nil
	}
	options, err := tcAttributes(attrs[2])
	if err != nil {
		return nil, false, err
	}
	var actions []inventory.PolicingAction
	partial := false
	for key := range options {
		if kind == "basic" && key > 6 || kind == "matchall" && key > 5 || kind == "u32" && key > 12 {
			partial = true
		}
	}
	if police, ok := options[policeKey]; policeKey != 0 && ok {
		a, incomplete, err := policeAction(police)
		if err != nil {
			return nil, false, err
		}
		actions, partial = append(actions, a), incomplete
	}
	if table, ok := options[actionKey]; ok {
		entries, err := tcAttributes(table)
		if err != nil || len(entries) > 32 {
			return nil, false, errPolicingWire
		}
		// Ordered action slots, independent from map iteration order.
		for slot := uint16(1); slot <= 32; slot++ {
			entry, present := entries[slot]
			if !present {
				continue
			}
			act, err := tcAttributes(entry)
			if err != nil {
				return nil, false, err
			}
			name, err := tcString(act[1])
			if err != nil {
				return nil, false, err
			}
			if name != "police" {
				partial = true
				continue
			}
			a, incomplete, err := policeAction(act[2])
			if err != nil {
				return nil, false, err
			}
			partial = partial || incomplete
			actions = append(actions, a)
		}
		for slot := range entries {
			if slot < 1 || slot > 32 {
				partial = true
			}
		}
	}
	for i := range actions {
		actions[i].FilterKind, actions[i].Priority = kind, binary.NativeEndian.Uint32(row[16:20])>>16
		actions[i].Handle = fmt.Sprintf("0x%08x", binary.NativeEndian.Uint32(row[8:12]))
	}
	return actions, partial, nil
}

func tcAction(code int32) string {
	switch code {
	case -1:
		return "continue"
	case 0:
		return "ok"
	case 1:
		return "reclassify"
	case 2:
		return "drop"
	case 3:
		return "pipe"
	case 4:
		return "stolen"
	case 5:
		return "queued"
	case 6:
		return "repeat"
	case 7:
		return "redirect"
	case 8:
		return "trap"
	default:
		return "unknown"
	}
}

func policeAction(data []byte) (inventory.PolicingAction, bool, error) {
	attrs, err := tcAttributes(data)
	if err != nil {
		return inventory.PolicingAction{}, false, err
	}
	tbf := attrs[1]
	if len(tbf) != 56 {
		return inventory.PolicingAction{}, false, errPolicingWire
	}
	a := inventory.PolicingAction{Index: binary.NativeEndian.Uint32(tbf[:4]), ExceedAction: tcAction(int32(binary.NativeEndian.Uint32(tbf[4:8]))), ConformAction: "ok"}
	partial := a.ExceedAction == "unknown"
	if result, ok := attrs[5]; ok {
		if len(result) != 4 {
			return a, false, errPolicingWire
		}
		a.ConformAction = tcAction(int32(binary.NativeEndian.Uint32(result)))
		partial = partial || a.ConformAction == "unknown"
	}
	bytes := uint64(binary.NativeEndian.Uint32(tbf[28:32]))
	if rate, ok := attrs[8]; ok {
		if len(rate) != 8 {
			return a, false, errPolicingWire
		}
		bytes = binary.NativeEndian.Uint64(rate)
	}
	if bytes != 0 {
		value := strconv.FormatUint(bytes, 10)
		a.BytesPerSecond = &value
	}
	if rate, ok := attrs[10]; ok {
		if len(rate) != 8 {
			return a, false, errPolicingWire
		}
		value := strconv.FormatUint(binary.NativeEndian.Uint64(rate), 10)
		a.PacketsPerSecond = &value
	}
	// Peak/average constraints and future police options are not reduced to the
	// displayed rate. Burst ticks need psched conversion and are not exposed.
	partial = partial || binary.NativeEndian.Uint32(tbf[40:44]) != 0
	for key := range attrs {
		if key > 11 || key == 3 || key == 4 || key == 9 {
			partial = true
		}
	}
	return a, partial, nil
}
