//go:build linux

package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	linuxprovider "github.com/sampsonlor/ovs-webui/internal/provider/linux"
)

func observeHostAcceleration(ctx context.Context, target *atomic.Pointer[inventory.HostAccelerationObservation]) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		sample := linuxprovider.ObserveHostAcceleration(ctx)
		if ctx.Err() != nil {
			return
		}
		target.Store(&sample)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
