//go:build !linux

package ovsdb

import (
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"net"
)

func peer(net.Conn, uint32) (string, int, error) { return "", 0, errors.New("LINUX_REQUIRED") }
func evidence(Options, discovered, inventory.Rows, string, int, inventory.Evidence, bool) inventory.Evidence {
	return inventory.Evidence{}
}
func validateOptions(Options) error { return errors.New("LINUX_REQUIRED") }
