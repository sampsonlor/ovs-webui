//go:build linux

package fieldexecution

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/provider/linux"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type nativeNeighbor struct {
	ID, Address, Family, MAC string
	IfIndex                  int
	InterfaceRef             apitypes.Ref `json:"interface_ref"`
}

type nativeNeighborPage struct {
	Snapshot     string `json:"snapshot_id"`
	Availability string
	Reason       string
	Items        []nativeNeighbor
	Source       struct {
		Freshness  string
		ObservedAt time.Time `json:"observed_at"`
	}
	Coverage struct {
		Eligible int `json:"eligible_interfaces"`
		Observed int `json:"observed_interfaces"`
	}
}

type kernelFixtureNeighbor struct {
	Address string   `json:"dst"`
	MAC     string   `json:"lladdr"`
	State   []string `json:"state"`
}

func (f *fixture) fixtureNeighborRows(family, address, device string) ([]kernelFixtureNeighbor, error) {
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "ip", "-j", family, "neigh", "show", "to", address, "dev", device).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("read kernel fixture neighbor on %s: %w: %s", device, err, raw)
	}
	var rows []kernelFixtureNeighbor
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, fmt.Errorf("kernel fixture neighbor read did not return an array")
	}
	return rows, nil
}

func fixturePermanentNeighbor(rows []kernelFixtureNeighbor, address, mac string) bool {
	return len(rows) == 1 && rows[0].Address == address && rows[0].MAC == mac &&
		slices.Contains(rows[0].State, "PERMANENT")
}

func (p nativeNeighborPage) complete() bool {
	return p.Snapshot != "" && p.Availability == "complete" &&
		p.Source.Freshness == "fresh" && !p.Source.ObservedAt.IsZero() &&
		p.Coverage.Eligible == 1 && p.Coverage.Observed == 1
}

func (p nativeNeighborPage) neighbor(address string) nativeNeighbor {
	for _, row := range p.Items {
		if row.Address == address {
			return row
		}
	}
	return nativeNeighbor{}
}

func (f *fixture) readNativeNeighbors(query url.Values, grant string) (nativeNeighborPage, error) {
	uri := "/api/v1/neighbors?" + query.Encode()
	out, err := f.auth.ReadAuth(f.ctx, grant, authn.Query{Method: "GET", URI: uri})
	if err != nil {
		return nativeNeighborPage{}, err
	}
	var page nativeNeighborPage
	err = json.Unmarshal(out.Body, &page)
	return page, err
}

