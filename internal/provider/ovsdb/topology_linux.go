//go:build linux

package ovsdb

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func topologyHostIdentities(c candidate.Candidate) (map[string]string, error) {
	var devices map[string]string
	for _, name := range topologySystemNames(c) {
		link, err := net.InterfaceByName(name)
		if err != nil || link.Index <= 0 {
			return nil, apitypes.Fail(409, "HOST_DEVICE_UNPROVEN")
		}
		if devices == nil {
			devices = map[string]string{}
		}
		devices[name] = strconv.Itoa(link.Index)
	}
	return devices, nil
}

func topologyHostCheck(c candidate.Candidate) error {
	i, ok := topologyIntent(c)
	if !ok {
		return nil
	}
	g := i.Topology
	before, after := g.Before, g.After
	if g.Compensating {
		before, after = g.After, g.Restored
	}
	for _, n := range after {
		if n.Binding.Table != "Interface" || slices.ContainsFunc(before, func(p candidate.TopologyNode) bool { return p.Binding == n.Binding && p.Type == n.Type }) {
			continue
		}
		old := slices.ContainsFunc(before, func(p candidate.TopologyNode) bool { return p.Binding == n.Binding })
		link, err := net.InterfaceByName(n.Name)
		if n.Type == "system" || n.Type == "" {
			if err != nil {
				return apitypes.Fail(409, "HOST_DEVICE_UNPROVEN")
			}
			if err = topologyHostUnused(link); err != nil {
				return err
			}
		} else if !old && err == nil {
			return apitypes.Fail(409, "HOST_INTERFACE_NAME_IN_USE")
		}
	}
	for _, n := range before {
		if n.Binding.Table != "Interface" || n.Type != "internal" || slices.ContainsFunc(after, func(p candidate.TopologyNode) bool { return p.Binding == n.Binding && p.Type == n.Type }) {
			continue
		}
		link, err := net.InterfaceByName(n.Name)
		if err == nil {
			if err = topologyHostUnused(link); err != nil {
				return err
			}
		}
	}
	return nil
}
func topologyHostUnused(link *net.Interface) error {
	addresses, err := link.Addrs()
	if err != nil {
		return err
	}
	for _, a := range addresses {
		ip, _, err := net.ParseCIDR(a.String())
		if err != nil || !ip.IsLinkLocalUnicast() {
			return apitypes.Fail(409, "HOST_NETWORK_DEPENDENCY_REQUIRES_SEPARATE_WORKFLOW")
		}
	}
	entries, err := os.ReadDir(filepath.Join("/sys/class/net", link.Name))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "master" || strings.HasPrefix(e.Name(), "upper_") {
			return apitypes.Fail(409, "HOST_NETWORK_DEPENDENCY_REQUIRES_SEPARATE_WORKFLOW")
		}
	}
	return nil
}
func topologyHostApplied(c candidate.Candidate) error {
	i, ok := topologyIntent(c)
	if !ok {
		return nil
	}
	g := i.Topology
	before, after := g.Before, g.After
	if g.Compensating {
		before, after = g.After, g.Restored
	}
	for _, n := range before {
		if n.Binding.Table != "Interface" || n.Type != "internal" || slices.ContainsFunc(after, func(p candidate.TopologyNode) bool { return p.Binding == n.Binding && p.Type == n.Type }) {
			continue
		}
		if _, err := net.InterfaceByName(n.Name); err == nil {
			return apitypes.Fail(409, "HOST_INTERFACE_REMOVAL_PENDING")
		}
	}
	return nil
}
