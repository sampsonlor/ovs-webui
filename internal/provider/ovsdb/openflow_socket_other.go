//go:build !linux

package ovsdb

import (
	"context"
	"net"
)

func dialOpenFlowUnix(context.Context, OpenFlowBinding) (net.Conn, error) {
	return nil, errOpenFlowUnsupported
}

func verifyOpenFlowUnix(net.Conn, OpenFlowBinding) bool { return false }
