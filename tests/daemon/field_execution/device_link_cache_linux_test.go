//go:build linux

package fieldexecution

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/linux"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type nativeLinkObservation struct {
	ID     string `json:"management_id"`
	Device struct {
		Availability, Reason string
		Index                *int `json:"ifindex"`
		Fields               map[string]struct {
			Value json.RawMessage
		}
		Source struct {
			Freshness  string
			ObservedAt *time.Time `json:"observed_at"`
		}
	} `json:"linux_device"`
}

func (v nativeLinkObservation) matches(index, mtu int) bool {
	return v.Device.Availability == "known" && v.Device.Index != nil && *v.Device.Index == index &&
		v.Device.Source.Freshness == "fresh" && v.Device.Source.ObservedAt != nil &&
		string(v.Device.Fields["mtu"].Value) == strconv.Itoa(mtu)
}

func (v nativeLinkObservation) unavailable(reason string) bool {
	return v.Device.Availability == "unavailable" && v.Device.Reason == reason &&
		v.Device.Index == nil && len(v.Device.Fields) == 0 &&
		v.Device.Source.Freshness == "unavailable" && v.Device.Source.ObservedAt == nil
}

// Use the same manager operation and current authorization as the inventory
// IPC handler. Separate principals must get the same manager-owned sample.
func (f *fixture) readLinkObservation(id, grant string) (nativeLinkObservation, error) {
	out, err := f.auth.ReadAuth(f.ctx, grant, authn.Query{Method: "GET", URI: "/api/v1/interfaces/" + id})
	if err != nil {
		return nativeLinkObservation{}, err
	}
	var value nativeLinkObservation
	err = json.Unmarshal(out.Body, &value)
	return value, err
}

func (f *fixture) createLinkReader() authn.LoginResult {
	f.t.Helper()
	const adminPassword = "synthetic-native-field-password"
	const readerPassword = "synthetic-native-link-reader"
	claims, err := f.auth.Reauthenticate(f.ctx, f.login.Grant, authn.Reauthentication{Password: adminPassword})
	must(f.t, err)
	out, err := f.auth.ReadAuth(f.ctx, f.login.Grant, authn.Query{Method: "GET", URI: "/api/v1/roles"})
	must(f.t, err)
	var roles struct{ Items []struct{ ID, Name string } }
	must(f.t, json.Unmarshal(out.Body, &roles))
	var roleID string
	for _, role := range roles.Items {
		if role.Name == "Reader" {
			roleID = role.ID
			break
		}
	}
	if roleID == "" {
		f.t.Fatal("missing Reader role in the native fixture")
	}
	id := apitypes.RequestID(time.Now())
	body, err := json.Marshal(map[string]any{"username": "link-reader", "password": readerPassword, "role_ids": []string{roleID}, "request_id": id})
	must(f.t, err)
	_, err = f.auth.ExecuteAuth(f.ctx, f.login.Grant, authn.Command{
		Method: "POST", URI: "/api/v1/users", Epoch: claims.RequestEpoch, RequestID: id, Payload: body,
	})
	must(f.t, err)
	reader, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "link-reader", Password: readerPassword})
	must(f.t, err)
	return reader
}

