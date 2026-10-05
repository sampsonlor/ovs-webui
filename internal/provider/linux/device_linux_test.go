//go:build linux

package linux

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func sysfs(t *testing.T, kind string) (string, string) {
	t.Helper()
	root := t.TempDir()
	parent := "devices/virtual"
	if kind == "pci" {
		parent = "devices/pci0000:00/0000:01:00.0"
	} else if kind == "usb" {
		parent = "devices/pci0000:00/0000:01:00.0/usb1/1-1"
	}
	path := filepath.Join(parent, "net", "synthetic0")
	for _, dir := range []string{path, "class/net", "bus/pci", "bus/usb", "bus/pci/drivers/synthetic-driver", "bus/usb/drivers/synthetic-usb"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rel, _ := filepath.Rel(filepath.Join(root, "class/net"), filepath.Join(root, path))
	if err := os.Symlink(rel, filepath.Join(root, "class/net/synthetic0")); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"ifindex": "7", "carrier": "1", "operstate": "up", "mtu": "9000", "speed": "10000", "duplex": "full"} {
		if err := os.WriteFile(filepath.Join(root, path, key), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if kind != "virtual" {
		bus := "pci"
		driver := "synthetic-driver"
		if kind == "usb" {
			bus, driver = "usb", "synthetic-usb"
		}
		for name, target := range map[string]string{"subsystem": "bus/" + bus, "driver": "bus/" + bus + "/drivers/" + driver} {
			rel, _ := filepath.Rel(filepath.Join(root, parent), filepath.Join(root, target))
			if err := os.Symlink(rel, filepath.Join(root, parent, name)); err != nil {
				t.Fatal(err)
			}
		}
		for key, value := range map[string]string{"vendor": "0x1234", "device": "0xabcd", "numa_node": "0"} {
			if err := os.WriteFile(filepath.Join(root, parent, key), []byte(value+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if kind == "usb" {
			controller := filepath.Join(root, "devices/pci0000:00/0000:01:00.0")
			rel, _ := filepath.Rel(controller, filepath.Join(root, "bus/pci"))
			if err := os.Symlink(rel, filepath.Join(controller, "subsystem")); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root, path
}

func TestLinuxDeviceSysfsAssociation(t *testing.T) {
	for _, kind := range []string{"virtual", "pci", "usb"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := sysfs(t, kind)
			s := newProvider(root).Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7})
			if s.Availability != "known" || s.IfIndex != 7 || s.Fields["carrier"].Value != true || s.Fields["mtu"].Value != int64(9000) || s.ObservedAt.IsZero() {
				t.Fatal(s)
			}
			if len(s.Fields) != 10 {
				t.Fatal("unbounded or incomplete projection", s)
			}
			if kind == "pci" {
				if s.Fields["pci_address"].Value != "0000:01:00.0" || s.Fields["driver"].Value != "synthetic-driver" || s.Fields["vendor_id"].Value != "0x1234" {
					t.Fatal(s)
				}
			} else if s.Fields["pci_address"].Availability != "unavailable" || s.Fields["pci_address"].Value != nil {
				t.Fatal("invented PCI association", s)
			}
			if kind == "usb" && s.Fields["driver"].Value != "synthetic-usb" {
				t.Fatal(s)
			}
			if kind == "usb" {
				if err := os.Rename(filepath.Join(root, "bus/usb"), filepath.Join(root, "bus/usb-hidden")); err != nil {
					t.Fatal(err)
				}
				missing := newProvider(root).Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7})
				if missing.Fields["pci_address"].Value != nil || missing.Fields["driver"].Value != nil {
					t.Fatal("dangling USB association borrowed the upstream PCI device", missing)
				}
			}
		})
	}
}

func TestLinuxDeviceRejectsUnprovenBindingsAndEscapes(t *testing.T) {
	root, path := sysfs(t, "virtual")
	p := newProvider(root)
	for _, r := range []inventory.DeviceRequest{{Name: "../synthetic0", IfIndex: 7}, {Name: ".", IfIndex: 7}, {Name: "synthetic0", IfIndex: 0}, {Name: "synthetic0", IfIndex: 8}, {Name: "missing0", IfIndex: 7}} {
		s := p.Observe(context.Background(), r)
		if s.Availability != "unavailable" || len(s.Fields) != 0 || !s.ObservedAt.IsZero() {
			t.Fatal("fabricated a device association", r, s)
		}
	}
	// A same-name replacement with a new ifindex cannot inherit the old binding.
	if err := os.WriteFile(filepath.Join(root, path, "ifindex"), []byte("19\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if s := p.Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7}); s.Reason != "LINUX_IFINDEX_MISMATCH" {
		t.Fatal(s)
	}
	// No attribute data may escape the fixed root through a symlink.
	if err := os.Remove(filepath.Join(root, path, "ifindex")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, path, "ifindex")); err != nil {
		t.Fatal(err)
	}
	if s := p.Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7}); s.Availability == "known" {
		t.Fatal("absolute symlink escaped root", s)
	}
}

