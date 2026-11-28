//go:build !linux

package capture

import (
	"context"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func Observe(context.Context, Resolver, Request) (Result, error) {
	return Result{}, apitypes.Fail(503, "CAPTURE_PROVIDER_UNAVAILABLE")
}
