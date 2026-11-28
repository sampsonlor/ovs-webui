//go:build linux

package main

import (
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func parseCaptureInterfaces(value string) (map[string]bool, error) {
	allowed := map[string]bool{}
	if value == "" {
		return allowed, nil
	}
	if len(value) > 8*37 {
		return nil, apitypes.Fail(422, "INVALID_CAPTURE_INTERFACE_SCOPE")
	}
	for _, id := range strings.Split(value, ",") {
		if !apitypes.ManagementID(id) || allowed[id] || len(allowed) == 8 {
			return nil, apitypes.Fail(422, "INVALID_CAPTURE_INTERFACE_SCOPE")
		}
		allowed[id] = true
	}
	return allowed, nil
}
