//go:build !linux

package ovsdb

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func bridgeHostCheck(c candidate.Candidate) error {
	if err := topologyHostCheck(c); err != nil {
		return err
	}
	if _, _, _, ok := lifecycleHost(c); ok {
		return apitypes.Fail(503, "BRIDGE_HOST_PROVIDER_UNAVAILABLE")
	}
	return nil
}

func sameExecutionFile(Options, int, inventory.Evidence) bool { return false }

func bridgeHostApplied(c candidate.Candidate) error { return bridgeHostCheck(c) }
