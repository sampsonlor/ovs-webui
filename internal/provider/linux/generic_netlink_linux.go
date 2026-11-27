//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

const genericNetlinkHeader = unix.SizeofNlMsghdr + unix.GENL_HDRLEN
const genericControlVersion = 2

var errGenericNetlink = errors.New("unproven generic netlink reply")

// A dedicated unicast socket permits one small request and one reply at a time.
// Callers supply the command, attributes and a fixed deadline.
type genericNetlink struct {
	fd       int
	sequence uint32
	buffer   [4096]byte
}

func openGenericNetlink() (*genericNetlink, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_GENERIC)
	if err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &genericNetlink{fd: fd}, nil
}

func (s *genericNetlink) close() { _ = unix.Close(s.fd) }

func (s *genericNetlink) family(ctx context.Context, name string, version uint32) (uint16, error) {
	data, err := s.request(ctx, unix.GENL_ID_CTRL, unix.CTRL_CMD_GETFAMILY, genericControlVersion,
		unix.CTRL_CMD_NEWFAMILY, genericControlVersion,
		genericAttribute(unix.CTRL_ATTR_FAMILY_NAME, append([]byte(name), 0)))
	if err != nil {
		return 0, err
	}
	return decodeGenericFamily(data, name, version)
}

func decodeGenericFamily(data []byte, name string, version uint32) (uint16, error) {
	attrs, err := genericAttributes(data)
	if err != nil {
		return 0, err
	}
	family := attrs[unix.CTRL_ATTR_FAMILY_ID]
	wireName := attrs[unix.CTRL_ATTR_FAMILY_NAME]
	wireVersion := attrs[unix.CTRL_ATTR_VERSION]
	size := attrs[unix.CTRL_ATTR_HDRSIZE]
	resolvedName, err := tcString(wireName.data)
	if err != nil || wireName.flags != 0 || resolvedName != name || !family.scalar(2) ||
		!wireVersion.scalar(4) || binary.NativeEndian.Uint32(wireVersion.data) != version ||
		!size.scalar(4) || binary.NativeEndian.Uint32(size.data) != 0 {
		return 0, errGenericNetlink
	}
	id := binary.NativeEndian.Uint16(family.data)
	if id <= unix.GENL_ID_CTRL {
		return 0, errGenericNetlink
	}
	return id, nil
}

func (s *genericNetlink) request(ctx context.Context, family uint16, command, version, replyCommand, replyVersion uint8, attrs []byte) ([]byte, error) {
	deadline, bounded := ctx.Deadline()
	if !bounded || len(attrs) > len(s.buffer)-genericNetlinkHeader {
		return nil, errGenericNetlink
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !time.Now().Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	s.sequence++
	message := make([]byte, genericNetlinkHeader+len(attrs))
	binary.NativeEndian.PutUint32(message[:4], uint32(len(message)))
	binary.NativeEndian.PutUint16(message[4:6], family)
	binary.NativeEndian.PutUint16(message[6:8], unix.NLM_F_REQUEST)
	binary.NativeEndian.PutUint32(message[8:12], s.sequence)
	message[unix.SizeofNlMsghdr], message[unix.SizeofNlMsghdr+1] = command, version
	copy(message[genericNetlinkHeader:], attrs)
	if err := unix.Sendto(s.fd, message, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	poll := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
	for ctx.Err() == nil {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		wait := min(remaining, 5*time.Millisecond)
		n, err := unix.Poll(poll, int((wait+time.Millisecond-1)/time.Millisecond))
		if err == unix.EINTR || err == nil && n == 0 {
			continue
		}
		if err != nil || poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return nil, errGenericNetlink
		}
		n, _, flags, source, err := unix.Recvmsg(s.fd, s.buffer[:], nil, unix.MSG_DONTWAIT)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return nil, err
		}
		peer, ok := source.(*unix.SockaddrNetlink)
		if !ok || peer == nil || peer.Family != unix.AF_NETLINK || peer.Pid != 0 || peer.Groups != 0 ||
			flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n < genericNetlinkHeader || n > len(s.buffer) {
			return nil, errGenericNetlink
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
		return decodeGenericReply(s.buffer[:n], family, s.sequence, replyCommand, replyVersion)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, context.DeadlineExceeded
}

func decodeGenericReply(data []byte, family uint16, sequence uint32, command, version uint8) ([]byte, error) {
	if len(data) < genericNetlinkHeader || int(binary.NativeEndian.Uint32(data[:4])) != len(data) ||
		binary.NativeEndian.Uint32(data[8:12]) != sequence {
		return nil, errGenericNetlink
	}
	kind := binary.NativeEndian.Uint16(data[4:6])
	if kind == unix.NLMSG_ERROR {
		code := int32(binary.NativeEndian.Uint32(data[unix.SizeofNlMsghdr:genericNetlinkHeader]))
		if code < 0 {
			return nil, unix.Errno(-code)
		}
		return nil, errGenericNetlink
	}
	if kind != family || binary.NativeEndian.Uint16(data[6:8]) != 0 ||
		data[unix.SizeofNlMsghdr] != command || data[unix.SizeofNlMsghdr+1] != version ||
		binary.NativeEndian.Uint16(data[unix.SizeofNlMsghdr+2:genericNetlinkHeader]) != 0 {
		return nil, errGenericNetlink
	}
	return data[genericNetlinkHeader:], nil
}

func genericAttribute(kind uint16, data []byte) []byte {
	message := make([]byte, align4(unix.SizeofNlAttr+len(data)))
	binary.NativeEndian.PutUint16(message[:2], uint16(unix.SizeofNlAttr+len(data)))
	binary.NativeEndian.PutUint16(message[2:4], kind)
	copy(message[unix.SizeofNlAttr:], data)
	return message
}

func genericUint32(value uint32) []byte {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, value)
	return data
}

type genericAttr struct {
	data  []byte
	flags uint16
}

func (a genericAttr) scalar(length int) bool { return a.flags == 0 && len(a.data) == length }

func genericAttributes(data []byte) (map[uint16]genericAttr, error) {
	attrs := make(map[uint16]genericAttr)
	for len(data) != 0 {
		if len(data) < unix.SizeofNlAttr {
			return nil, errGenericNetlink
		}
		n := int(binary.NativeEndian.Uint16(data[:2]))
		if n < unix.SizeofNlAttr || align4(n) > len(data) {
			return nil, errGenericNetlink
		}
		kind := binary.NativeEndian.Uint16(data[2:4])
		flags := kind & (unix.NLA_F_NESTED | unix.NLA_F_NET_BYTEORDER)
		kind &^= flags
		if previous, found := attrs[kind]; found && (!previous.scalar(0) || n != unix.SizeofNlAttr || flags != 0) {
			return nil, errGenericNetlink
		}
		attrs[kind] = genericAttr{data[unix.SizeofNlAttr:n], flags}
		data = data[align4(n):]
	}
	return attrs, nil
}
