//go:build !linux

package ovsdb

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func topologyHostCheck(c candidate.Candidate) error {
	if _, ok := topologyIntent(c); ok {
		return apitypes.Fail(409, "HOST_PLATFORM_UNSUPPORTED")
	}
	return nil
}
func topologyHostApplied(c candidate.Candidate) error { return topologyHostCheck(c) }
