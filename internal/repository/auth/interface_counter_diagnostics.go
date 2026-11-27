package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

const interfaceCounterOperation = "runInterfaceCounterDiagnostic"

type counterDiagnosticInventory interface {
	CheckInterfaceCounterTarget(context.Context, diagnostics.InterfaceCounterRequest, authn.Claims) error
	ObserveInterfaceCounterDiagnostic(context.Context, diagnostics.InterfaceCounterRequest, authn.Claims) (inventory.InterfaceCounterDiagnostic, error)
}

func (r *Repository) runInterfaceCounterDiagnostic(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	op, body, err := r.candidateCommand(input, interfaceCounterOperation)
	if err != nil {
		return apitypes.Result{}, err
	}
	source, ok := r.inventory.(counterDiagnosticInventory)
	if !ok || r.diagnosticsContext == nil {
		return apitypes.Result{}, apitypes.Fail(503, "DIAGNOSTICS_EXECUTOR_UNAVAILABLE")
	}
	var wire struct {
		Target        apitypes.Ref `json:"target_ref"`
		SampleSeconds int          `json:"sample_seconds"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return apitypes.Result{}, apitypes.Fail(422, "INVALID_DIAGNOSTIC_PARAMETERS")
	}
	req := diagnostics.InterfaceCounterRequest{Target: wire.Target, SampleSeconds: wire.SampleSeconds}
	return r.startDiagnosticObservation(ctx, credential, input, op, body, boundedObservation{
		target: req.Target, budget: time.Duration(req.SampleSeconds) * time.Second,
		capability: diagnostics.InterfaceCounterCapability, authorize: req.Authorize,
		admit: func(ctx context.Context, claims authn.Claims) error {
			return source.CheckInterfaceCounterTarget(ctx, req, claims)
		},
		collect: func(ctx context.Context, claims authn.Claims) (diagnosticObservation, error) {
			result, err := source.ObserveInterfaceCounterDiagnostic(ctx, req, claims)
			return diagnosticObservation{state: result.State, document: func(authn.Claims) any { return result }}, err
		},
	})
}

func (r *Repository) readInterfaceCounterResult(ctx context.Context, tx *sql.Tx, claims authn.Claims, id string) (any, error) {
	job, doc, err := r.readDiagnosticObservation(ctx, tx, claims, id, interfaceCounterOperation)
	if err != nil {
		return nil, err
	}
	var out inventory.InterfaceCounterDiagnostic
	if json.Unmarshal(doc, &out) != nil || out.Template != diagnostics.InterfaceCounterTemplate || job.Resource == nil || out.Target != *job.Resource {
		return nil, apitypes.Fail(503, "DIAGNOSTIC_RESULT_UNAVAILABLE")
	}
	return out, nil
}
