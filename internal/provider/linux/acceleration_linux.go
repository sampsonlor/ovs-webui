package linux

import (
	"context"
	"os"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

const accelerationBudget = 100 * time.Millisecond

var accelerationSlot = make(chan struct{}, 1)

// ObserveHostAcceleration samples two fixed global HugeTLB pools and the
// count of online NUMA nodes. A timed-out reader retains the sole slot until
// it exits. Nothing in this sample attributes pages or nodes to DPDK.
func ObserveHostAcceleration(ctx context.Context) inventory.HostAccelerationObservation {
	if ctx.Err() != nil {
		return hostAccelerationUnavailable("ACCELERATION_LINUX_CANCELLED")
	}
	select {
	case accelerationSlot <- struct{}{}:
	default:
		return hostAccelerationUnavailable("ACCELERATION_LINUX_BUSY")
	}
	bounded, cancel := context.WithTimeout(ctx, accelerationBudget)
	defer cancel()
	done := make(chan inventory.HostAccelerationObservation, 1)
	go func() {
		sample := readHostAcceleration(bounded)
		<-accelerationSlot
		done <- sample
	}()
	select {
	case sample := <-done:
		if bounded.Err() == nil {
			return sample
		}
	case <-bounded.Done():
	}
	if ctx.Err() != nil {
		return hostAccelerationUnavailable("ACCELERATION_LINUX_CANCELLED")
	}
	return hostAccelerationUnavailable("ACCELERATION_LINUX_TIMEOUT")
}

func readHostAcceleration(ctx context.Context) inventory.HostAccelerationObservation {
	root, err := os.OpenRoot("/sys")
	if err != nil {
		return hostAccelerationUnavailable("ACCELERATION_SYSFS_UNAVAILABLE")
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return hostAccelerationUnavailable("ACCELERATION_SYSFS_UNAVAILABLE")
	}
	defer dir.Close()
	var filesystem unix.Statfs_t
	if unix.Fstatfs(int(dir.Fd()), &filesystem) != nil || filesystem.Type != unix.SYSFS_MAGIC {
		return hostAccelerationUnavailable("ACCELERATION_SYSFS_UNPROVEN")
	}
	return collectHostAcceleration(ctx, func(path string) (string, string) {
		return scalar(root, path)
	}, time.Now)
}
