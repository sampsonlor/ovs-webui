package ovsdb

import (
	"context"
	"net"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func dialOpenFlowUnix(ctx context.Context, b OpenFlowBinding) (net.Conn, error) {
	if !openFlowSocketRecorded(b) {
		return nil, errOpenFlowIdentity
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", b.Socket)
	if err != nil {
		return nil, err
	}
	if !verifyOpenFlowUnix(conn, b) {
		_ = conn.Close()
		return nil, errOpenFlowIdentity
	}
	return conn, nil
}

func verifyOpenFlowUnix(conn net.Conn, b OpenFlowBinding) bool {
	daemon, err := spanningTreeDaemon(conn, b.PeerUID)
	return err == nil && daemon == b.Daemon && openFlowSocketRecorded(b)
}

func openFlowSocketRecorded(b OpenFlowBinding) bool {
	device, inode, ok := openFlowSocketRecord(b.Socket, b.PeerUID)
	return ok && device == b.SocketDevice && inode == b.SocketInode
}

func validOpenFlowSocketPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 100 &&
		strings.HasSuffix(path, ".mgmt") && !strings.ContainsAny(path, "\x00\r\n")
}

func openFlowSocketRecord(path string, uid uint32) (uint64, uint64, bool) {
	if !validOpenFlowSocketPath(path) {
		return 0, 0, false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return 0, 0, false
	}
	var dir, socket unix.Stat_t
	if unix.Lstat(filepath.Dir(path), &dir) != nil || dir.Mode&unix.S_IFMT != unix.S_IFDIR ||
		dir.Mode&0022 != 0 || dir.Uid != 0 && dir.Uid != uid ||
		unix.Lstat(path, &socket) != nil || socket.Mode&unix.S_IFMT != unix.S_IFSOCK ||
		socket.Uid != 0 && socket.Uid != uid || socket.Ino == 0 {
		return 0, 0, false
	}
	return uint64(socket.Dev), socket.Ino, true
}
