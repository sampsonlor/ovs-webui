//go:build !linux

package linux

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

type Provider struct{}

func New() *Provider { return &Provider{} }
func (*Provider) Observe(context.Context, inventory.DeviceRequest) inventory.DeviceSample {
	return inventory.DeviceSample{Availability: "unavailable", Reason: "LINUX_PROVIDER_UNAVAILABLE", Fields: map[string]inventory.DeviceField{}}
}

func (*Provider) ObservePolicing(context.Context, inventory.DeviceRequest) inventory.PolicingSample {
	return inventory.PolicingSample{Availability: "unavailable", Reason: "LINUX_PROVIDER_UNAVAILABLE", Actions: []inventory.PolicingAction{}}
}
