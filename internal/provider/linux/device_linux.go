//go:build linux

// Package linux observes host devices without changing links or OVS authority.
package linux

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

const readBudget = 250 * time.Millisecond

type Provider struct {
	slots         chan struct{}
	collect       func(inventory.DeviceRequest) inventory.DeviceSample
	policingSlots chan struct{}
}

func New() *Provider { return newProvider("/sys") }
func newProvider(root string) *Provider {
	return &Provider{slots: make(chan struct{}, 2), policingSlots: make(chan struct{}, 2), collect: func(r inventory.DeviceRequest) inventory.DeviceSample { return collect(root, r) }}
}
func unavailable(reason string) inventory.DeviceSample {
	return inventory.DeviceSample{Availability: "unavailable", Reason: reason, Fields: map[string]inventory.DeviceField{}}
}
func (p *Provider) Observe(ctx context.Context, req inventory.DeviceRequest) inventory.DeviceSample {
	if req.IfIndex <= 0 || len(req.Name) == 0 || len(req.Name) > 15 || req.Name == "." || req.Name == ".." || strings.ContainsAny(req.Name, "/\\\x00:\t\r\n ") {
		return unavailable("LINUX_DEVICE_NAME_INVALID")
	}
	if ctx.Err() != nil {
		return unavailable("LINUX_OBSERVATION_CANCELLED")
	}
	select {
	case p.slots <- struct{}{}:
	default:
		return unavailable("LINUX_PROVIDER_BUSY")
	}
	result := make(chan inventory.DeviceSample, 1)
	go func() {
		defer func() { <-p.slots }()
		result <- p.collect(req)
	}()
	// A blocked kernel attribute retains its worker slot after timeout. Requests
	// do not spawn unbounded workers or borrow the configuration/safety queue.
	timer := time.NewTimer(readBudget)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return unavailable("LINUX_OBSERVATION_CANCELLED")
	case <-timer.C:
		return unavailable("LINUX_OBSERVATION_TIMEOUT")
	case sample := <-result:
		return sample
	}
}

func scalar(root *os.Root, name string) (string, string) {
	f, err := root.Open(name)
	if err != nil {
		return "", "unavailable"
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", "unknown"
	}
	b, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil {
		return "", "unavailable"
	}
	text := strings.TrimSpace(string(b))
	if len(b) > 128 || text == "" || strings.ContainsAny(text, "\x00\r\n\t ") {
		return "", "unknown"
	}
	return text, "known"
}
func field(root *os.Root, name string, parse func(string) (any, bool)) inventory.DeviceField {
	s, availability := scalar(root, name)
	f := inventory.DeviceField{Availability: availability, Reason: "LINUX_ATTRIBUTE_UNAVAILABLE"}
	if availability == "known" {
		value, valid := parse(s)
		if valid {
			f.Value, f.Reason = value, ""
			return f
		}
		f.Availability = "unknown"
	}
	if f.Availability == "unknown" {
		f.Reason = "LINUX_ATTRIBUTE_UNPROVEN"
	}
	return f
}
func number(minimum, maximum int64) func(string) (any, bool) {
	return func(s string) (any, bool) {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil && n >= minimum && n <= maximum
	}
}
func enum(values ...string) func(string) (any, bool) {
	return func(s string) (any, bool) { return s, slices.Contains(values, s) }
}

