package linux

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func collectHostAcceleration(ctx context.Context, scalar func(string) (string, string), now func() time.Time) inventory.HostAccelerationObservation {
	out := hostAccelerationUnavailable("ACCELERATION_LINUX_EVIDENCE_UNAVAILABLE")
	if ctx.Err() != nil {
		return hostAccelerationUnavailable("ACCELERATION_LINUX_CANCELLED")
	}
	known := 0
	read := func(path string) inventory.AccelerationField[uint64] {
		if ctx.Err() != nil {
			return linuxAccelerationUnknown[uint64]("ACCELERATION_LINUX_CANCELLED")
		}
		field := linuxAccelerationField(scalar, path, accelerationPages)
		if field.State == "observed" {
			known++
		}
		return field
	}
	for _, pool := range []struct {
		size uint32
		dir  string
	}{
		{2048, "kernel/mm/hugepages/hugepages-2048kB/"},
		{1048576, "kernel/mm/hugepages/hugepages-1048576kB/"},
	} {
		out.HugePages = append(out.HugePages, inventory.HugePagePool{
			SizeKiB:         pool.size,
			PersistentPages: read(pool.dir + "nr_hugepages"),
			FreePages:       read(pool.dir + "free_hugepages"),
			ReservedPages:   read(pool.dir + "resv_hugepages"),
			SurplusPages:    read(pool.dir + "surplus_hugepages"),
		})
	}
	if ctx.Err() != nil {
		return hostAccelerationUnavailable("ACCELERATION_LINUX_CANCELLED")
	}
	out.OnlineNUMA = linuxAccelerationField(scalar, "devices/system/node/online", accelerationNUMACount)
	if out.OnlineNUMA.State == "observed" {
		known++
	}
	if ctx.Err() != nil {
		return hostAccelerationUnavailable("ACCELERATION_LINUX_CANCELLED")
	}
	if known != 0 {
		at := now().UTC()
		out.ObservedAt = &at
		out.State, out.Reason = "partial", "ACCELERATION_LINUX_EVIDENCE_PARTIAL"
		if known == 9 {
			out.State, out.Reason = "observed", ""
		}
	}
	return out
}

func hostAccelerationUnavailable(code string) inventory.HostAccelerationObservation {
	return inventory.HostAccelerationObservation{
		State: "unavailable", Reason: code, Source: "linux-global-sysfs",
		HugePages: []inventory.HugePagePool{}, OnlineNUMA: linuxAccelerationUnknown[uint16](code),
	}
}

func linuxAccelerationUnknown[T any](code string) inventory.AccelerationField[T] {
	return inventory.AccelerationField[T]{State: "unknown", Reason: code}
}

func linuxAccelerationField[T any](read func(string) (string, string), path string, parse func(string) (T, bool)) inventory.AccelerationField[T] {
	raw, status := read(path)
	if status == "unavailable" {
		return linuxAccelerationUnknown[T]("LINUX_ATTRIBUTE_UNAVAILABLE")
	}
	if value, valid := parse(raw); status == "known" && valid {
		return inventory.AccelerationField[T]{State: "observed", Value: &value}
	}
	return linuxAccelerationUnknown[T]("LINUX_ATTRIBUTE_UNPROVEN")
}

func accelerationPages(text string) (uint64, bool) {
	if len(text) < 1 || len(text) > 20 ||
		strings.ContainsFunc(text, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, false
	}
	n, err := strconv.ParseUint(text, 10, 64)
	return n, err == nil
}

func accelerationNUMACount(text string) (uint16, bool) {
	if len(text) < 1 || len(text) > 128 {
		return 0, false
	}
	var count, next uint64
	for _, interval := range strings.Split(text, ",") {
		first, last, ranged := strings.Cut(interval, "-")
		start, ok := accelerationPages(first)
		if !ok || start < next || start >= 4096 {
			return 0, false
		}
		end := start
		if ranged {
			end, ok = accelerationPages(last)
			if !ok || end < start || end >= 4096 {
				return 0, false
			}
		}
		count += end - start + 1
		next = end + 1
	}
	return uint16(count), true
}
