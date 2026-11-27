package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
)

type capabilityDefinition struct {
	id, key, label string
	component      string
	prerequisites  []string
	permissions    []string
	unavailable    string
	targetWrite    bool
}

// Keep the reviewed Capability subjects and expose the current shared Linux
// device and statistics readers as additional runtime observations.
var runtimeCapabilities = []capabilityDefinition{
	{id: "31fa3492-67e6-4a39-bfce-000000000002", key: "bridge", label: "Bridge inventory",
		component: "ovsdb", prerequisites: []string{"ovsdb"}, permissions: []string{"inventory.read", "state.read"}},
	{id: "31fa3492-67e6-4a39-bfce-000000000003", key: "vlan", label: "Port VLAN configuration",
		component: "ovsdb", prerequisites: []string{"ovsdb", "safety-watchdog", "target-authority", "candidate-validation"},
		permissions: []string{"ovs.port.vlan.write", "workspace.write", "configuration.validate", "configuration.apply"}, targetWrite: true},
	{id: "31fa3492-67e6-4a39-bfce-000000000004", key: "bond", label: "Bond and LACP configuration",
		component: "ovsdb", prerequisites: []string{"ovsdb", "safety-watchdog", "target-authority", "candidate-validation"},
		permissions: []string{"ovs.port.bond.write", "workspace.write", "configuration.validate", "configuration.apply"}, targetWrite: true},
	{id: "31fa3492-67e6-4a39-bfce-000000000005", key: "protected", label: "Native protected Port configuration",
		component: "ovsdb", prerequisites: []string{"ovsdb", "safety-watchdog", "target-authority", "candidate-validation"},
		permissions: []string{"ovs.port.isolation.write", "workspace.write", "configuration.validate", "configuration.apply"}, targetWrite: true},
	{id: "31fa3492-67e6-4a39-bfce-000000000006", key: "openflow", label: "OpenFlow observation",
		prerequisites: []string{"openflow-provider"}, permissions: []string{"inventory.read", "state.read"},
		unavailable: "OPENFLOW_PROVIDER_UNAVAILABLE"},
	{id: "31fa3492-67e6-4a39-bfce-000000000007", key: "dpdk", label: "DPDK observation",
		prerequisites: []string{"dpdk-provider"}, permissions: []string{"inventory.read", "state.read"},
		unavailable: "DPDK_PROVIDER_UNAVAILABLE"},
	{id: "31fa3492-67e6-4a39-bfce-000000000008", key: "offload", label: "Hardware offload observation",
		prerequisites: []string{"offload-provider", "hardware-runtime-evidence"}, permissions: []string{"inventory.read", "state.read"},
		unavailable: "OFFLOAD_PROVIDER_UNAVAILABLE"},
	{id: "31fa3492-67e6-4a39-bfce-000000000009", key: "diagnostics", label: "Bounded diagnostics",
		component: "diagnostics-executor", prerequisites: []string{"ovsdb", "diagnostics-executor"},
		permissions: []string{"diagnostics.read", "inventory.read"}},
	{id: "31fa3492-67e6-4a39-bfce-00000000000a", key: "lifecycle", label: "OVS lifecycle",
		prerequisites: []string{"lifecycle-executor", "backup", "safety-watchdog"}, permissions: []string{"ovs.lifecycle.manage"},
		unavailable: "LIFECYCLE_EXECUTOR_UNAVAILABLE", targetWrite: true},
	{id: "31fa3492-67e6-4a39-bfce-00000000000b", key: "linux-devices", label: "Linux Interface observation",
		component: "cache-interface-devices", prerequisites: []string{"ovsdb", "cache-interface-devices"}, permissions: []string{"inventory.read", "state.read"}},
	{id: "31fa3492-67e6-4a39-bfce-00000000000c", key: "statistics", label: "Interface statistics",
		component: "cache-statistics", prerequisites: []string{"ovsdb", "cache-statistics"}, permissions: []string{"inventory.read", "state.read"}},
}

type capabilityFacts struct {
	Key                  string   `json:"key"`
	Label                string   `json:"label"`
	Availability         string   `json:"availability"`
	Reason               string   `json:"reason"`
	Authority            string   `json:"authority"`
	Prerequisites        []string `json:"prerequisites"`
	RequiredCapabilities []string `json:"required_capabilities"`
	GrantState           string   `json:"grant_state"`
	Recovery             string   `json:"recovery"`
}

type capabilityReadResource struct {
	runtimeResource
	Capability capabilityFacts `json:"capability_evidence"`
}

func isRuntimeCapability(id string) bool {
	return slices.ContainsFunc(runtimeCapabilities, func(d capabilityDefinition) bool { return d.id == id })
}

func capabilityPrerequisite(components map[string]healthComponent, key string) healthComponent {
	component, found := components[key]
	if !found {
		return healthComponent{State: "unknown", Reason: "PREREQUISITE_EVIDENCE_UNAVAILABLE",
			Source: healthSource{ProviderID: "ovs-webd", Authority: "capability-registry", Freshness: "unknown", Confidence: "unknown"}}
	}
	if component.State == "ready" && component.Source.Freshness != "fresh" {
		component.State, component.Reason = "unknown", "PREREQUISITE_EVIDENCE_UNAVAILABLE"
	}
	return component
}

