package inventory

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/capture"
)

// ResolveCaptureBinding joins one root-approved management ID to the current
// OVS graph and independently sampled Linux association. allowed is supplied
// by trusted manager configuration and must remain immutable during use.
// This uses no new provider read; every caller, including a collector already
// running, must present the same authority again at each check.
func (s *Service) ResolveCaptureBinding(ctx context.Context, id string, allowed map[string]bool) (capture.Binding, error) {
	if err := ctx.Err(); err != nil {
		return capture.Binding{}, err
	}
	if !apitypes.ManagementID(id) || !allowed[id] {
		return capture.Binding{}, apitypes.Fail(403, "CAPTURE_SCOPE_DENIED")
	}
	if s == nil || !s.mu.TryRLock() {
		return capture.Binding{}, apitypes.Fail(503, "CAPTURE_EVIDENCE_UNAVAILABLE")
	}
	defer s.mu.RUnlock()

	v, now := s.current, s.now()
	if !deviceViewFresh(v, s.failure, now) || v.observation.Evidence.ObservedAt.IsZero() || s.devices.observer == nil {
		return capture.Binding{}, apitypes.Fail(503, "CAPTURE_EVIDENCE_UNAVAILABLE")
	}
	// The existing index excludes every candidate whose name or ifindex is
	// shared by another active Interface, without an extra topology scan.
	compareID := func(binding neighborBinding, target string) int {
		return strings.Compare(binding.managementID, target)
	}
	scope := v.neighborScope
	index, found := slices.BinarySearchFunc(scope.bindings, id, compareID)
	if !found || index+1 < len(scope.bindings) && scope.bindings[index+1].managementID == id {
		return capture.Binding{}, apitypes.Fail(409, "CAPTURE_INTERFACE_UNPROVEN")
	}
	if _, ambiguous := slices.BinarySearchFunc(scope.ambiguous, id, compareID); ambiguous {
		return capture.Binding{}, apitypes.Fail(409, "CAPTURE_INTERFACE_UNPROVEN")
	}
	target := scope.bindings[index]
	proof, eligible := deviceBinding(v, target.ovsUUID)
	if !eligible || proof != target.deviceProof || !apitypes.ManagementID(target.port.id) || !apitypes.ManagementID(target.bridge.id) ||
		v.observation.Rows["Interface"][target.ovsUUID].UUID != target.ovsUUID {
		return capture.Binding{}, apitypes.Fail(409, "CAPTURE_INTERFACE_UNPROVEN")
	}
	entry, ok := s.devices.entries[id]
	if !ok || entry.dirty || entry.proof != proof || entry.sample.Availability != "known" ||
		entry.sample.IfIndex != proof.request.IfIndex || !captureSampleFresh(now, entry.cachedAt) ||
		!captureSampleFresh(now, entry.sample.ObservedAt) {
		return capture.Binding{}, apitypes.Fail(409, "CAPTURE_INTERFACE_UNPROVEN")
	}
	witness := capture.Binding{
		InterfaceID: id, Generation: proof.generation, NativeUUID: proof.ovsUUID,
		IfIndex: proof.request.IfIndex, Name: proof.request.Name, KernelEpoch: entry.linkRevision,
	}
	if !witness.ValidFor(id) {
		return capture.Binding{}, apitypes.Fail(409, "CAPTURE_INTERFACE_UNPROVEN")
	}
	if err := ctx.Err(); err != nil {
		return capture.Binding{}, err
	}
	return witness, nil
}

func captureSampleFresh(now, observed time.Time) bool {
	age := now.Sub(observed)
	return !observed.IsZero() && age >= 0 && age <= FreshFor
}
