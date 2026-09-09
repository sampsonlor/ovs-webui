//go:build !linux

package ipc

import (
	"context"
	"errors"
	"net"
)

func DialPeer(context.Context, string, uint32) (net.Conn, error) {
	return nil, errors.New("IPC_REQUIRES_LINUX")
}
