package inventory

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

const accelerationRootID = "420b0a46-3d3c-439a-a179-000000000001"
const accelerationGeneration = "420b0a46-3d3c-439a-a179-000000000002"

func accelerationFixture(values map[string]any) (*Service, authn.Claims) {
	s := New(nil)
	now := time.Now()
	s.now = func() time.Time { return now }
	s.failure = ""
	s.current = &view{
		observation: Observation{
			Rows:     Rows{"Open_vSwitch": {accelerationRootID: {UUID: accelerationRootID, Values: values}}},
			Evidence: Evidence{Root: accelerationRootID, ObservedAt: now},
		},
		decision: Decision{State: "confirmed", Generation: accelerationGeneration},
	}
	return s, authn.Claims{Capabilities: []string{"inventory.read", "state.read", "configuration.read"}}
}

func TestAccelerationPreservesConfigurationAndIndependentDatabaseRuntime(t *testing.T) {
	const secret = "ovsg_synthetic-secret"
	values := map[string]any{
		"dpdk_initialized": false,
		"dpdk_version":     []any{"DPDK 23.11.3"},
		"other_config": map[string]any{
			"dpdk-init": "try", "dpdk-socket-mem": "2048,0,64", "pmd-cpu-mask": "0x35",
			"hw-offload": "true", "tc-policy": "skip_sw", "dpdk-extra": secret, "private": secret,
		},
	}
	s, claims := accelerationFixture(values)
	out, err := s.ObserveAcceleration(t.Context(), claims)
	if err != nil {
		t.Fatal(err)
	}
	if out.InstanceGeneration != accelerationGeneration || out.ObservedAt.IsZero() ||
		out.ReportedRuntimeSource != "ovsdb-reported-runtime" || out.ConfigurationSource != "ovsdb-root-configuration" {
		t.Fatalf("missing exact observed source: %+v", out)
	}
	if f := out.DPDK.ReportedInitialized; f.State != "observed" || f.Value == nil || *f.Value != false {
		t.Fatalf("explicit false lost from observation: %+v", f)
	}
	if f := out.DPDK.ReportedVersion; f.State != "observed" || f.Value == nil || *f.Value != "23.11.3" {
		t.Fatalf("missing exact reported runtime version: %+v", f)
	}
	if f := out.DPDK.InitMode; f.State != "configured" || f.Value == nil || *f.Value != "try" {
		t.Fatalf("DPDK settings lost: %+v", f)
	}
	if f := out.DPDK.SocketMemoryMiB; f.State != "configured" || f.Value == nil || !slices.Equal(*f.Value, []uint32{2048, 0, 64}) {
		t.Fatalf("bad bounded socket memory projection: %+v", f)
	}
	if f := out.DPDK.ConfiguredPMDCPUs; f.State != "configured" || f.Value == nil || *f.Value != 4 {
		t.Fatalf("bad PMD CPU bit count: %+v", f)
	}
	if out.DPDK.PMDThreads.State != "unknown" || out.DPDK.RXQueues.Value != nil || out.DPDK.NUMAAffinity.Value != nil ||
		out.Offload.Configured.Value == nil || !*out.Offload.Configured.Value || out.Offload.HardwareFlows.Value != nil ||
		out.Offload.HardwareSupport.Value != nil || out.Offload.SoftwareFallback.Value != nil {
		t.Fatalf("configuration was treated as runtime or hardware proof: %+v", out)
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 4096 || strings.Contains(string(raw), secret) ||
		strings.Contains(string(raw), "dpdk-extra") || strings.Contains(string(raw), accelerationRootID) {
		t.Fatalf("unexpected unstructured data: %s, %v", raw, err)
	}
	claims.Capabilities = []string{"inventory.read", "state.read"}
	out, err = s.ObserveAcceleration(t.Context(), claims)
	if err != nil || out.DPDK.InitMode.State != "withheld" || out.DPDK.InitMode.Value != nil ||
		out.Offload.Configured.State != "withheld" || out.Offload.Configured.Value != nil ||
		out.DPDK.ReportedInitialized.State != "observed" {
		t.Fatalf("configuration withheld or independent runtime lost: %+v, %v", out, err)
	}
}

func TestAccelerationDistinguishesMissingUnsetMalformedAndStale(t *testing.T) {
	values := map[string]any{
		"dpdk_initialized": []any{}, "dpdk_version": []any{"DPDK ovsg_synthetic-secret"},
		"other_config": map[string]any{"dpdk-socket-mem": "1, +2", "pmd-cpu-mask": "0xg", "hw-offload": "maybe"},
	}
	s, claims := accelerationFixture(values)
	out, err := s.ObserveAcceleration(t.Context(), claims)
	if err != nil || out.DPDK.ReportedInitialized.State != "unknown" || out.DPDK.ReportedInitialized.Value != nil ||
		out.DPDK.ReportedVersion.Value != nil || out.DPDK.InitMode.State != "unset" || out.Offload.Configured.State != "unknown" ||
		out.DPDK.SocketMemoryMiB.Value != nil || out.DPDK.ConfiguredPMDCPUs.Value != nil {
		t.Fatalf("missing, malformed and unset were confused: %+v, %v", out, err)
	}
	delete(values, "other_config")
	out, err = s.ObserveAcceleration(t.Context(), claims)
	if err != nil || out.DPDK.InitMode.Reason != "OVSDB_CONFIGURATION_UNOBSERVED" {
		t.Fatalf("absent map was mistaken for known empty map: %+v, %v", out, err)
	}
	require := func(err error, code string) {
		t.Helper()
		var p *apitypes.Problem
		if !errors.As(err, &p) || p.Code != code {
			t.Fatalf("want %s, got %v", code, err)
		}
	}
	_, err = s.ObserveAcceleration(t.Context(), authn.Claims{Capabilities: []string{"state.read"}})
	require(err, "CAPABILITY_DENIED")
	roots := s.current.observation.Rows["Open_vSwitch"]
	roots[accelerationGeneration] = Row{UUID: accelerationGeneration}
	_, err = s.ObserveAcceleration(t.Context(), claims)
	require(err, "ACCELERATION_ROOT_UNPROVEN")
	delete(roots, accelerationGeneration)
	s.mu.Lock()
	_, err = s.ObserveAcceleration(t.Context(), claims)
	s.mu.Unlock()
	require(err, "ACCELERATION_CACHE_BUSY")
	s.now = func() time.Time { return s.current.observation.Evidence.ObservedAt.Add(FreshFor + time.Nanosecond) }
	_, err = s.ObserveAcceleration(t.Context(), claims)
	require(err, "ACCELERATION_OVSDB_STALE")
}
