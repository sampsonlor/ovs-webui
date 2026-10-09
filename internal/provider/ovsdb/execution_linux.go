//go:build linux

package ovsdb

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"net"
	"os"
	"path/filepath"
	"strings"
)

func bridgeHostCheck(c candidate.Candidate) error {
	if err := topologyHostCheck(c); err != nil {
		return err
	}
	name, before, _, ok := lifecycleHost(c)
	if !ok {
		return nil
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, link := range interfaces {
		if link.Name != name {
			continue
		}
		if !before {
			return apitypes.Fail(409, "HOST_INTERFACE_NAME_IN_USE")
		}
		// Refuse compensation if the OS has acquired a routable address or upper
		// device. Host networking is not modified by this isolated OVS operation.
		addresses, err := link.Addrs()
		if err != nil {
			return err
		}
		for _, a := range addresses {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil || !ip.IsLinkLocalUnicast() {
				return apitypes.Fail(409, "BRIDGE_HOST_DEPENDENCY_CHANGED")
			}
		}
		entries, err := os.ReadDir(filepath.Join("/sys/class/net", link.Name))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "upper_") || entry.Name() == "master" {
				return apitypes.Fail(409, "BRIDGE_HOST_DEPENDENCY_CHANGED")
			}
		}
	}
	return nil
}

func bridgeHostApplied(c candidate.Candidate) error {
	if err := topologyHostApplied(c); err != nil {
		return err
	}
	name, _, after, ok := lifecycleHost(c)
	if !ok || after {
		return nil
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, link := range interfaces {
		if link.Name == name {
			return apitypes.Fail(409, "BRIDGE_HOST_REMOVAL_PENDING")
		}
	}
	return nil
}

func sameExecutionFile(o Options, pid int, prior inventory.Evidence) bool {
	w := witness(o.DatabaseFile, pid, prior.File)
	return w.Available && w.ServerHasFile && prior.File.Available && w.Device == prior.File.Device && w.Inode == prior.File.Inode && w.PriorMatches
}
