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
	collect       func(context.Context, inventory.DeviceRequest) inventory.DeviceSample
	policingSlots chan struct{}
	qosSlots      chan struct{}
}

func New() *Provider { return newProvider("/sys") }
func newProvider(root string) *Provider {
	return &Provider{
		slots: make(chan struct{}, 2), policingSlots: make(chan struct{}, 2), qosSlots: make(chan struct{}, 2),
		collect: func(ctx context.Context, r inventory.DeviceRequest) inventory.DeviceSample {
			return collect(ctx, root, r)
		},
	}
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
	readCtx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	result := make(chan inventory.DeviceSample, 1)
	go func() {
		sample := p.collect(readCtx, req)
		<-p.slots
		result <- sample
	}()
	// A blocked kernel attribute retains its worker slot after timeout. Requests
	// do not spawn unbounded workers or borrow the configuration/safety queue.
	select {
	case <-readCtx.Done():
	case sample := <-result:
		if readCtx.Err() == nil {
			return sample
		}
	}
	if ctx.Err() != nil {
		return unavailable("LINUX_OBSERVATION_CANCELLED")
	}
	return unavailable("LINUX_OBSERVATION_TIMEOUT")
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

func collect(ctx context.Context, sys string, req inventory.DeviceRequest) inventory.DeviceSample {
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
	pciFunction := hardware(root, rel, fields)
	pciFunction.ObservedAt = time.Now()
	roles := &inventory.DeviceHardwareObservation{PCIFunction: pciFunction}
	// Synthetic sysfs fixtures never query the real host. A representor can
	// lack a PCI parent, so production checks the matched netdevice instead.
	if sys == "/sys" && ctx.Err() == nil {
		roles.SwitchPort, roles.Connector = observeHardwareSwitchPort(ctx, req)
	}
	after, err := root.Stat(class)
	last := field(device, "ifindex", number(1, 1<<31-1))
	if err != nil || !os.SameFile(before, after) || last.Availability != "known" || last.Value != index.Value {
		return unavailable("LINUX_DEVICE_CHANGED")
	}
	return inventory.DeviceSample{
		Availability: "known", ObservedAt: time.Now(), IfIndex: req.IfIndex, Fields: fields, Hardware: roles,
	}
}

var pciName = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
var pciID = regexp.MustCompile(`^0x[0-9a-f]{4}$`)
var driverName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func hardware(root *os.Root, netPath string, fields map[string]inventory.DeviceField) inventory.DeviceHardwareField {
	role := inventory.DeviceHardwareField{
		Availability: "unavailable", Reason: "LINUX_HARDWARE_ASSOCIATION_UNPROVEN", Authority: "linux-sysfs-pci",
	}
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
			return role
		}
		subsystem, err := root.Stat(subsystemPath)
		if err != nil {
			// An unreadable or dangling nearest subsystem does not grant access
			// to an upstream controller's hardware association.
			return role
		}
		before, err := root.Stat(path)
		if err != nil {
			return role
		}
		pci, err := root.Stat("bus/pci")
		isPCI := err == nil && os.SameFile(subsystem, pci) && pciName.MatchString(filepath.Base(path))
		device, err := root.OpenRoot(path)
		if err != nil {
			return role
		}
		defer device.Close()
		pinned, err := device.Stat(".")
		if err != nil || !os.SameFile(before, pinned) {
			return role
		}
		observed := map[string]inventory.DeviceField{}
		pciRole := role
		if isPCI {
			observed["pci_address"] = inventory.DeviceField{Value: filepath.Base(path), Availability: "known"}
			for key, attr := range map[string]string{"vendor_id": "vendor", "device_id": "device"} {
				observed[key] = field(device, attr, func(s string) (any, bool) { return s, pciID.MatchString(s) })
			}
			observed["numa_node"] = field(device, "numa_node", number(0, 1<<31-1))
			pciRole = observePCIFunction(root, device, path, pci, pinned)
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
			return role
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
		return pciRole
	}
	return role
}

func indexedPCIDevice(root *os.Root, address string, pci os.FileInfo) (os.FileInfo, bool) {
	if !pciName.MatchString(address) {
		return nil, false
	}
	path := filepath.Join("bus", "pci", "devices", address)
	indexed, err := root.Stat(path)
	subsystem, subsystemErr := root.Stat(filepath.Join(path, "subsystem"))
	return indexed, err == nil && indexed.IsDir() && subsystemErr == nil && os.SameFile(pci, subsystem)
}

func observePCIFunction(root, device *os.Root, path string, pci, pinned os.FileInfo) inventory.DeviceHardwareField {
	result := inventory.DeviceHardwareField{
		Availability: "unknown", Reason: "LINUX_PCI_ROLE_UNPROVEN", Authority: "linux-sysfs-pci",
	}
	address := filepath.Base(path)
	initial, ok := indexedPCIDevice(root, address, pci)
	if !ok || !os.SameFile(initial, pinned) {
		return result
	}
	physfn := filepath.Join(path, "physfn")
	link, err := root.Lstat(physfn)
	var role string
	switch {
	case err == nil && link.Mode()&os.ModeSymlink != 0:
		// A VF must not also carry the SR-IOV PF attribute.
		if _, err := device.Lstat("sriov_totalvfs"); errors.Is(err, os.ErrNotExist) &&
			provePhysicalFunction(root, physfn, link, pci, pinned) {
			if _, err := device.Lstat("sriov_totalvfs"); errors.Is(err, os.ErrNotExist) {
				role = "sr-iov-vf"
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// This is an SR-IOV capability of the PCI function. It says
		// nothing about a physical Ethernet connector or a representor.
		total, availability := scalar(device, "sriov_totalvfs")
		n, err := strconv.ParseUint(total, 10, 16)
		if availability == "known" && err == nil && n > 0 && total == strconv.FormatUint(n, 10) {
			_, linkErr := root.Lstat(physfn)
			latest, available := scalar(device, "sriov_totalvfs")
			if errors.Is(linkErr, os.ErrNotExist) && available == "known" && latest == total {
				role = "sr-iov-pf"
			}
		}
	}
	if role != "" {
		if latest, ok := indexedPCIDevice(root, address, pci); ok && os.SameFile(latest, pinned) {
			result.Value, result.Availability, result.Reason = role, "known", ""
		}
	}
	return result
}

func provePhysicalFunction(root *os.Root, physfn string, link, pci, vf os.FileInfo) bool {
	target, err := root.Readlink(physfn)
	if err != nil {
		return false
	}
	pfAddress := filepath.Base(target)
	pf, ok := indexedPCIDevice(root, pfAddress, pci)
	resolved, err := root.Stat(physfn)
	if !ok || err != nil || !os.SameFile(pf, resolved) || os.SameFile(pf, vf) {
		return false
	}
	// A PF cannot also point to another PF as a VF would.
	_, targetRoleErr := root.Lstat(filepath.Join("bus", "pci", "devices", pfAddress, "physfn"))
	currentLink, linkErr := root.Lstat(physfn)
	currentTarget, targetErr := root.Stat(physfn)
	currentPF, indexed := indexedPCIDevice(root, pfAddress, pci)
	return indexed && errors.Is(targetRoleErr, os.ErrNotExist) && linkErr == nil && targetErr == nil && os.SameFile(link, currentLink) &&
		os.SameFile(pf, currentTarget) && os.SameFile(pf, currentPF)
}
