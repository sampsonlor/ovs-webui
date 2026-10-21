//go:build linux

package ovsdb

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

var sourceUnproven = errors.New("SPANNING_TREE_SOURCE_UNPROVEN")

// ReadBoundSpanningTreeRuntime is a private, read-only source prerequisite.
// It checks a pinned control incarnation, a root-owned executable and an actual
// established Unix connection to the configured DB peer, both before and after
// the one runtime request. It does NOT bind a Bridge UUID to the name-based
// runtime reply, qualify exclusive IDL ownership or produce Applied evidence.
// Graph/Bridge qualification and dispatch reservations remain separate gates.
func ReadBoundSpanningTreeRuntime(ctx context.Context, control SpanningTreeControl, database Options, expected inventory.Evidence, executable, protocol, bridge string, authorize func(context.Context) error) (SpanningTreeRuntimeSample, error) {
	if authorize == nil {
		return SpanningTreeRuntimeSample{}, sourceUnproven
	}
	if err := authorize(ctx); err != nil {
		return SpanningTreeRuntimeSample{}, err
	}
	if validateOptions(database) != nil || control.ExpectedDaemon == "" || !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\x00\r\n") {
		return SpanningTreeRuntimeSample{}, sourceUnproven
	}
	var original string
	check := func(ctx context.Context, conn net.Conn) error {
		if err := authorize(ctx); err != nil {
			return err
		}
		identity, err := spanningTreeSource(ctx, conn, control.PeerUID, database, expected, executable)
		if err != nil || original != "" && identity != original {
			return sourceUnproven
		}
		original = identity
		return nil
	}
	sample, err := readSpanningTreeRuntime(ctx, control, protocol, bridge, check)
	if err != nil {
		return SpanningTreeRuntimeSample{}, err
	}
	if err = authorize(ctx); err != nil {
		return SpanningTreeRuntimeSample{}, err
	}
	return sample, nil
}

func spanningTreeSource(ctx context.Context, control net.Conn, uid uint32, database Options, expected inventory.Evidence, executable string) (string, error) {
	if expected.Endpoint != filepath.Clean(database.Socket) || expected.Database != "Open_vSwitch" || expected.Schema == "" || !apitypes.UUID(expected.Root) || !apitypes.UUID(expected.Boot) || time.Since(expected.ObservedAt) < 0 || time.Since(expected.ObservedAt) > inventory.FreshFor {
		return "", sourceUnproven
	}
	daemon, pid, err := peer(control, uid)
	if err != nil {
		return "", sourceUnproven
	}
	var trusted unix.Stat_t
	var actual unix.Stat_t
	if unix.Stat(executable, &trusted) != nil || trusted.Uid != 0 || trusted.Mode&unix.S_IFMT != unix.S_IFREG || trusted.Mode&0022 != 0 || trusted.Mode&0111 == 0 || unix.Stat(fmt.Sprintf("/proc/%d/exe", pid), &actual) != nil || trusted.Dev != actual.Dev || trusted.Ino != actual.Ino {
		return "", sourceUnproven
	}
	db, err := (&net.Dialer{}).DialContext(ctx, "unix", database.Socket)
	if err != nil {
		return "", sourceUnproven
	}
	defer db.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = db.Close() })
	defer closeOnCancel()
	dbPeer, dbPID, err := peer(db, database.PeerUID)
	boot, bootErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || bootErr != nil || strings.TrimSpace(string(boot)) != expected.Boot || dbPeer != expected.Peer || dbPID == pid {
		return "", sourceUnproven
	}
	r := bufio.NewReader(db)
	schema, err := call(db, r, 1, "get_schema", []any{"Open_vSwitch"})
	if err != nil {
		return "", sourceUnproven
	}
	d, err := discover(schema)
	if err != nil || d.public.Digest != expected.Schema {
		return "", sourceUnproven
	}
	// Fixed read-only select verifies the single current root, independently of
	// configured paths or process arguments. No mutation/lock/reconnect RPC.
	body, err := call(db, r, 2, "transact", []any{"Open_vSwitch", map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{}, "columns": []string{"_uuid"}}})
	var roots []struct {
		Rows []struct {
			UUID []string `json:"_uuid"`
		} `json:"rows"`
	}
	if err != nil || json.Unmarshal(body, &roots) != nil || len(roots) != 1 || len(roots[0].Rows) != 1 || len(roots[0].Rows[0].UUID) != 2 || roots[0].Rows[0].UUID[0] != "uuid" || roots[0].Rows[0].UUID[1] != expected.Root {
		return "", sourceUnproven
	}
	w := witness(database.DatabaseFile, dbPID, expected.File)
	// This gate requires a real open file handle. The broader inventory's argv
	// fallback is intentionally insufficient for this source qualification.
	if !w.Available || !w.ServerHasFile || w.ServerBinding != "observed-file-handle" || !expected.File.Available || !w.PriorMatches || w.Device != expected.File.Device || w.Inode != expected.File.Inode {
		return "", sourceUnproven
	}
	a, err := processSocketInodes(pid)
	if err != nil {
		return "", err
	}
	b, err := processSocketInodes(dbPID)
	if err != nil {
		return "", err
	}
	links, err := unixSocketPeers(ctx)
	if err != nil {
		return "", err
	}
	var connection string
	for inode := range a {
		other := links[inode]
		if other != 0 && b[other] && links[other] == inode {
			if connection != "" {
				return "", sourceUnproven
			}
			connection = fmt.Sprintf("%d:%d", inode, other)
		}
	}
	if connection == "" || ctx.Err() != nil {
		return "", sourceUnproven
	}
	// Recheck process incarnations after proc/diag collection. Socket replacement
	// or a daemon/DB restart cannot inherit the sampled connection identity.
	current, _, err := peer(control, uid)
	dbCurrent, _, dbErr := peer(db, database.PeerUID)
	if err != nil || dbErr != nil || current != daemon || dbCurrent != dbPeer {
		return "", sourceUnproven
	}
	return fmt.Sprintf("%s/%s/%d:%d/%d:%d/%s", daemon, dbPeer, trusted.Dev, trusted.Ino, w.Device, w.Inode, connection), nil
}

