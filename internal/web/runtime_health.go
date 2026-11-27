package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const runtimeHealthID = "31fa3492-67e6-4a39-bfce-000000000001"

type runtimeInspector interface {
	Inspect(context.Context, string) (ipc.Health, error)
}

type runtimeHealth struct {
	manager runtimeInspector
	storage func(context.Context) repository.Status
	pageKey [32]byte
}

func newRuntimeHealth(manager runtimeInspector, storage func(context.Context) repository.Status) (*runtimeHealth, error) {
	h := &runtimeHealth{manager: manager, storage: storage}
	_, err := rand.Read(h.pageKey[:])
	return h, err
}

type healthSource struct {
	ProviderID string     `json:"provider_id"`
	Authority  string     `json:"authority"`
	ObservedAt *time.Time `json:"observed_at"`
	Freshness  string     `json:"freshness"`
	Confidence string     `json:"confidence"`
}

type runtimeResource struct {
	ID             string       `json:"id"`
	State          string       `json:"state"`
	Sequence       string       `json:"sequence"`
	Kind           string       `json:"resource_kind"`
	Source         healthSource `json:"source"`
	AllowedActions []string     `json:"allowed_actions"`
}

func runtimeReadResource(id, kind, state string, source healthSource) runtimeResource {
	return runtimeResource{ID: id, State: state, Kind: kind, Sequence: "1", Source: source, AllowedActions: []string{}}
}

type healthComponent struct {
	Key      string       `json:"key"`
	State    string       `json:"state"`
	Reason   string       `json:"reason"`
	Active   *int         `json:"active,omitempty"`
	Capacity *int         `json:"capacity,omitempty"`
	Source   healthSource `json:"source"`
}

type healthReadResource struct {
	runtimeResource
	Components          []healthComponent `json:"components"`
	AuthenticationReady bool              `json:"authentication_ready"`
	ConfigurationReady  bool              `json:"configuration_ready"`
}

var managerHealthKeys = []string{
	"management-debug", "management-restart",
	"manager", "manager-db", "manager-secret-store", "ovsdb", "cache-interface-devices",
	"cache-statistics", "cache-neighbors", "cache-lldp", "cache-fdb", "safety-watchdog",
	"ipc-auth", "ipc-control", "ipc-read", "ipc-heavy", "ipc-observation",
}

var healthCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,96}$`)

func safeHealthCode(code, fallback string) string {
	if healthCode.MatchString(code) {
		return code
	}
	return fallback
}

func normalizeHealthState(state string) string {
	switch state {
	case "ready", "degraded", "unknown", "unavailable", "stale", "disabled":
		return state
	default:
		return "unknown"
	}
}

func runtimeSource(provider, authority, state string, observed *time.Time, now time.Time) healthSource {
	source := healthSource{ProviderID: provider, Authority: authority, Freshness: "unknown", Confidence: "unknown"}
	if observed != nil && !observed.IsZero() && !observed.After(now) {
		timestamp := observed.UTC()
		source.ObservedAt = &timestamp
	}
	switch {
	case state == "unavailable" || state == "disabled":
		source.Freshness = "unavailable"
	case state == "stale":
		source.Freshness = "stale"
		if source.ObservedAt != nil {
			source.Confidence = "partial"
		}
	case state == "ready" && source.ObservedAt != nil:
		source.Freshness, source.Confidence = "fresh", "proven"
	case state == "degraded" && source.ObservedAt != nil:
		source.Freshness, source.Confidence = "fresh", "partial"
	}
	return source
}

func projectManagerHealth(raw ipc.Health, now time.Time) []healthComponent {
	byKey := make(map[string]ipc.HealthComponent, len(managerHealthKeys))
	for _, component := range raw.Components {
		if !slices.Contains(managerHealthKeys, component.Key) {
			continue
		}
		if _, duplicate := byKey[component.Key]; duplicate {
			byKey[component.Key] = ipc.HealthComponent{Key: component.Key, State: "unknown", Reason: "COMPONENT_EVIDENCE_AMBIGUOUS"}
			continue
		}
		byKey[component.Key] = component
	}
	components := make([]healthComponent, 0, len(managerHealthKeys))
	for _, key := range managerHealthKeys {
		report, found := byKey[key]
		if !found {
			continue
		}
		state := normalizeHealthState(report.State)
		fallbackReason := "COMPONENT_REASON_UNKNOWN"
		if state == "ready" {
			fallbackReason = "COMPONENT_READY"
		}
		reason := safeHealthCode(report.Reason, fallbackReason)
		authority := safeHealthCode(report.Authority, "ovs-mgrd-inspection")
		out := healthComponent{Key: key, State: state, Reason: reason,
			Source: runtimeSource("ovs-mgrd", authority, state, report.ObservedAt, now)}
		if report.Active != nil && report.Capacity != nil && *report.Capacity >= 0 &&
			*report.Active >= 0 && *report.Active <= *report.Capacity {
			out.Active, out.Capacity = report.Active, report.Capacity
		}
		components = append(components, out)
	}
	return components
}

func (h *runtimeHealth) assembleHealth(ctx context.Context, raw ipc.Health) healthReadResource {
	now := time.Now().UTC()
	components := projectManagerHealth(raw, now)
	webState, webReason := "unknown", "WEB_STORAGE_EVIDENCE_UNAVAILABLE"
	var probedAt *time.Time
	if h.storage != nil {
		storage := h.storage(ctx)
		webState = normalizeHealthState(storage.State)
		webReason = safeHealthCode(storage.Code, "WEB_STORAGE_"+strings.ToUpper(webState))
		if webState == "ready" && (!storage.Writable || !storage.Readable) {
			webState, webReason = "degraded", "WEB_STORAGE_ACCESS_LIMITED"
		}
		completed := time.Now().UTC()
		probedAt = &completed
	}
	finished := time.Now().UTC()
	components = append([]healthComponent{
		{Key: "webd", State: "ready", Reason: "WEB_REQUEST_SERVED", Source: runtimeSource("ovs-webd", "webd-request", "ready", &now, finished)},
		{Key: "web-db", State: webState, Reason: webReason, Source: runtimeSource("ovs-webd", "web-sqlite-probe", webState, probedAt, finished)},
	}, components...)
	state := normalizeHealthState(raw.State)
	if state == "ready" && webState != "ready" {
		state = "degraded"
	}
	return healthReadResource{
		runtimeResource: runtimeReadResource(runtimeHealthID, "health", state,
			runtimeSource("ovs-webd", "management-plane-health", state, &finished, finished)),
		Components: components, AuthenticationReady: raw.AuthenticationReady && webState == "ready",
		ConfigurationReady: raw.ConfigurationReady && webState == "ready",
	}
}

func (a *Authentication) runtimeClaims(ctx context.Context, subject publicapi.Subject, needCapabilities bool) (authn.Claims, error) {
	claims, err := a.manager.CheckAuth(ctx, subject.Credential, authn.Check{Capability: "state.read"})
	if err != nil {
		return authn.Claims{}, authError(err)
	}
	if claims.PrincipalID != subject.ID || claims.Revision != subject.PermissionRevision {
		return authn.Claims{}, apitypes.Fail(403, "AUTHORIZATION_CHANGED")
	}
	if needCapabilities && !slices.Contains(claims.Capabilities, "capabilities.read") {
		return authn.Claims{}, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	return claims, nil
}

func (a *Authentication) readRuntimeHealth(ctx context.Context, subject publicapi.Subject, query publicapi.Query) (publicapi.Response, error) {
	claims, err := a.runtimeClaims(ctx, subject, query.Operation.ID != "readHealth")
	if err != nil {
		return publicapi.Response{}, err
	}
	if a.runtimeHealth == nil {
		return publicapi.Response{}, apitypes.Fail(503, "DOMAIN_SERVICE_UNAVAILABLE")
	}
	if query.Operation.ID == "readCapability" && !isRuntimeCapability(query.Path["capability_id"]) {
		return publicapi.Response{}, apitypes.Fail(404, "NOT_FOUND")
	}
	report, err := a.runtimeHealth.manager.Inspect(ctx, subject.Credential)
	if err != nil {
		return publicapi.Response{}, authError(err)
	}
	if report.Scope != "runtime-inspection" {
		return publicapi.Response{}, apitypes.Fail(503, "HEALTH_EVIDENCE_UNAVAILABLE")
	}
	health := a.runtimeHealth.assembleHealth(ctx, report)
	if ctx.Err() != nil {
		return publicapi.Response{}, apitypes.Fail(503, "HEALTH_EVIDENCE_UNAVAILABLE")
	}
	if query.Operation.ID == "readHealth" {
		return runtimeHealthResponse(health)
	}
	return a.runtimeHealth.readCapabilities(query, claims, health)
}

func runtimeHealthResponse(value any) (publicapi.Response, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return publicapi.Response{}, apitypes.Fail(503, "HEALTH_EVIDENCE_UNAVAILABLE")
	}
	return publicapi.Response{Status: 200, Body: body}, nil
}
