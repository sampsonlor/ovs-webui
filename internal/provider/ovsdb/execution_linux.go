//go:build linux

package ovsdb

import "github.com/sampsonlor/ovs-webui/internal/inventory"

func sameExecutionFile(o Options, pid int, prior inventory.Evidence) bool {
	w := witness(o.DatabaseFile, pid, prior.File)
	return w.Available && w.ServerHasFile && prior.File.Available && w.Device == prior.File.Device && w.Inode == prior.File.Inode && w.PriorMatches
}
