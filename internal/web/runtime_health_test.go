package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type runtimeManagerFixture struct {
	authn.Manager
	claims         authn.Claims
	report         ipc.Health
	authErr        error
	inspectErr     error
	inspectionCall int
}

func (m *runtimeManagerFixture) Probe(context.Context) (ipc.Health, error) {
	return ipc.BootstrapHealth(), nil
}

func (m *runtimeManagerFixture) InspectAuth(context.Context, string) (authn.Claims, error) {
	return m.claims, m.authErr
}

func (m *runtimeManagerFixture) CheckAuth(_ context.Context, _ string, check authn.Check) (authn.Claims, error) {
	if m.authErr != nil {
		return authn.Claims{}, m.authErr
	}
	if check.Capability != "state.read" || !slices.Contains(m.claims.Capabilities, check.Capability) {
		return authn.Claims{}, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	if len(check.Resources) != 0 {
		return authn.Claims{}, apitypes.Fail(503, "OBJECT_AUTHORITY_UNAVAILABLE")
	}
	return m.claims, nil
}

func (m *runtimeManagerFixture) Inspect(context.Context, string) (ipc.Health, error) {
	m.inspectionCall++
	return m.report, m.inspectErr
}

func fixtureRuntimeHTTP(t *testing.T, storage repository.Status) (*runtimeManagerFixture, func(string) *httptest.ResponseRecorder) {
	t.Helper()
	past := time.Now().UTC().Add(-time.Second)
	manager := &runtimeManagerFixture{
		claims: authn.Claims{PrincipalID: "00000000-0000-4000-8000-000000000001", CredentialID: "00000000-0000-4000-8000-000000000002",
			CredentialKind: "token", Revision: "fixture-permissions", Capabilities: []string{"state.read", "inventory.read", "capabilities.read"}},
		report: ipc.Health{State: "ready", Scope: "runtime-inspection", AuthenticationReady: true, ConfigurationReady: true,
			Components: []ipc.HealthComponent{
				{Key: "manager", State: "ready", Reason: "PROCESS_SERVING", Authority: "manager-process", ObservedAt: &past},
				{Key: "ovsdb", State: "ready", Reason: "OVSDB_SNAPSHOT_CONFIRMED", Authority: "ovsdb-monitor", ObservedAt: &past},
				{Key: "safety-watchdog", State: "ready", Reason: "SAFETY_CYCLE_CURRENT", Authority: "safety-loop", ObservedAt: &past},
				{Key: "cache-statistics", State: "stale", Reason: "STATISTICS_EXPIRED", Authority: "statistics-cache", ObservedAt: &past},
				{Key: "unregistered-private-provider", State: "ready", Reason: "NOT_EXPOSED", Authority: "hidden"},
			}},
	}
	handler, closeAPI, err := PublicHandler(t.Context(), manager, func(context.Context) repository.Status { return storage },
		"https://console.example", NewAuthentication(manager, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAPI)
	return manager, func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://console.example/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer ovst_"+strings.Repeat("A", 43))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
}

func TestRuntimeHealthComposesIndependentSources(t *testing.T) {
	manager, read := fixtureRuntimeHTTP(t, repository.Status{State: "degraded", Code: "WEB_STORE_READ_ONLY", Readable: true})
	resp := read("/health")
	var value healthReadResource
	if resp.Code != 200 || json.Unmarshal(resp.Body.Bytes(), &value) != nil {
		t.Fatalf("health status %d: %s", resp.Code, resp.Body.String())
	}
	if value.ID != runtimeHealthID || value.Sequence != "1" || value.Kind != "health" ||
		value.State != "degraded" || value.AuthenticationReady || value.ConfigurationReady || len(value.AllowedActions) != 0 {
		t.Fatalf("unexpected health resource %+v", value)
	}
	components := map[string]healthComponent{}
	for _, component := range value.Components {
		components[component.Key] = component
	}
	if _, hidden := components["unregistered-private-provider"]; hidden {
		t.Fatal("private provider escaped the public projection")
	}
	web, statistics := components["web-db"], components["cache-statistics"]
	if web.State != "degraded" || web.Source.ObservedAt == nil || web.Source.ProviderID != "ovs-webd" ||
		statistics.State != "stale" || statistics.Source.Freshness != "stale" || statistics.Source.ProviderID != "ovs-mgrd" ||
		statistics.Source.ObservedAt == nil || !statistics.Source.ObservedAt.Equal(*manager.report.Components[3].ObservedAt) {
		t.Fatalf("provider observations were conflated or refreshed: web=%+v stats=%+v", web, statistics)
	}
	manager.inspectErr = errors.New("transport failed")
	if response := read("/health"); response.Code != 503 || strings.Contains(response.Body.String(), "transport failed") {
		t.Fatalf("disconnected manager was represented as a healthy result: %s", response.Body.String())
	}
	manager.authErr = errors.New("manager not reachable")
	priorCalls := manager.inspectionCall
	if response := read("/health"); response.Code != 503 || manager.inspectionCall != priorCalls {
		t.Fatalf("manager disconnection bypassed authentication: %s", response.Body.String())
	}
}

func TestRuntimeCapabilitiesRequireBothGrantsAndKnownIdentity(t *testing.T) {
	manager, read := fixtureRuntimeHTTP(t, repository.Status{State: "ready", Readable: true, Writable: true})
	manager.claims.Capabilities = []string{"state.read", "inventory.read"}
	id := runtimeCapabilities[1].id
	if response := read("/capabilities"); response.Code != 403 {
		t.Fatalf("capability list accepted state.read alone: %s", response.Body.String())
	}
	if response := read("/capabilities/" + id); response.Code != 403 {
		t.Fatalf("capability detail accepted state.read alone: %s", response.Body.String())
	}
	if manager.inspectionCall != 0 {
		t.Fatal("a denied request inspected evidence")
	}
	manager.claims.Capabilities = append(manager.claims.Capabilities, "capabilities.read")
	if response := read("/capabilities/" + runtimeHealthID); response.Code != 404 {
		t.Fatalf("non-registry identity was accepted: %s", response.Body.String())
	}
	response := read("/capabilities/" + id)
	var row capabilityReadResource
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &row) != nil {
		t.Fatalf("known capability detail failed: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"capability_evidence":`) || strings.Contains(response.Body.String(), `"capability":`) {
		t.Fatalf("capability evidence reused the Job and Event capability wire name: %s", response.Body.String())
	}
	if row.ID != id || row.Capability.Key != "vlan" || row.Capability.Availability != "unknown" ||
		row.Capability.GrantState != "denied" || row.Capability.Reason != "TARGET_AUTHORITY_REQUIRED" || len(row.AllowedActions) != 0 {
		t.Fatalf("runtime readiness manufactured blanket write capability: %+v", row)
	}
	manager.report.ConfigurationReady = false
	response = read("/capabilities/" + id)
	if json.Unmarshal(response.Body.Bytes(), &row) != nil || row.Capability.Recovery != "unavailable" || row.Capability.Reason != "SAFE_APPLY_UNAVAILABLE" {
		t.Fatalf("missing recovery was not reflected: %s", response.Body.String())
	}
	manager.claims.Capabilities = []string{"capabilities.read"}
	if response := read("/health"); response.Code != 403 {
		t.Fatalf("capabilities.read bypassed the state.read health gate: %s", response.Body.String())
	}
}

func TestRuntimeCapabilityPagesBindFilterPermissionAndEvidence(t *testing.T) {
	manager, read := fixtureRuntimeHTTP(t, repository.Status{State: "ready", Readable: true, Writable: true})
	pageOf := func(path string) capabilityPage {
		t.Helper()
		response := read(path)
		var page capabilityPage
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
			t.Fatalf("capability list %d: %s", response.Code, response.Body.String())
		}
		return page
	}
	first := pageOf("/capabilities?limit=2")
	if first.NextCursor == nil || !apitypes.ManagementID(first.SnapshotID) || len(first.Items) != 2 {
		t.Fatalf("invalid first page: %+v", first)
	}
	cursor := url.QueryEscape(*first.NextCursor)
	second := pageOf("/capabilities?limit=2&cursor=" + cursor)
	if second.SnapshotID != first.SnapshotID || len(second.Items) != 2 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("invalid second page: %+v", second)
	}
	filtered := pageOf("/capabilities?filter=openflow")
	if len(filtered.Items) != 1 || filtered.Items[0].Capability.Availability != "unavailable" ||
		filtered.Items[0].Source.ObservedAt != nil || filtered.NextCursor != nil {
		t.Fatalf("unproven provider evidence was manufactured: %+v", filtered)
	}
	if response := read("/capabilities?limit=2&filter=bridge&cursor=" + cursor); response.Code != 410 {
		t.Fatalf("cursor was accepted for another selection: %s", response.Body.String())
	}
	manager.claims.Revision = "changed-permissions"
	if response := read("/capabilities?limit=2&cursor=" + cursor); response.Code != 410 {
		t.Fatalf("cursor was accepted after permission revision: %s", response.Body.String())
	}
}
