package inventory

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type fdbTestObserver func(context.Context, FDBRequest) (FDBSample, error)

func (f fdbTestObserver) ObserveFDB(ctx context.Context, req FDBRequest) (FDBSample, error) {
	return f(ctx, req)
}

type fdbFixture struct {
	s                           *Service
	o                           Observation
	d                           Decision
	claims                      authn.Claims
	bridge, second, port, iface Binding
}

func newFDBFixture() fdbFixture {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New(nil)
	s.now = func() time.Time { return at }
	newBinding := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), UUID: repository.NewID(), Table: table, State: "active"}
	}
	bridge := newBinding("Bridge")
	second := newBinding("Bridge")
	port := newBinding("Port")
	iface := newBinding("Interface")
	root := repository.NewID()
	o := Observation{Evidence: Evidence{Root: root, ObservedAt: at, Peer: "db-1"}, Rows: Rows{
		"Open_vSwitch": {root: {UUID: root, Values: map[string]any{"bridges": []any{bridge.UUID, second.UUID}}}},
		"Bridge": {
			bridge.UUID: {UUID: bridge.UUID, Values: map[string]any{"name": "br-fdb", "ports": port.UUID}},
			second.UUID: {UUID: second.UUID, Values: map[string]any{"name": "br-other"}},
		},
		"Port":      {port.UUID: {UUID: port.UUID, Values: map[string]any{"name": "logical-bond", "interfaces": iface.UUID}}},
		"Interface": {iface.UUID: {UUID: iface.UUID, Values: map[string]any{"name": "member", "ofport": "17"}}},
	}}
	d := Decision{State: "confirmed", Generation: repository.NewID(), Bindings: make(map[string]Binding)}
	for _, b := range []Binding{bridge, second, port, iface} {
		d.Bindings[Key(b.Table, b.UUID)] = b
	}
	s.install(o, d)
	claims := authn.Claims{PrincipalID: repository.NewID(), CredentialID: repository.NewID(), Revision: repository.NewID(), Capabilities: []string{"inventory.read", "state.read"}}
	return fdbFixture{s, o, d, claims, bridge, second, port, iface}
}

func fdbValue[T any](v T) *T { return &v }

