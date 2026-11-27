package runtimehealth

import (
	"time"

	"golang.org/x/sys/unix"
)

// DebugBootTime is a process clock coordinate, not a wall timestamp. Boottime
// continues across host suspend and is unaffected by wall-clock adjustments.
func DebugBootTime() time.Time {
	var now unix.Timespec
	if unix.ClockGettime(unix.CLOCK_BOOTTIME, &now) != nil {
		return time.Time{}
	}
	return time.Unix(now.Sec, now.Nsec)
}