func processSocketInodes(pid int) (map[uint32]bool, error) {
	base := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) > 1024 {
		return nil, sourceUnproven
	}
	out := map[uint32]bool{}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(base, entry.Name()))
		if err != nil {
			// A concurrent close makes the collected view unproven.
			return nil, sourceUnproven
		}
		if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
			continue
		}
		id, err := strconv.ParseUint(target[8:len(target)-1], 10, 32)
		if err != nil || id == 0 {
			return nil, sourceUnproven
		}
		out[uint32(id)] = true
	}
	return out, nil
}

// Linux SOCK_DIAG supplies actual connected peer inodes. Paths, argv and equal
// process UIDs do not supply this relation. Fail closed when proc/diag access is
// restricted; no capability escalation or weaker fallback is attempted.
func unixSocketPeers(ctx context.Context) (map[uint32]uint32, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return nil, sourceUnproven
	}
	defer unix.Close(fd)
	if unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}) != nil || unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 100000}) != nil {
		return nil, sourceUnproven
	}
	order := binary.NativeEndian
	request := make([]byte, 40) // nlmsghdr + unix_diag_req
	order.PutUint32(request, 40)
	order.PutUint16(request[4:], 20) // SOCK_DIAG_BY_FAMILY
	order.PutUint16(request[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	order.PutUint32(request[8:], 1)
	request[16] = unix.AF_UNIX
	order.PutUint32(request[20:], 1<<1) // established only
	order.PutUint32(request[28:], 4)    // UDIAG_SHOW_PEER
	order.PutUint64(request[32:], ^uint64(0))
	if unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}) != nil {
		return nil, sourceUnproven
	}
	out := map[uint32]uint32{}
	buffer := make([]byte, 64<<10)
	deadline := time.Now().Add(time.Second)
	for total := 0; total < 1<<20 && len(out) <= 4096 && ctx.Err() == nil && time.Now().Before(deadline); {
		n, _, flags, from, err := unix.Recvmsg(fd, buffer, nil, 0)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		sender, ok := from.(*unix.SockaddrNetlink)
		if err != nil || !ok || sender.Pid != 0 || flags&unix.MSG_TRUNC != 0 || n == 0 {
			return nil, sourceUnproven
		}
		total += n
		done, err := parseUnixPeers(buffer[:n], out)
		if err != nil {
			return nil, err
		}
		if done {
			if ctx.Err() != nil || len(out) > 4096 {
				return nil, sourceUnproven
			}
			return out, nil
		}
	}
	return nil, sourceUnproven
}

func parseUnixPeers(data []byte, out map[uint32]uint32) (bool, error) {
	order := binary.NativeEndian
	for len(data) > 0 {
		if len(data) < 16 {
			return false, sourceUnproven
		}
		n := int(order.Uint32(data))
		if n < 16 || n > len(data) || (n+3)&^3 > len(data) || order.Uint32(data[8:]) != 1 || order.Uint16(data[6:])&unix.NLM_F_DUMP_INTR != 0 {
			return false, sourceUnproven
		}
		kind := order.Uint16(data[4:])
		body := data[16:n]
		data = data[(n+3)&^3:]
		if kind == unix.NLMSG_DONE {
			if len(data) != 0 || len(body) != 4 || order.Uint32(body) != 0 {
				return false, sourceUnproven
			}
			return true, nil
		}
		if kind != 20 || len(body) < 16 || body[0] != unix.AF_UNIX || body[1] != unix.SOCK_STREAM || body[2] != 1 {
			// Established non-stream Unix sockets cannot attest this JSON-RPC link.
			if kind == 20 && len(body) >= 16 && body[0] == unix.AF_UNIX && body[2] == 1 {
				continue
			}
			return false, sourceUnproven
		}
		inode := order.Uint32(body[4:])
		if inode == 0 {
			return false, sourceUnproven
		}
		peer := uint32(0)
		for attrs := body[16:]; len(attrs) > 0; {
			if len(attrs) < 4 {
				return false, sourceUnproven
			}
			size := int(order.Uint16(attrs))
			if size < 4 || size > len(attrs) || (size+3)&^3 > len(attrs) {
				return false, sourceUnproven
			}
			if order.Uint16(attrs[2:]) == 2 { // UNIX_DIAG_PEER
				if size != 8 || peer != 0 {
					return false, sourceUnproven
				}
				peer = order.Uint32(attrs[4:])
				if peer == 0 {
					return false, sourceUnproven
				}
			}
			attrs = attrs[(size+3)&^3:]
		}
		if _, duplicate := out[inode]; duplicate {
			return false, sourceUnproven
		}
		out[inode] = peer
		if len(out) > 4096 {
			return false, sourceUnproven
		}
	}
	return false, nil
}
