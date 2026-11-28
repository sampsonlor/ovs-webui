package diagnostics

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

const (
	InterfaceCounterTemplate   = "diag.host.interface-counters"
	InterfaceCounterCapability = "diagnostics.active"
	MaxCounterInterfaces       = 4
)

type InterfaceCounterRequest struct {
	Target        apitypes.Ref
	SampleSeconds int
}

func (r InterfaceCounterRequest) Authorize(claims authn.Claims) error {
	if !slices.Contains(claims.Capabilities, InterfaceCounterCapability) || !slices.Contains(claims.Capabilities, "inventory.read") {
		return apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	if r.Target.Kind != "port" || !apitypes.ManagementID(r.Target.ID) {
		return apitypes.Fail(422, "INVALID_DIAGNOSTIC_TARGET")
	}
	if r.SampleSeconds != 5 && r.SampleSeconds != 10 && r.SampleSeconds != 15 {
		return apitypes.Fail(422, "INVALID_DIAGNOSTIC_SAMPLE_BUDGET")
	}
	return nil
}