func TestLinuxDevicePartialAttributesDoNotInventDefaults(t *testing.T) {
	root, path := sysfs(t, "pci")
	for key, value := range map[string]string{"carrier": "0", "operstate": "unknown", "speed": "-1", "mtu": "9223372036854775807", "duplex": "future"} {
		if err := os.WriteFile(filepath.Join(root, path, key), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, filepath.Dir(filepath.Dir(path)), "numa_node"), []byte("-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := newProvider(root)
	s := p.Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7})
	if s.Availability != "known" || s.Fields["carrier"].Value != false || s.Fields["operstate"].Value != "unknown" {
		t.Fatal(s)
	}
	for _, key := range []string{"mtu", "speed_mbps", "duplex", "numa_node"} {
		if s.Fields[key].Availability != "unknown" || s.Fields[key].Value != nil {
			t.Fatal("invented numeric/enum fallback", key, s)
		}
	}
	if err := os.WriteFile(filepath.Join(root, path, "carrier"), make([]byte, 1024), 0600); err != nil {
		t.Fatal(err)
	}
	if s := p.Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7}); s.Fields["carrier"].Availability != "unknown" || s.Fields["carrier"].Value != nil {
		t.Fatal("over-budget scalar exposed", s)
	}
	if err := os.Remove(filepath.Join(root, path, "carrier")); err != nil {
		t.Fatal(err)
	}
	s = p.Observe(context.Background(), inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7})
	if s.Fields["carrier"].Availability != "unavailable" || s.Fields["carrier"].Value != nil {
		t.Fatal("missing carrier became down", s)
	}
}

func TestLinuxDeviceReadBudgetAndCancellation(t *testing.T) {
	p := newProvider(t.TempDir())
	blocked, done := make(chan struct{}), make(chan struct{}, 2)
	defer close(blocked)
	var calls atomic.Int32
	p.collect = func(inventory.DeviceRequest) inventory.DeviceSample {
		calls.Add(1)
		<-blocked
		done <- struct{}{}
		return unavailable("test-finished")
	}
	req := inventory.DeviceRequest{Name: "synthetic0", IfIndex: 7}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p.Observe(ctx, req).Reason != "LINUX_OBSERVATION_CANCELLED" || calls.Load() != 0 {
		t.Fatal("cancelled request started a worker")
	}
	for i := 0; i < 2; i++ {
		began := time.Now()
		if s := p.Observe(context.Background(), req); s.Reason != "LINUX_OBSERVATION_TIMEOUT" || time.Since(began) > time.Second {
			t.Fatal("read deadline did not bound response", s)
		}
	}
	if s := p.Observe(context.Background(), req); s.Reason != "LINUX_PROVIDER_BUSY" || calls.Load() != 2 {
		t.Fatal("timed out workers did not retain bounded slots", s, calls.Load())
	}
}

func TestLinuxDeviceHostObservation(t *testing.T) {
	// Real kernel sysfs on each native CI architecture; no fixture fallback.
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	s := New().Observe(context.Background(), inventory.DeviceRequest{Name: iface.Name, IfIndex: iface.Index})
	if s.Availability != "known" || s.Fields["mtu"].Value != int64(iface.MTU) || s.Fields["pci_address"].Value != nil {
		t.Fatal(s)
	}
}