func projectCapability(d capabilityDefinition, claims authn.Claims, health healthReadResource, components map[string]healthComponent) capabilityReadResource {
	facts := capabilityFacts{Key: d.key, Label: d.label, Prerequisites: d.prerequisites,
		RequiredCapabilities: d.permissions, GrantState: "granted", Recovery: "not-required"}
	for _, permission := range d.permissions {
		if !slices.Contains(claims.Capabilities, permission) {
			facts.GrantState = "denied"
			break
		}
	}
	var evidence healthComponent
	if d.unavailable != "" {
		evidence = healthComponent{State: "unavailable", Reason: d.unavailable,
			Source: healthSource{ProviderID: "ovs-webd", Authority: "capability-registry", Freshness: "unavailable", Confidence: "unknown"}}
		if d.targetWrite {
			facts.Recovery = "unavailable"
		}
	} else {
		evidence = capabilityPrerequisite(components, "ovsdb")
		if d.component != "ovsdb" && evidence.State == "ready" {
			evidence = capabilityPrerequisite(components, d.component)
		}
		if d.targetWrite {
			watchdog := capabilityPrerequisite(components, "safety-watchdog")
			facts.Recovery = "unknown"
			switch {
			case watchdog.State != "ready":
				facts.Recovery = watchdog.State
				if facts.Recovery == "disabled" || facts.Recovery == "degraded" || facts.Recovery == "stale" {
					facts.Recovery = "unavailable"
				}
				if evidence.State == "ready" {
					evidence = watchdog
				}
			case !health.ConfigurationReady:
				facts.Recovery = "unavailable"
				if evidence.State == "ready" {
					evidence.State, evidence.Reason = "unavailable", "SAFE_APPLY_UNAVAILABLE"
				}
			case evidence.State == "ready":
				// Process readiness and current grants do not prove a particular
				// object's native authority, validation, or restore path.
				evidence.State, evidence.Reason = "unknown", "TARGET_AUTHORITY_REQUIRED"
			}
		}
	}
	facts.Availability, facts.Reason, facts.Authority = evidence.State, evidence.Reason, evidence.Source.Authority
	return capabilityReadResource{runtimeReadResource(d.id, "capability", evidence.State, evidence.Source), facts}
}

type capabilityCursor struct {
	Principal, Credential, Permission, Snapshot, Filter string
	Offset, Limit                                       int
	Expires                                             int64
}

type capabilityPage struct {
	SnapshotID         string                   `json:"snapshot_id"`
	InstanceGeneration *string                  `json:"instance_generation"`
	Items              []capabilityReadResource `json:"items"`
	NextCursor         *string                  `json:"next_cursor"`
	Truncated          bool                     `json:"truncated"`
}

func (h *runtimeHealth) readCapabilities(q publicapi.Query, claims authn.Claims, health healthReadResource) (publicapi.Response, error) {
	components := make(map[string]healthComponent, len(health.Components))
	for _, component := range health.Components {
		components[component.Key] = component
	}
	if q.Operation.ID == "readCapability" {
		for _, d := range runtimeCapabilities {
			if d.id == q.Path["capability_id"] {
				return runtimeHealthResponse(projectCapability(d, claims, health, components))
			}
		}
		return publicapi.Response{}, apitypes.Fail(404, "NOT_FOUND")
	}
	limit := 100
	if value := q.Values.Get("limit"); value != "" {
		limit, _ = strconv.Atoi(value)
		if limit < 1 || limit > 500 {
			return publicapi.Response{}, apitypes.Fail(422, "INVALID_LIMIT")
		}
	}
	filter := strings.ToLower(q.Values.Get("filter"))
	if len(filter) > 1024 {
		return publicapi.Response{}, apitypes.Fail(422, "INVALID_FILTER")
	}
	items := make([]capabilityReadResource, 0, len(runtimeCapabilities))
	for _, d := range runtimeCapabilities {
		if strings.Contains(d.key, filter) || strings.Contains(strings.ToLower(d.label), filter) {
			items = append(items, projectCapability(d, claims, health, components))
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return publicapi.Response{}, apitypes.Fail(503, "HEALTH_EVIDENCE_UNAVAILABLE")
	}
	snapshot := capabilitySnapshotID(encoded)
	now := time.Now()
	cur := capabilityCursor{
		Principal: claims.PrincipalID, Credential: claims.CredentialID, Permission: claims.Revision,
		Snapshot: snapshot, Filter: filter, Limit: limit, Expires: now.Add(30 * time.Second).UnixMilli(),
	}
	pageKey := []byte("capability-page-v1")
	if token := q.Values.Get("cursor"); token != "" {
		sealed, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(token) > 4096 {
			return publicapi.Response{}, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		plain, err := authn.Unseal(h.pageKey[:], sealed, pageKey)
		var old capabilityCursor
		if err != nil || json.Unmarshal(plain, &old) != nil || old.Expires <= now.UnixMilli() || old.Offset <= 0 || old.Offset >= len(items) {
			return publicapi.Response{}, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		cur.Offset, cur.Expires = old.Offset, old.Expires
		if old != cur {
			return publicapi.Response{}, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
	}
	end := min(cur.Offset+cur.Limit, len(items))
	page := capabilityPage{SnapshotID: snapshot, Items: items[cur.Offset:end]}
	if end < len(items) {
		cur.Offset = end
		payload, _ := json.Marshal(cur)
		sealed, err := authn.Seal(h.pageKey[:], payload, pageKey)
		if err != nil {
			return publicapi.Response{}, apitypes.Fail(503, "HEALTH_EVIDENCE_UNAVAILABLE")
		}
		token := base64.RawURLEncoding.EncodeToString(sealed)
		page.NextCursor = &token
	}
	return runtimeHealthResponse(page)
}

func capabilitySnapshotID(data []byte) string {
	digest := sha256.Sum256(data)
	digest[6] = digest[6]&15 | 64
	digest[8] = digest[8]&63 | 128
	id := hex.EncodeToString(digest[:16])
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}
