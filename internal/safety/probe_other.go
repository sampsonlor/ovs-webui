//go:build !linux

package safety

import "errors"

func NewTCPProbe(address, device string) (Probe, error) {
	return nil, errors.New("LINUX_MANAGEMENT_PROBE_REQUIRED")
}