func TestNativeNeighborEventCache(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires the explicit isolated Linux OVS fixture")
	}
	const (
		ipv4   = "192.0.2.30"
		ipv6   = "2001:db8:44:5::2"
		other  = "198.51.100.30"
		hidden = "203.0.113.30"
		macA   = "02:00:44:05:00:01"
		macB   = "02:00:44:05:00:02"
		macC   = "02:00:44:05:00:03"
	)
	f := newFixture(t)
	reader := f.createLinkReader()
	f.inventory.SetNeighborObserver(linux.New())
	suffix := repository.NewID()[:6]
	bridge := "bn" + suffix
	f.vs("add-br", bridge, "--", "set", "Bridge", bridge, "datapath_type=system")
	t.Cleanup(func() { f.vs("--if-exists", "del-br", bridge) })
	addPort := func(prefix string) (link *net.Interface, peer *net.Interface) {
		name, peerName := prefix+suffix, "c"+prefix+suffix
		f.run("ip", "link", "add", name, "type", "veth", "peer", "name", peerName)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })
		f.run("ip", "link", "set", peerName, "up")
		f.run("ip", "link", "set", name, "up")
		before, err := net.InterfaceByName(name)
		must(t, err)
		peer, err = net.InterfaceByName(peerName)
		must(t, err)
		if before.Index <= 0 || peer.Index <= 0 || before.Index == peer.Index ||
			before.Flags&net.FlagUp == 0 || peer.Flags&net.FlagUp == 0 {
			t.Fatal("fixture veth must be up before native attachment", before, peer)
		}
		// Wait for ovs-vswitchd to process the port addition before seeding
		// the neighbor table. The shared fixture's f.vs uses --no-wait.
		f.run("ovs-vsctl", "--timeout=5", "--db=unix:"+f.dbSocket, "add-port", bridge, name)
		link, err = net.InterfaceByName(name)
		must(t, err)
		ovsLink := f.vs("get", "Interface", name, "ifindex", "ofport", "error")
		fields := strings.Split(ovsLink, "\n")
		if link.Index != before.Index || link.Flags&net.FlagUp == 0 || len(fields) != 3 ||
			strings.Trim(fields[0], "[]") != strconv.Itoa(link.Index) || fields[2] != "[]" {
			t.Fatalf("native attachment did not retain the fixture link ifindex %d: %q", before.Index, ovsLink)
		}
		ofport, err := strconv.Atoi(strings.Trim(fields[1], "[]"))
		if err != nil || ofport <= 0 {
			t.Fatalf("native attachment did not admit an OVS ofport: %q", ovsLink)
		}
		return link, peer
	}
	firstLink, peer := addPort("pna")
	secondLink, _ := addPort("pnb")
	f.run("sysctl", "-q", "-w", "net.ipv6.conf."+firstLink.Name+".disable_ipv6=0")
	f.run("ip", "-6", "addr", "add", "2001:db8:44:5::1/64", "nodad", "dev", firstLink.Name)
	replace := func(family, address, mac, device string) {
		f.run("ip", family, "neigh", "replace", address, "lladdr", mac, "nud", "permanent", "dev", device)
	}
	expected := []struct{ family, address, device string }{
		{"-4", ipv4, firstLink.Name}, {"-6", ipv6, firstLink.Name}, {"-4", other, secondLink.Name},
	}
	for _, row := range expected {
		existing, err := f.fixtureNeighborRows(row.family, row.address, row.device)
		must(t, err)
		if len(existing) != 0 {
			t.Fatalf("fixture neighbor existed before permanent setup: %+v", existing)
		}
		replace(row.family, row.address, macA, row.device)
		installed, err := f.fixtureNeighborRows(row.family, row.address, row.device)
		must(t, err)
		if !fixturePermanentNeighbor(installed, row.address, macA) {
			t.Fatalf("kernel did not retain the exact permanent fixture row %s on %s: %+v", row.address, row.device, installed)
		}
	}
	firstID := f.interfaceBinding(firstLink.Name).ManagementID
	secondID := f.interfaceBinding(secondLink.Name).ManagementID
	firstScope := url.Values{"interface_id": {firstID}}
	secondScope := url.Values{"interface_id": {secondID}}
	read := func(query url.Values, grant string) nativeNeighborPage {
		page, err := f.readNativeNeighbors(query, grant)
		must(t, err)
		return page
	}

	var initial, unaffected nativeNeighborPage
	var initialized bool
	t.Cleanup(func() {
		if initialized {
			return
		}
		t.Logf("initial Neighbor cache first={availability:%s reason:%s freshness:%s coverage:%+v IPv4:%+v IPv6:%+v} second={availability:%s reason:%s freshness:%s coverage:%+v IPv4:%+v}",
			initial.Availability, initial.Reason, initial.Source.Freshness, initial.Coverage, initial.neighbor(ipv4), initial.neighbor(ipv6),
			unaffected.Availability, unaffected.Reason, unaffected.Source.Freshness, unaffected.Coverage, unaffected.neighbor(other))
		allPresent := true
		for _, row := range expected {
			kernel, err := f.fixtureNeighborRows(row.family, row.address, row.device)
			present := err == nil && fixturePermanentNeighbor(kernel, row.address, macA)
			allPresent = allPresent && present
			t.Logf("initial neighbor %s on %s permanent=%t rows=%+v error=%v", row.address, row.device, present, kernel, err)
		}
		t.Logf("all initial permanent fixture neighbors still present: %t", allPresent)
		for _, link := range []*net.Interface{firstLink, secondLink} {
			t.Logf("initial OVS Interface %s expected-ifindex=%d ifindex/ofport/error=%q", link.Name, link.Index,
				f.vs("get", "Interface", link.Name, "ifindex", "ofport", "error"))
		}
	})
	waitFor(t, func() bool {
		f.inventory.SampleNeighbors(f.ctx)
		initial = read(firstScope, f.login.Grant)
		unaffected = read(secondScope, f.login.Grant)
		return initial.complete() && unaffected.complete() &&
			initial.neighbor(ipv4).MAC == macA && initial.neighbor(ipv6).MAC == macA &&
			unaffected.neighbor(other).MAC == macA
	})
	initialized = true
	initialIPv4, initialIPv6 := initial.neighbor(ipv4), initial.neighbor(ipv6)
	if initialIPv4.Family != "ipv4" || initialIPv6.Family != "ipv6" || initialIPv4.ID == initialIPv6.ID ||
		initialIPv4.IfIndex != firstLink.Index || initialIPv6.IfIndex != firstLink.Index ||
		initialIPv4.InterfaceRef != (apitypes.Ref{Kind: "interface", ID: firstID}) ||
		initialIPv6.InterfaceRef != initialIPv4.InterfaceRef {
		t.Fatal("native IPv4 and IPv6 observations have an incorrect OVS binding", initialIPv4, initialIPv6)
	}
	if shared := read(firstScope, reader.Grant); !shared.complete() || shared.Snapshot != initial.Snapshot ||
		!shared.Source.ObservedAt.Equal(initial.Source.ObservedAt) || shared.neighbor(ipv4) != initialIPv4 {
		t.Fatal("the separate Reader grant did not get the initial shared snapshot", shared)
	}

	// Leave the periodic collector off here. A received real kernel hint must
	// affect only its current managed association before any further dump.
	watchCtx, cancelWatch := context.WithCancel(f.ctx)
	watchDone := make(chan struct{})
	stopWatch := func() { cancelWatch(); <-watchDone }
	defer stopWatch()
	indices := make(chan int, 16)
	go func() {
		defer close(watchDone)
		linux.WatchNeighborEvents(watchCtx, func(events inventory.InterfaceEvents) {
			f.inventory.HandleNeighborEvents(events)
			for _, index := range events.Indices {
				if index == firstLink.Index || index == peer.Index {
					select {
					case indices <- index:
					default:
					}
				}
			}
		})
	}()
	seenIndex := func(index int) bool {
		for range len(indices) {
			if <-indices == index {
				return true
			}
		}
		return false
	}
	currentMAC := macA
	waitFor(t, func() bool {
		if seenIndex(peer.Index) {
			return true
		}
		if currentMAC == macB {
			currentMAC = macC
		} else {
			currentMAC = macB
		}
		replace("-4", hidden, currentMAC, peer.Name)
		return false
	})
	if after := read(firstScope, reader.Grant); !after.complete() || after.Snapshot != initial.Snapshot ||
		!after.Source.ObservedAt.Equal(initial.Source.ObservedAt) || after.neighbor(ipv4) != initialIPv4 {
		t.Fatal("an unmanaged peer's hint changed the managed snapshot", after)
	}

	replace("-4", ipv4, macB, firstLink.Name)
	waitFor(t, func() bool { return seenIndex(firstLink.Index) })
	assertInvalidated := func(baseline nativeNeighborPage) {
		t.Helper()
		for _, grant := range []string{f.login.Grant, reader.Grant} {
			affected := read(firstScope, grant)
			if affected.Availability != "unavailable" || affected.Reason != "NEIGHBORS_RESYNCING" ||
				affected.Source.Freshness != "unavailable" || affected.Coverage.Eligible != 1 ||
				affected.Coverage.Observed != 0 || len(affected.Items) != 0 {
				t.Fatal("a managed hint retained old rows or coverage before an independent dump", affected)
			}
			stillCovered := read(secondScope, grant)
			if !stillCovered.complete() || !stillCovered.Source.ObservedAt.Equal(baseline.Source.ObservedAt) ||
				stillCovered.neighbor(other) != baseline.neighbor(other) {
				t.Fatal("a targeted hint withheld an unrelated managed Interface", stillCovered)
			}
		}
	}
	assertInvalidated(unaffected)

	// Start the production collector to verify the authoritative follow-up.
	startCollector := func() func() {
		collectCtx, cancelCollect := context.WithCancel(f.ctx)
		collectDone := make(chan struct{})
		go func() {
			defer close(collectDone)
			f.inventory.RunNeighbors(collectCtx)
		}()
		return func() { cancelCollect(); <-collectDone }
	}
	stopCollector := startCollector()
	defer func() { stopCollector() }()
	var refreshed nativeNeighborPage
	waitFor(t, func() bool {
		refreshed = read(firstScope, reader.Grant)
		row := refreshed.neighbor(ipv4)
		return refreshed.complete() && refreshed.Snapshot != initial.Snapshot &&
			refreshed.Source.ObservedAt.After(initial.Source.ObservedAt) &&
			row.ID == initialIPv4.ID && row.MAC == macB &&
			refreshed.neighbor(ipv6).ID == initialIPv6.ID
	})
	if shared := read(firstScope, f.login.Grant); !shared.complete() || shared.Snapshot != refreshed.Snapshot ||
		shared.neighbor(ipv4) != refreshed.neighbor(ipv4) {
		t.Fatal("readers did not share the newly qualified update", shared)
	}
	if hiddenPage := read(url.Values{"filter": {hidden}}, reader.Grant); len(hiddenPage.Items) != 0 {
		t.Fatal("an unmanaged kernel row entered the shared Neighbor page", hiddenPage)
	}

	f.run("ip", "-4", "neigh", "del", ipv4, "dev", firstLink.Name)
	waitFor(t, func() bool { return seenIndex(firstLink.Index) })
	waitFor(t, func() bool {
		page := read(firstScope, f.login.Grant)
		return page.complete() && page.Snapshot != refreshed.Snapshot &&
			page.neighbor(ipv4).ID == "" && page.neighbor(ipv6).ID == initialIPv6.ID
	})

	// Pause the collector to make the IPv6 notification observable on its own.
	// Drain prior indices so the replacement must yield a new multicast hint.
	stopCollector()
	beforeIPv6 := read(firstScope, reader.Grant)
	unaffectedIPv6 := read(secondScope, reader.Grant)
	if !beforeIPv6.complete() || beforeIPv6.neighbor(ipv6) != initialIPv6 {
		t.Fatal("the previously observed IPv6 neighbor was not present before replacement", beforeIPv6)
	}
	for range len(indices) {
		<-indices
	}
	replace("-6", ipv6, macC, firstLink.Name)
	waitFor(t, func() bool { return seenIndex(firstLink.Index) })
	assertInvalidated(unaffectedIPv6)

	stopCollector = startCollector()
	waitFor(t, func() bool {
		page := read(firstScope, reader.Grant)
		row := page.neighbor(ipv6)
		return page.complete() && page.Snapshot != beforeIPv6.Snapshot &&
			page.Source.ObservedAt.After(beforeIPv6.Source.ObservedAt) &&
			row.ID == initialIPv6.ID && row.MAC == macC && page.neighbor(ipv4).ID == ""
	})

	stopWatch()
	// Observe a completed round with the listener stopped before triggering the
	// final change. This keeps the fallback proof independent of a queued hint.
	beforeFallback := read(secondScope, reader.Grant)
	waitFor(t, func() bool {
		page := read(secondScope, reader.Grant)
		return page.complete() && page.neighbor(other).MAC == macA &&
			page.Source.ObservedAt.After(beforeFallback.Source.ObservedAt)
	})
	replace("-4", other, macC, secondLink.Name)
	waitFor(t, func() bool {
		page := read(secondScope, reader.Grant)
		return page.complete() && page.neighbor(other).MAC == macC
	})
	if f.proxy.sent.Load() != 0 {
		t.Fatal("neighbor observation issued a manager-side OVS mutation")
	}
}