func TestNativeDeviceLinkCache(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires the explicit isolated Linux OVS fixture")
	}
	f := newFixture(t)
	reader := f.createLinkReader()
	f.inventory.SetDeviceObserver(linux.New())
	suffix := repository.NewID()[:6]
	bridge, name, peer := "bl"+suffix, "pl"+suffix, "cl"+suffix
	f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system")
	t.Cleanup(func() {
		f.vs("--if-exists", "del-br", bridge)
		_ = exec.Command("ip", "link", "del", name).Run()
	})
	createLink := func() *net.Interface {
		f.run("ip", "link", "add", name, "type", "veth", "peer", "name", peer)
		f.run("ip", "link", "set", peer, "up")
		f.run("ip", "link", "set", name, "up")
		info, err := net.InterfaceByName(name)
		must(t, err)
		return info
	}
	link := createLink()
	f.vs("add-port", bridge, name)
	original := f.interfaceBinding(name)
	read := func(id, grant string) nativeLinkObservation {
		value, err := f.readLinkObservation(id, grant)
		must(t, err)
		return value
	}
	sample := func(id string, idx, mtu int) nativeLinkObservation {
		var value nativeLinkObservation
		waitFor(t, func() bool {
			f.inventory.SampleDevices(f.ctx)
			got, err := f.readLinkObservation(id, f.login.Grant)
			value = got
			return err == nil && value.matches(idx, mtu)
		})
		return value
	}
	first := sample(original.ManagementID, link.Index, link.MTU)
	if other := read(original.ManagementID, reader.Grant); !other.matches(link.Index, link.MTU) ||
		!other.Device.Source.ObservedAt.Equal(*first.Device.Source.ObservedAt) {
		t.Fatal("the second principal did not receive the original shared observation", other)
	}

	// Periodic sampling stays off while testing invalidation. A real link change
	// must remove the old proof before any new sysfs observation is attempted.
	watchCtx, cancelWatch := context.WithCancel(f.ctx)
	watchDone := make(chan struct{})
	stopWatch := func() { cancelWatch(); <-watchDone }
	defer stopWatch()
	hints := make(chan struct{}, 1)
	go func() {
		defer close(watchDone)
		linux.WatchDeviceLinks(watchCtx, func(update inventory.DeviceLinkEvents) {
			f.inventory.HandleDeviceLinkEvents(update)
			if slices.Contains(update.Indices, link.Index) {
				select {
				case hints <- struct{}{}:
				default:
				}
			}
		})
	}()
	mtu := link.MTU
	waitFor(t, func() bool {
		select {
		case <-hints:
			if value := read(original.ManagementID, f.login.Grant); !value.unavailable("LINUX_OBSERVATION_PENDING") {
				t.Fatal("a real targeted kernel hint did not invalidate the known sample", value)
			}
			return true
		default:
		}
		// If the subscription was not bound yet, another genuine MTU change
		// provides a fresh hint. The assertions depend on state, not timing.
		if mtu == 1600 {
			mtu = 1700
		} else {
			mtu = 1600
		}
		f.run("ip", "link", "set", name, "mtu", strconv.Itoa(mtu))
		return false
	})
	if other := read(original.ManagementID, reader.Grant); !other.unavailable("LINUX_OBSERVATION_PENDING") {
		t.Fatal("a second read returned invalidated kernel fields or started sampling", other)
	}
	changed := sample(original.ManagementID, link.Index, mtu)
	if other := read(original.ManagementID, reader.Grant); !other.matches(link.Index, mtu) ||
		!changed.Device.Source.ObservedAt.After(*first.Device.Source.ObservedAt) ||
		!other.Device.Source.ObservedAt.Equal(*changed.Device.Source.ObservedAt) {
		t.Fatal("readers did not share renewed native MTU evidence", changed, other)
	}

	// Keep the native OVSDB association stable while Linux removes and
	// recreates the name. The replacement's ifindex must not be adopted.
	pid := f.pid("switch")
	must(t, syscall.Kill(pid, syscall.SIGSTOP))
	defer syscall.Kill(pid, syscall.SIGCONT)
	f.run("ip", "link", "del", name)
	waitFor(t, func() bool {
		value, err := f.readLinkObservation(original.ManagementID, f.login.Grant)
		return err == nil && value.unavailable("LINUX_OBSERVATION_PENDING")
	})
	f.inventory.SampleDevices(f.ctx)
	if missing := read(original.ManagementID, reader.Grant); !missing.unavailable("LINUX_DEVICE_NOT_FOUND") {
		t.Fatal("a deleted link retained Linux device evidence", missing)
	}
	replacement := createLink()
	if replacement.Index == link.Index {
		t.Fatal("same-name veth replacement reused the test ifindex")
	}
	f.inventory.SampleDevices(f.ctx)
	if mismatched := read(original.ManagementID, reader.Grant); !mismatched.unavailable("LINUX_IFINDEX_MISMATCH") {
		t.Fatal("unrelated kernel ifindex was adopted under the old OVS identity", mismatched)
	}

	must(t, syscall.Kill(pid, syscall.SIGCONT))
	f.vs("del-port", bridge, name)
	waitFor(t, func() bool {
		var problem *apitypes.Problem
		_, err := f.readLinkObservation(original.ManagementID, f.login.Grant)
		return errors.As(err, &problem) && problem.Status == 404
	})
	f.vs("add-port", bridge, name)
	rebound := f.interfaceBinding(name)
	if original.ManagementID == rebound.ManagementID || original.OVSUUID == rebound.OVSUUID {
		t.Fatal("retired OVS Interface identity carried over to the replacement")
	}
	updated := sample(rebound.ManagementID, replacement.Index, replacement.MTU)
	if updated.ID != rebound.ManagementID {
		t.Fatal("observed evidence belongs to the wrong management identity", updated)
	}

	// With the watcher stopped, exercise the running periodic fallback after
	// its initial sample. No notification or browser read can drive this refresh.
	stopWatch()
	observeCtx, cancelObserve := context.WithCancel(f.ctx)
	observeDone := make(chan struct{})
	defer func() { cancelObserve(); <-observeDone }()
	go func() {
		defer close(observeDone)
		f.inventory.RunDeviceObservations(observeCtx)
	}()
	waitFor(t, func() bool {
		value, err := f.readLinkObservation(rebound.ManagementID, reader.Grant)
		return err == nil && value.matches(replacement.Index, replacement.MTU) &&
			value.Device.Source.ObservedAt.After(*updated.Device.Source.ObservedAt)
	})
	f.run("ip", "link", "set", name, "mtu", "1800")
	waitFor(t, func() bool {
		value, err := f.readLinkObservation(rebound.ManagementID, reader.Grant)
		return err == nil && value.matches(replacement.Index, 1800)
	})
	if f.proxy.sent.Load() != 0 {
		t.Fatal("kernel observation issued a manager-side OVS mutation")
	}
}
