package linux

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAccelerationHostSamplesOnlyBoundedKernelFields(t *testing.T) {
	const node = "devices/system/node/online"
	const pool2 = "kernel/mm/hugepages/hugepages-2048kB/"
	const pool1g = "kernel/mm/hugepages/hugepages-1048576kB/"
	replies := map[string]string{
		node:                        "0-1,4",
		pool2 + "nr_hugepages":      "1024",
		pool2 + "free_hugepages":    "0",
		pool2 + "resv_hugepages":    "ovsg_synthetic-secret",
		pool2 + "surplus_hugepages": "2",
	}
	var readPaths []string
	at := time.Now().UTC()
	out := collectHostAcceleration(t.Context(), func(path string) (string, string) {
		readPaths = append(readPaths, path)
		if value, ok := replies[path]; ok {
			return value, "known"
		}
		return "", "unavailable"
	}, func() time.Time { return at })
	want := []string{
		pool2 + "nr_hugepages", pool2 + "free_hugepages", pool2 + "resv_hugepages", pool2 + "surplus_hugepages",
		pool1g + "nr_hugepages", pool1g + "free_hugepages", pool1g + "resv_hugepages", pool1g + "surplus_hugepages", node,
	}
	if !reflect.DeepEqual(want, readPaths) {
		t.Fatalf("unexpected reads: %v", readPaths)
	}
	if out.State != "partial" || out.Source != "linux-global-sysfs" || out.ObservedAt == nil ||
		!out.ObservedAt.Equal(at) || out.OnlineNUMA.Value == nil || *out.OnlineNUMA.Value != 3 || len(out.HugePages) != 2 {
		t.Fatalf("wrong scoped Linux projection: %+v", out)
	}
	first, second := out.HugePages[0], out.HugePages[1]
	if first.SizeKiB != 2048 || first.PersistentPages.Value == nil || *first.PersistentPages.Value != 1024 ||
		first.FreePages.Value == nil || *first.FreePages.Value != 0 || first.ReservedPages.Value != nil ||
		first.ReservedPages.Reason != "LINUX_ATTRIBUTE_UNPROVEN" || second.SizeKiB != 1048576 ||
		second.FreePages.Reason != "LINUX_ATTRIBUTE_UNAVAILABLE" || second.FreePages.Value != nil {
		t.Fatalf("missing, known zero and malformed attributes were confused: %+v %+v", first, second)
	}
}

func TestAccelerationHostRejectsMalformedCountsAndCancelledRead(t *testing.T) {
	for _, text := range []string{"", "6-4", "0-2,2-4", "5,4", "+2", "0-4096", "0-1,", "ovsg_synthetic"} {
		if _, valid := accelerationNUMACount(text); valid {
			t.Fatalf("accepted invalid online-node mask %q", text)
		}
	}
	for _, text := range []string{"", "-2", "+2", "18446744073709551616", "2 3", "2e7", "0x2"} {
		if _, valid := accelerationPages(text); valid {
			t.Fatalf("accepted invalid huge page count %q", text)
		}
	}
	if n, valid := accelerationNUMACount("0-3,5,12-14"); !valid || n != 8 {
		t.Fatal("valid bounded NUMA mask did not preserve its node count")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := collectHostAcceleration(ctx, func(string) (string, string) {
		t.Fatal("cancelled request performed a kernel read")
		return "", "unavailable"
	}, time.Now)
	if out.State != "unavailable" || out.Reason != "ACCELERATION_LINUX_CANCELLED" || out.ObservedAt != nil {
		t.Fatalf("cancelled observation became evidence: %+v", out)
	}
}
