package ovsdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

// Independent root process configuration. No public request can select a path,
// profile, executable, peer, manifest or native control method.
type mirrorPreparationFailure struct{ cause error }

func (e *mirrorPreparationFailure) Error() string {
	return "MIRROR_RUNTIME_UNQUALIFIED: " + e.cause.Error()
}
func (e *mirrorPreparationFailure) Unwrap() error {
	return apitypes.Fail(409, "MIRROR_RUNTIME_UNQUALIFIED")
}

type MirrorExecutionOptions struct {
	ControlSocket  string `json:"control_socket"`
	Executable     string `json:"executable"`
	Manifest       string `json:"manifest"`
	ManifestDigest string `json:"manifest_sha256"`
	PeerUID        uint32 `json:"peer_uid"`
}

func (p *Provider) ConfigureMirrorExecution(o MirrorExecutionOptions) error {
	if p == nil || p.mirror != nil || !filepath.IsAbs(o.ControlSocket) || len(o.ControlSocket) > 100 || !filepath.IsAbs(o.Executable) || !filepath.IsAbs(o.Manifest) || strings.ContainsAny(o.ControlSocket+o.Executable+o.Manifest, "\x00\r\n") || len(o.ManifestDigest) != 64 || strings.Trim(o.ManifestDigest, "0123456789abcdef") != "" || o.PeerUID == ^uint32(0) {
		return errors.New("INVALID_MIRROR_RUNTIME_CONFIGURATION")
	}
	copy := o
	p.mirror = &copy
	return nil
}

func isMirrorCandidate(c candidate.Candidate) bool {
	return len(c.Intents) == 1 && candidate.IsMirrorOperation(c.Intents[0].Operation)
}

func (e *Executor) prepareMirrorRuntime(ctx context.Context, p execution.Plan, original *nativePlan, reviewed string) (execution.Plan, error) {
	if !isMirrorCandidate(p.Envelope.Candidate) {
		return p, nil
	}
	o := e.provider.mirror
	if original != nil {
		o = original.MirrorBuild
	}
	var n nativePlan
	if o == nil || json.Unmarshal(p.Native, &n) != nil {
		return execution.Plan{}, apitypes.Fail(409, "MIRROR_RUNTIME_UNQUALIFIED")
	}
	proof, err := e.provider.mirrorRuntime(ctx, o, n.Evidence, true)
	if err != nil {
		return execution.Plan{}, &mirrorPreparationFailure{cause: err}
	}
	if proof == "" || original == nil && (reviewed == "" || !strings.HasPrefix(proof, reviewed+"/")) || original != nil && proof != original.MirrorAuthority {
		return execution.Plan{}, apitypes.Fail(409, "MIRROR_RUNTIME_UNQUALIFIED")
	}
	return pinMirrorRuntime(p, *o, proof)
}

// Native predicates include exact signed 64-bit limits. Preserve JSON numbers
// while adding the runtime pin; a float64 round trip changes the counter guard
// to an out-of-range integer and causes the actual OVS transaction to reject.
func pinMirrorRuntime(p execution.Plan, options MirrorExecutionOptions, proof string) (execution.Plan, error) {
	var n nativePlan
	decoder := json.NewDecoder(bytes.NewReader(p.Native))
	decoder.UseNumber()
	if decoder.Decode(&n) != nil {
		return execution.Plan{}, apitypes.Fail(503, "EXECUTION_PLAN_INVALID")
	}
	n.MirrorBuild, n.MirrorAuthority = &options, proof
	data, err := json.Marshal(n)
	if err != nil || len(data) > execution.MaxPlanBytes {
		return execution.Plan{}, apitypes.Fail(429, "EXECUTION_PLAN_BUDGET")
	}
	p.Native = data
	return p, nil
}

func (e *Executor) mirrorDispatchCheck(ctx context.Context, p execution.Plan, n nativePlan) error {
	if !isMirrorCandidate(p.Envelope.Candidate) {
		return nil
	}
	// Recovery uses only its durable admitted pin. New admission also requires
	// the current root configuration; removing it cannot admit another write.
	i := p.Envelope.Candidate.Intents[0]
	if i.Topology == nil || n.MirrorBuild == nil || n.MirrorAuthority == "" || !i.Topology.Compensating && (e.provider.mirror == nil || candidate.Digest(e.provider.mirror) != candidate.Digest(n.MirrorBuild)) {
		return errors.New("MIRROR_CURRENT_ADMISSION_REVOKED")
	}
	proof, err := e.provider.mirrorRuntime(ctx, n.MirrorBuild, n.Evidence, true)
	if err != nil {
		return err
	}
	if proof != n.MirrorAuthority {
		return errors.New("MIRROR_RUNTIME_IDENTITY_CHANGED")
	}
	return nil
}

// Closed internal diagnostics explain a refused attempt without publishing
// private source identities, row values, filenames or driver error text.
func (e *Executor) logMirrorDenial(ctx context.Context, scope string, err error) {
	if e.provider.options.Logger == nil {
		return
	}
	code := "MIRROR_RUNTIME_UNQUALIFIED"
	switch err.Error() {
	case "MIRROR_CURRENT_ADMISSION_REVOKED", "MIRROR_RUNTIME_IDENTITY_CHANGED", "MIRROR_RUNTIME_NOT_CONFIGURED", "MIRROR_CONTROL_OR_BUILD_UNPROVEN",
		"SPANNING_TREE_SOURCE_UNPROVEN", "SPANNING_TREE_SOURCE_EVIDENCE_UNPROVEN", "SPANNING_TREE_SOURCE_CONTROL_UNPROVEN", "SPANNING_TREE_SOURCE_EXECUTABLE_UNPROVEN",
		"SPANNING_TREE_SOURCE_DATABASE_UNAVAILABLE", "SPANNING_TREE_SOURCE_DATABASE_PEER_UNPROVEN", "SPANNING_TREE_SOURCE_SCHEMA_UNPROVEN", "SPANNING_TREE_SOURCE_ROOT_UNPROVEN",
		"SPANNING_TREE_SOURCE_FILE_UNPROVEN", "SPANNING_TREE_SOURCE_DIAG_UNPROVEN", "SPANNING_TREE_SOURCE_DIAG_INTERRUPTED", "SPANNING_TREE_SOURCE_CONNECTION_UNPROVEN", "SPANNING_TREE_SOURCE_PROCESS_FDS_UNPROVEN":
		code = err.Error()
	}
	e.provider.options.Logger.WarnContext(ctx, "mirror_runtime_denied", "code", code, "scope", scope)
}

func (e *Executor) mirrorAppliedProof(ctx context.Context, p execution.Plan, v inventory.ExecutionView) error {
	if !isMirrorCandidate(p.Envelope.Candidate) {
		return nil
	}
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil || n.MirrorBuild == nil || n.MirrorAuthority == "" {
		return errors.New("MIRROR_RUNTIME_UNQUALIFIED")
	}
	proof, err := e.provider.mirrorRuntime(ctx, n.MirrorBuild, v.Observation.Evidence, true)
	if err != nil || proof != n.MirrorAuthority {
		return errors.New("MIRROR_RUNTIME_UNQUALIFIED")
	}
	return nil
}