func collect(sys string, req inventory.DeviceRequest) inventory.DeviceSample {
	root, err := os.OpenRoot(sys)
	if err != nil {
		return unavailable("LINUX_SYSFS_UNAVAILABLE")
	}
	defer root.Close()
	class := filepath.Join("class", "net", req.Name)
	before, err := root.Stat(class)
	if err != nil {
		return unavailable("LINUX_DEVICE_NOT_FOUND")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Join(sys, class))
	if err != nil {
		return unavailable("LINUX_DEVICE_NOT_FOUND")
	}
	rel, err := filepath.Rel(sys, canonical)
	if err != nil || !strings.HasPrefix(rel, "devices"+string(os.PathSeparator)) {
		return unavailable("LINUX_DEVICE_PATH_UNPROVEN")
	}
	device, err := root.OpenRoot(rel)
	if err != nil {
		return unavailable("LINUX_DEVICE_PATH_UNPROVEN")
	}
	defer device.Close()
	pinned, err := device.Stat(".")
	if err != nil || !os.SameFile(before, pinned) {
		return unavailable("LINUX_DEVICE_CHANGED")
	}
	index := field(device, "ifindex", number(1, 1<<31-1))
	if index.Availability != "known" || index.Value != int64(req.IfIndex) {
		return unavailable("LINUX_IFINDEX_MISMATCH")
	}
	fields := map[string]inventory.DeviceField{
		"carrier":    field(device, "carrier", func(s string) (any, bool) { return s == "1", s == "0" || s == "1" }),
		"operstate":  field(device, "operstate", enum("unknown", "notpresent", "down", "lowerlayerdown", "testing", "dormant", "up")),
		"mtu":        field(device, "mtu", number(1, 1<<32-1)),
		"speed_mbps": field(device, "speed", number(1, 1<<32-2)),
		"duplex":     field(device, "duplex", enum("half", "full")),
	}
	hardware(root, rel, fields)
	after, err := root.Stat(class)
	last := field(device, "ifindex", number(1, 1<<31-1))
	if err != nil || !os.SameFile(before, after) || last.Availability != "known" || last.Value != index.Value {
		return unavailable("LINUX_DEVICE_CHANGED")
	}
	return inventory.DeviceSample{Availability: "known", ObservedAt: time.Now(), IfIndex: req.IfIndex, Fields: fields}
}

var pciName = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
var pciID = regexp.MustCompile(`^0x[0-9a-f]{4}$`)
var driverName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func hardware(root *os.Root, netPath string, fields map[string]inventory.DeviceField) {
	for _, key := range []string{"pci_address", "driver", "vendor_id", "device_id", "numa_node"} {
		fields[key] = inventory.DeviceField{Availability: "unavailable", Reason: "LINUX_HARDWARE_ASSOCIATION_UNPROVEN"}
	}
	// Follow the actual devpath upwards, stopping at the first device subsystem.
	// A USB NIC below PCI is a USB device, not its upstream PCI controller.
	for path, depth := filepath.Dir(netPath), 0; path != "devices" && path != "." && depth < 16; path, depth = filepath.Dir(path), depth+1 {
		subsystemPath := filepath.Join(path, "subsystem")
		_, err := root.Lstat(subsystemPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return
		}
		subsystem, err := root.Stat(subsystemPath)
		if err != nil {
			// An unreadable or dangling nearest subsystem does not grant access
			// to an upstream controller's hardware association.
			return
		}
		before, err := root.Stat(path)
		if err != nil {
			return
		}
		pci, err := root.Stat("bus/pci")
		isPCI := err == nil && os.SameFile(subsystem, pci) && pciName.MatchString(filepath.Base(path))
		device, err := root.OpenRoot(path)
		if err != nil {
			return
		}
		defer device.Close()
		observed := map[string]inventory.DeviceField{}
		if isPCI {
			observed["pci_address"] = inventory.DeviceField{Value: filepath.Base(path), Availability: "known"}
			for key, attr := range map[string]string{"vendor_id": "vendor", "device_id": "device"} {
				observed[key] = field(device, attr, func(s string) (any, bool) { return s, pciID.MatchString(s) })
			}
			observed["numa_node"] = field(device, "numa_node", number(0, 1<<31-1))
		}
		// Only use the driver linked from this exact parent device, never a name
		// reported by OVS or a driver from a more distant bridge/controller.
		driver, driverErr := root.Stat(filepath.Join(path, "driver"))
		if link, err := root.Readlink(filepath.Join(path, "driver")); err == nil && driverErr == nil && driverName.MatchString(filepath.Base(link)) {
			observed["driver"] = inventory.DeviceField{Value: filepath.Base(link), Availability: "known"}
		}
		after, err := root.Stat(path)
		lastSubsystem, subsystemErr := root.Stat(filepath.Join(path, "subsystem"))
		if err != nil || subsystemErr != nil || !os.SameFile(before, after) || !os.SameFile(subsystem, lastSubsystem) {
			return
		}
		if driverErr == nil {
			last, err := root.Stat(filepath.Join(path, "driver"))
			if err != nil || !os.SameFile(driver, last) {
				delete(observed, "driver")
			}
		}
		for key, f := range observed {
			fields[key] = f
		}
		return
	}
}