func (f fdbFixture) page(t *testing.T, q url.Values) map[string]any {
	t.Helper()
	result, err := f.s.Read(t.Context(), "listFdb", nil, q, f.claims)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func TestFDBQualifiedScopeImmutablePagesAndExactPort(t *testing.T) {
	f := newFDBFixture()
	q := url.Values{"bridge": {f.bridge.ManagementID}, "limit": {"1"}}
	off := f.page(t, q)
	if off["reason"] != "disabled" || off["availability"] != "unavailable" {
		t.Fatalf("default: %v", off)
	}
	calls := 0
	rows := []FDBNativeRow{
		{MAC: "02:13:00:00:00:10", VLAN: fdbValue(uint16(101)), OFPort: fdbValue(uint16(17)), PortName: fdbValue("logical-bond"), AgeSeconds: fdbValue(uint32(2))},
		{MAC: "02:13:00:00:00:10", VLAN: fdbValue(uint16(202)), OFPort: fdbValue(uint16(19)), PortName: fdbValue("external")},
	}
	observe := fdbTestObserver(func(_ context.Context, req FDBRequest) (FDBSample, error) {
		calls++
		if req.Bridge != f.bridge || req.Name != "br-fdb" || req.Evidence.Root != f.o.Evidence.Root {
			t.Fatalf("unexpected source request: %+v", req)
		}
		return FDBSample{f.s.now(), uint32(len(rows)), rows}, nil
	})
	if err := f.s.SetFDBObserver(observe, []string{f.bridge.ManagementID}); err != nil {
		t.Fatal(err)
	}
	if pending := f.page(t, q); pending["reason"] != "not_ready" || calls != 0 {
		t.Fatalf("pending read triggered collection or returned wrong status: %v, %d", pending, calls)
	}
	f.s.collectFDB(t.Context())
	first := f.page(t, q)
	if first["availability"] != "available" || first["freshness"] != "fresh" || first["next_cursor"] == nil {
		t.Fatalf("first page: %v", first)
	}
	cursor := first["next_cursor"].(string)
	q.Set("cursor", cursor)
	second := f.page(t, q)
	firstItem := first["items"].([]map[string]any)[0]
	secondItem := second["items"].([]map[string]any)[0]
	if firstItem["id"] == secondItem["id"] || second["next_cursor"] != nil || first["snapshot_id"] != second["snapshot_id"] {
		t.Fatalf("pagination mismatch: %v, %v", first, second)
	}
	for _, item := range []map[string]any{firstItem, secondItem} {
		row := item["fdb"].(map[string]any)
		detail, err := f.s.Read(t.Context(), "readFdbEntry", map[string]string{"fdb_entry_id": item["id"].(string)}, nil, f.claims)
		if err != nil || Digest(detail) != Digest(item) {
			t.Fatalf("detail mismatch: %v, %v", detail, err)
		}
		switch row["vlan"] {
		case uint16(101):
			if row["port_ref"] != (apitypes.Ref{Kind: "port", ID: f.port.ManagementID}) ||
				row["last_seen_at"] != f.s.now().Add(-2*time.Second) || row["port_resolution"] != "resolved" {
				t.Fatalf("live Port association or age mismatch: %v", row)
			}
		case uint16(202):
			if row["port_ref"] != nil || row["last_seen_at"] != nil || row["port_name"] != "external" || row["port_resolution"] != "unmapped" {
				t.Fatalf("invented Port/last seen or lost external name: %v", row)
			}
		default:
			t.Fatalf("unexpected native VLAN: %v", row)
		}
	}
	portPage := f.page(t, url.Values{"port": {f.port.ManagementID}, "mac": {"02-13-00-00-00-10"}, "vlan": {"101"}})
	if len(portPage["items"].([]map[string]any)) != 1 || portPage["reason"] != "partial" {
		t.Fatalf("exact Port filter with incomplete mapping: %v", portPage)
	}
	unknown := f.page(t, url.Values{"bridge": {f.second.ManagementID}})
	if unknown["reason"] != "no_managed_bridges" || unknown["availability"] != "unavailable" {
		t.Fatalf("unconfigured Bridge appeared empty: %v", unknown)
	}
	q.Set("vlan", "202")
	if _, err := f.s.Read(t.Context(), "listFdb", nil, q, f.claims); err == nil || err.Error() != "CURSOR_EXPIRED" {
		t.Fatalf("cursor replayed across filters: %v", err)
	}
	q.Del("vlan")
	other := f.claims
	other.CredentialID = repository.NewID()
	if _, err := f.s.Read(t.Context(), "listFdb", nil, q, other); err == nil || err.Error() != "CURSOR_EXPIRED" {
		t.Fatalf("cursor replayed across sessions: %v", err)
	}
	other.Capabilities = []string{"state.read"}
	if _, err := f.s.Read(t.Context(), "listFdb", nil, nil, other); err == nil || err.Error() != "CAPABILITY_DENIED" {
		t.Fatalf("missing inventory.read: %v", err)
	}
	other.Capabilities = []string{"inventory.read"}
	if _, err := f.s.Read(t.Context(), "readFdbEntry", nil, nil, other); err == nil || err.Error() != "CAPABILITY_DENIED" {
		t.Fatalf("missing state.read: %v", err)
	}
	if calls != 1 {
		t.Fatalf("HTTP reads made native queries: %d", calls)
	}
}

func TestFDBCoverageAndEpochFence(t *testing.T) {
	f := newFDBFixture()
	rows := make([]FDBNativeRow, MaxFDBRows)
	for i := range rows {
		rows[i] = FDBNativeRow{MAC: fmt.Sprintf("02:13:00:00:00:%02x", i), VLAN: fdbValue(uint16(0))}
	}
	native := FDBSample{f.s.now(), uint32(len(rows) + 1), rows}
	var fail error
	change := false
	observe := fdbTestObserver(func(_ context.Context, _ FDBRequest) (FDBSample, error) {
		if change {
			f.o.Evidence.Peer = "new-db-peer"
			f.s.install(f.o, f.d)
		}
		return native, fail
	})
	if err := f.s.SetFDBObserver(observe, []string{f.bridge.ManagementID}); err != nil {
		t.Fatal(err)
	}
	f.s.collectFDB(t.Context())
	filter := url.Values{"bridge": {f.bridge.ManagementID}, "vlan": {"1"}}
	page := f.page(t, filter)
	coverage := page["coverage"].(fdbCoverage)
	if page["availability"] != "degraded" || page["truncated"] != true ||
		coverage.EntriesSampled != MaxFDBRows || coverage.EntriesTotal != uint64(MaxFDBRows+1) || len(page["items"].([]map[string]any)) != 0 {
		t.Fatalf("truncated sample reported complete: %v", page)
	}
	fail = apitypes.Fail(503, "FDB_OBSERVATION_TIMEOUT")
	f.s.collectFDB(t.Context())
	page = f.page(t, filter)
	if page["reason"] != "timeout" || page["freshness"] != "unavailable" {
		t.Fatalf("stale rows remained after a timeout: %v", page)
	}
	fail = nil
	native = FDBSample{ObservedAt: f.s.now(), Rows: []FDBNativeRow{}}
	f.s.collectFDB(t.Context())
	page = f.page(t, filter)
	if page["availability"] != "available" || page["truncated"] != false || page["coverage"].(fdbCoverage).EntriesTotal != 0 {
		t.Fatalf("verified empty did not remain distinct: %v", page)
	}
	change = true
	f.s.collectFDB(t.Context())
	if got := f.page(t, filter); got["reason"] != "not_ready" {
		t.Fatalf("sample survived an identity change: %v", got)
	}
	f.s.Unavailable("TEST_PROVIDER_DOWN")
	if got := f.page(t, filter); got["freshness"] != "stale" {
		t.Fatalf("provider disconnect did not mark sample stale: %v", got)
	}
}
