//go:build linux

package tlscontrol

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"time"
)

func Now() Clock {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	var ts unix.Timespec
	if err != nil || unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts) != nil {
		return Clock{Wall: time.Now()}
	}
	return Clock{BootID: strings.TrimSpace(string(b)), NS: ts.Nano(), Wall: time.Now()}
}
