//go:build !linux

package runtimecontrol

import "context"

// NewSystemd is unavailable on non-Linux hosts.
func NewSystemd() FencedSupervisor {
	return &systemdSupervisor{
		call: func(context.Context, ...string) ([]byte, error) {
			return nil, &SystemdError{Code: "SYSTEMD_UNAVAILABLE"}
		},
	}
}
