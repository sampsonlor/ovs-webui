//go:build linux

package linux

import (
	"context"
	"encoding/binary"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"golang.org/x/sys/unix"
)

const (
	neighborDumpBytes    = 4 << 20
	neighborDumpMessages = 8192
)

// A dedicated, unicast route socket lets one round share a single receive
// budget. It accepts kernel replies only and never sends a mutation.
type neighborRoute struct {
	fd              int
	sequence        uint32
	bytes, messages int
	buffer          []byte
}

func (s *neighborRoute) dump(ctx context.Context, operation uint16, family byte, visit func([]byte) error) error {
	if ctx.Err() != nil {
		return apitypes.Fail(503, "NEIGHBORS_DUMP_TIMEOUT")
	}
	var expected uint16
	var headerSize int
	switch {
	case operation == unix.RTM_GETLINK && family == unix.AF_UNSPEC:
		expected, headerSize = unix.RTM_NEWLINK, unix.SizeofIfInfomsg
	case operation == unix.RTM_GETNEIGH && (family == unix.AF_INET || family == unix.AF_INET6):
		expected, headerSize = unix.RTM_NEWNEIGH, unix.SizeofNdMsg
	default:
		return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
	}
	s.sequence++
	request := make([]byte, unix.SizeofNlMsghdr+headerSize)
	binary.NativeEndian.PutUint32(request[0:4], uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:6], operation)
	binary.NativeEndian.PutUint16(request[6:8], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(request[8:12], s.sequence)
	request[unix.SizeofNlMsghdr] = family
	if err := unix.Sendto(s.fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return apitypes.Fail(503, "NEIGHBORS_DUMP_UNAVAILABLE")
	}
	poll := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
	for ctx.Err() == nil {
		n, err := unix.Poll(poll, 10)
		if err == unix.EINTR || n == 0 && err == nil {
			continue
		}
		if err != nil || poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNAVAILABLE")
		}
		n, _, flags, from, err := unix.Recvmsg(s.fd, s.buffer, nil, 0)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNAVAILABLE")
		}
		// The datagram's sender is kernel-supplied. Reply headers carry the
		// requesting socket's port ID and are not sender authentication.
		peer, ok := from.(*unix.SockaddrNetlink)
		if !ok || peer.Pid != 0 || peer.Groups != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n == 0 {
			return apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		}
		done, err := s.consume(s.buffer[:n], expected, visit)
		if err != nil {
			return err
		}
		if done && ctx.Err() == nil {
			return nil
		}
	}
	return apitypes.Fail(503, "NEIGHBORS_DUMP_TIMEOUT")
}

func (s *neighborRoute) consume(data []byte, expected uint16, visit func([]byte) error) (bool, error) {
	s.bytes += len(data)
	if s.bytes > neighborDumpBytes {
		return false, apitypes.Fail(503, "NEIGHBORS_DUMP_BUDGET")
	}
	for len(data) >= unix.SizeofNlMsghdr {
		s.messages++
		if s.messages > neighborDumpMessages {
			return false, apitypes.Fail(503, "NEIGHBORS_DUMP_BUDGET")
		}
		n := int(binary.NativeEndian.Uint32(data[:4]))
		flags := binary.NativeEndian.Uint16(data[6:8])
		if n < unix.SizeofNlMsghdr || n > len(data) || align4(n) > len(data) ||
			binary.NativeEndian.Uint32(data[8:12]) != s.sequence || flags&(unix.NLM_F_DUMP_INTR|unix.NLM_F_DUMP_FILTERED) != 0 {
			break
		}
		kind := binary.NativeEndian.Uint16(data[4:6])
		payload := data[unix.SizeofNlMsghdr:n]
		data = data[align4(n):]
		switch {
		case kind == unix.NLMSG_ERROR:
			if len(payload) >= 4 && int32(binary.NativeEndian.Uint32(payload[:4])) < 0 {
				return false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNAVAILABLE")
			}
			return false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		case flags&unix.NLM_F_MULTI == 0:
			return false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		case kind == unix.NLMSG_DONE:
			if len(data) == 0 && (len(payload) == 0 || len(payload) == 4 && binary.NativeEndian.Uint32(payload) == 0) {
				return true, nil
			}
			return false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		case kind == expected:
			if err := visit(payload); err != nil {
				return false, err
			}
			if len(data) == 0 {
				return false, nil // More multipart frames, including DONE, must follow.
			}
		default:
			return false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
		}
	}
	return false, apitypes.Fail(503, "NEIGHBORS_DUMP_UNPROVEN")
}
