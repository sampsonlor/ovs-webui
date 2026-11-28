package inventory

import (
	"context"
	"math/bits"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

type AccelerationField[T any] struct {
	State  string `json:"state"`
	Value  *T     `json:"value,omitempty"`
	Reason string `json:"reason_code,omitempty"`
}

type DPDKObservation struct {
	InitMode            AccelerationField[string]   `json:"init_mode"`
	SocketMemoryMiB     AccelerationField[[]uint32] `json:"socket_memory_mib"`
	ConfiguredPMDCPUs   AccelerationField[uint16]   `json:"configured_pmd_cpus"`
	ReportedInitialized AccelerationField[bool]     `json:"reported_initialized"`
	ReportedVersion     AccelerationField[string]   `json:"reported_version"`
	PMDThreads          AccelerationField[uint32]   `json:"pmd_threads"`
	RXQueues            AccelerationField[uint32]   `json:"rx_queues"`
	NUMAAffinity        AccelerationField[string]   `json:"numa_affinity"`
}

type OffloadObservation struct {
	Configured       AccelerationField[bool]   `json:"configured"`
	TCPolicy         AccelerationField[string] `json:"tc_policy"`
	HardwareSupport  AccelerationField[bool]   `json:"hardware_support"`
	HardwareFlows    AccelerationField[uint32] `json:"hardware_flows"`
	SoftwareFallback AccelerationField[bool]   `json:"software_fallback"`
}

type AccelerationObservation struct {
	InstanceGeneration    string             `json:"instance_generation"`
	ObservedAt            time.Time          `json:"observed_at"`
	ConfigurationSource   string             `json:"configuration_source"`
	ReportedRuntimeSource string             `json:"reported_runtime_source"`
	DPDK                  DPDKObservation    `json:"dpdk"`
	Offload               OffloadObservation `json:"offload"`
}

// ObserveAcceleration projects the exact root from one fresh confirmed OVSDB
// publication. OVSDB-reported runtime fields are DB observations and cannot
// qualify current ovs-vswitchd liveness, NIC hardware or offload enforcement.
func (s *Service) ObserveAcceleration(ctx context.Context, claims authn.Claims) (AccelerationObservation, error) {
	var out AccelerationObservation
	if !slices.Contains(claims.Capabilities, "inventory.read") || !slices.Contains(claims.Capabilities, "state.read") {
		return out, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s == nil {
		return out, apitypes.Fail(503, "ACCELERATION_OVSDB_UNAVAILABLE")
	}
	if !s.mu.TryRLock() {
		return out, apitypes.Fail(503, "ACCELERATION_CACHE_BUSY")
	}
	defer s.mu.RUnlock()
	v := s.current
	if v == nil || s.failure != "" || v.decision.State != "confirmed" ||
		!apitypes.ManagementID(v.decision.Generation) {
		return out, apitypes.Fail(503, "ACCELERATION_OVSDB_UNAVAILABLE")
	}
	evidence := v.observation.Evidence
	age := s.now().Sub(evidence.ObservedAt)
	if evidence.ObservedAt.IsZero() || age < 0 || age > FreshFor {
		return out, apitypes.Fail(503, "ACCELERATION_OVSDB_STALE")
	}
	roots := v.observation.Rows["Open_vSwitch"]
	root := roots[evidence.Root]
	if len(roots) != 1 || !apitypes.UUID(evidence.Root) || root.UUID != evidence.Root {
		return out, apitypes.Fail(503, "ACCELERATION_ROOT_UNPROVEN")
	}
	options, knownOptions := root.Values["other_config"].(map[string]any)
	readConfig := slices.Contains(claims.Capabilities, "configuration.read")
	config := accelerationOptions{values: options, known: knownOptions && options != nil, allowed: readConfig}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out = AccelerationObservation{
		InstanceGeneration:    v.decision.Generation,
		ObservedAt:            evidence.ObservedAt.UTC(),
		ConfigurationSource:   "ovsdb-root-configuration",
		ReportedRuntimeSource: "ovsdb-reported-runtime",
		DPDK: DPDKObservation{
			InitMode:            accelerationOption(config, "dpdk-init", oneAccelerationValue("true", "false", "try")),
			SocketMemoryMiB:     accelerationOption(config, "dpdk-socket-mem", accelerationSocketMemory),
			ConfiguredPMDCPUs:   accelerationOption(config, "pmd-cpu-mask", accelerationMaskCount),
			ReportedInitialized: accelerationInitialized(root),
			ReportedVersion:     accelerationVersion(root),
			PMDThreads:          accelerationUnknown[uint32]("DPDK_PMD_RUNTIME_UNOBSERVED"),
			RXQueues:            accelerationUnknown[uint32]("DPDK_RXQ_RUNTIME_UNOBSERVED"),
			NUMAAffinity:        accelerationUnknown[string]("DPDK_NUMA_AFFINITY_UNOBSERVED"),
		},
		Offload: OffloadObservation{
			Configured:       accelerationOption(config, "hw-offload", accelerationBool),
			TCPolicy:         accelerationOption(config, "tc-policy", oneAccelerationValue("none", "skip_sw", "skip_hw")),
			HardwareSupport:  accelerationUnknown[bool]("HARDWARE_OFFLOAD_SUPPORT_UNOBSERVED"),
			HardwareFlows:    accelerationUnknown[uint32]("HARDWARE_OFFLOAD_RUNTIME_UNOBSERVED"),
			SoftwareFallback: accelerationUnknown[bool]("OFFLOAD_FALLBACK_RUNTIME_UNOBSERVED"),
		},
	}
	if err := ctx.Err(); err != nil {
		return AccelerationObservation{}, err
	}
	return out, nil
}

func accelerationUnknown[T any](code string) AccelerationField[T] {
	return AccelerationField[T]{State: "unknown", Reason: code}
}

type accelerationOptions struct {
	values         map[string]any
	known, allowed bool
}

func accelerationOption[T any](o accelerationOptions, key string, parse func(string) (T, bool)) AccelerationField[T] {
	if !o.allowed {
		return AccelerationField[T]{State: "withheld", Reason: "CONFIGURATION_WITHHELD"}
	}
	if !o.known {
		return accelerationUnknown[T]("OVSDB_CONFIGURATION_UNOBSERVED")
	}
	raw, present := o.values[key]
	if !present {
		return AccelerationField[T]{State: "unset", Reason: "OVSDB_OPTION_UNSET"}
	}
	value, ok := raw.(string)
	if !ok {
		return accelerationUnknown[T]("OVSDB_OPTION_UNRECOGNIZED")
	}
	parsed, valid := parse(value)
	if !valid {
		return accelerationUnknown[T]("OVSDB_OPTION_UNRECOGNIZED")
	}
	return AccelerationField[T]{State: "configured", Value: &parsed}
}

func oneAccelerationValue(allowed ...string) func(string) (string, bool) {
	return func(value string) (string, bool) {
		return value, slices.Contains(allowed, value)
	}
}

func accelerationBool(value string) (bool, bool) {
	return value == "true", value == "true" || value == "false"
}

func accelerationInitialized(root Row) AccelerationField[bool] {
	if value, ok := one(root.Values["dpdk_initialized"]).(bool); ok {
		return AccelerationField[bool]{State: "observed", Value: &value}
	}
	return accelerationUnknown[bool]("DPDK_INITIALIZATION_UNOBSERVED")
}

var accelerationVersionPattern = regexp.MustCompile(`^(?:DPDK )?([0-9]{1,4}\.[0-9]{1,3}(?:\.[0-9]{1,3})?(?:-rc[0-9]{1,2})?)$`)

func accelerationVersion(root Row) AccelerationField[string] {
	if raw := textValue(root.Values["dpdk_version"]); len(raw) <= 32 {
		if match := accelerationVersionPattern.FindStringSubmatch(raw); len(match) == 2 {
			return AccelerationField[string]{State: "observed", Value: &match[1]}
		}
	}
	return accelerationUnknown[string]("DPDK_VERSION_UNOBSERVED")
}

func accelerationSocketMemory(value string) ([]uint32, bool) {
	if len(value) < 1 || len(value) > 128 {
		return nil, false
	}
	parts := strings.Split(value, ",")
	if len(parts) > 16 {
		return nil, false
	}
	out := make([]uint32, 0, len(parts))
	for _, part := range parts {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool { return r < '0' || r > '9' }) {
			return nil, false
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || n > 1<<20 {
			return nil, false
		}
		out = append(out, uint32(n))
	}
	return out, true
}

func accelerationMaskCount(value string) (uint16, bool) {
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(value) < 1 || len(value) > 256 {
		return 0, false
	}
	var count uint16
	for _, c := range []byte(value) {
		switch {
		case '0' <= c && c <= '9':
			c -= '0'
		case 'a' <= c && c <= 'f':
			c -= 'a' - 10
		case 'A' <= c && c <= 'F':
			c -= 'A' - 10
		default:
			return 0, false
		}
		count += uint16(bits.OnesCount8(c))
	}
	return count, true
}
