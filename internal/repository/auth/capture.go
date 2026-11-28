package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/capture"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

const (
	captureOperation = "createCapture"
	captureCap       = "diagnostics.disruptive"
	captureTTL       = 15 * time.Minute
	maxCaptureFiles  = 32
)

type captureAuthority struct {
	resolver capture.Resolver
	allowed  map[string]bool
}

func (r *Repository) WithCapture(resolver capture.Resolver, allowed map[string]bool) *Repository {
	r.capture = &captureAuthority{resolver: resolver, allowed: allowed}
	return r
}

type captureTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (r *Repository) readCaptureTargets(ctx context.Context, claims authn.Claims) (any, error) {
	if err := require(claims, "inventory.read", false, 0); err != nil {
		return nil, err
	}
	state := "disabled"
	targets := []captureTarget{}
	if r.capture == nil || r.capture.resolver == nil {
		state = "unavailable"
	} else if len(r.capture.allowed) > 0 {
		state = "enabled"
		ids := make([]string, 0, len(r.capture.allowed))
		for id := range r.capture.allowed {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			binding, err := r.capture.resolver(ctx, id)
			if err == nil && binding.ValidFor(id) {
				targets = append(targets, captureTarget{ID: id, Name: binding.Name})
			}
		}
	}
	return map[string]any{"targets": targets, "policy_state": state}, nil
}

func (r *Repository) prepareCapture(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	op, body, err := r.candidateCommand(input, captureOperation)
	if err != nil {
		return apitypes.Result{}, err
	}
	var req capture.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return apitypes.Result{}, apitypes.Fail(422, "INVALID_CAPTURE_PARAMETERS")
	}
	if err := req.Validate(); err != nil {
		return apitypes.Result{}, err
	}
	if r.capture == nil || r.capture.resolver == nil || len(r.capture.allowed) == 0 || r.diagnosticsContext == nil {
		return apitypes.Result{}, apitypes.Fail(503, "CAPTURE_PROVIDER_UNAVAILABLE")
	}
	out, id, err := r.admitCapture(ctx, credential, op, input, body, req)
	if err == nil && id != "" {
		go r.runCapture(r.diagnosticsContext, credential, op, id, req)
	}
	return out, err
}

func (r *Repository) admitCapture(ctx context.Context, credential string, op *apicontract.Operation, input authn.Command, body []byte, req capture.Request) (apitypes.Result, string, error) {
	unlock, err := r.lock(ctx)
	if err != nil {
		return apitypes.Result{}, "", err
	}
	defer unlock()
	claims, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return apitypes.Result{}, "", err
	}
	target := apitypes.Ref{Kind: "interface", ID: req.InterfaceID}
	check := func(ctx context.Context, q querier) error {
		claims, err = r.checkOperation(ctx, q, credential, op, []apitypes.Ref{target}, true)
		if err != nil {
			return err
		}
		if !r.capture.allowed[target.ID] {
			return apitypes.Fail(404, "CAPTURE_INTERFACE_NOT_AUTHORIZED")
		}
		return nil
	}
	command := requests.Command{Principal: claims.PrincipalID, Credential: claims.CredentialID, Capability: captureCap,
		Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID, Method: input.Method, URI: input.URI, Payload: body}
	var admitted string
	out, err := r.receipts.Execute(ctx, command, func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, conn *sql.Conn) error { return check(ctx, conn) })
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		result := requests.Mutation{Status: 202, Body: json.RawMessage(`{}`)}
		if err := check(ctx, tx); err != nil {
			return result, err
		}
		if _, err := r.capture.resolver(ctx, target.ID); err != nil {
			return result, err
		}
		now := r.now()
		if err := pruneCaptureArtifacts(ctx, tx, now); err != nil {
			return result, err
		}
		var active, files int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),(SELECT count(*) FROM capture_artifacts)
          FROM jobs j JOIN evidence_jobs e ON e.id=j.id
          WHERE j.state IN ('queued','running','cancel-requested') AND e.operation=?`,
			captureOperation).Scan(&active, &files); err != nil {
			return result, err
		}
		if active != 0 || files >= maxCaptureFiles {
			return result, apitypes.Fail(429, "CAPTURE_CAPACITY_REACHED")
		}
		job, err := evidence.CreateJob(ctx, tx, evidence.Job{State: "queued", Reason: "capture-admitted", Resource: &target, Cancellable: true, Created: now})
		if err != nil {
			return result, err
		}
		result.Resource, result.Job = &target, &apitypes.Ref{Kind: "job", ID: job.ID}
		if err := r.audit(ctx, tx, claims, op.ID, job.ID, "capture-admitted", input.RequestID, &target, result.Job); err != nil {
			return result, err
		}
		admitted = job.ID
		return result, r.touch(ctx, tx, credential, claims)
	})
	return out, admitted, err
}

func (r *Repository) runCapture(serviceCtx context.Context, credential string, op *apicontract.Operation, id string, req capture.Request) {
	ctx, cancel := context.WithTimeout(serviceCtx, time.Duration(req.Seconds+3)*time.Second)
	defer cancel()
	var admitted bool
	target := apitypes.Ref{Kind: "interface", ID: req.InterfaceID}
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		job, err := evidence.LoadJob(ctx, tx, id)
		if err != nil || job.State != "queued" {
			return err
		}
		if _, err := r.checkOperation(ctx, tx, credential, op, []apitypes.Ref{target}, true); err != nil {
			return err
		}
		_, err = evidence.ChangeJob(ctx, tx, id, job.Sequence, evidence.Transition{State: "running", Dispatch: "sent", Reason: "capture-collecting"}, r.now())
		admitted = err == nil
		return err
	})
	var captured capture.Result
	if err == nil && admitted {
		go r.watchDiagnosticCancellation(ctx, cancel, id, credential, captureCap)
		captured, err = capture.Observe(ctx, r.capture.resolver, req)
	}
	cancel()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(serviceCtx), 3*time.Second)
	defer finishCancel()
	_ = r.finishCapture(finishCtx, credential, op, id, req, captured, err)
}

type captureMetadata struct {
	ID        string          `json:"id"`
	Target    apitypes.Ref    `json:"target_ref"`
	Profile   capture.Profile `json:"profile"`
	State     string          `json:"state"`
	Reason    string          `json:"reason_code,omitempty"`
	Packets   int             `json:"packets"`
	Bytes     int             `json:"bytes"`
	Parts     int             `json:"parts"`
	Digest    string          `json:"sha256"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at"`
	ExpiresAt time.Time       `json:"expires_at"`
}

func (r *Repository) finishCapture(ctx context.Context, credential string, op *apicontract.Operation, id string, req capture.Request, captured capture.Result, runErr error) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		job, err := evidence.LoadJob(ctx, tx, id)
		if err != nil || evidence.Terminal(job.State) {
			return err
		}
		now := r.now()
		transition := evidence.Transition{State: "failed", Business: "failure", Reason: "capture-not-completed"}
		if job.Dispatch == "sent" {
			transition.Dispatch = "completed"
		}
		target := apitypes.Ref{Kind: "interface", ID: req.InterfaceID}
		_, authErr := r.checkOperation(ctx, tx, credential, op, []apitypes.Ref{target}, false)
		switch {
		case job.CancelRequested:
			transition.State, transition.Business, transition.Reason = "cancelled", "cancelled", "capture-cancelled"
		case authErr != nil:
			transition.Reason = "capture-authority-unavailable"
		case errors.Is(runErr, context.DeadlineExceeded):
			transition.Reason = "capture-timed-out"
		case runErr != nil:
			var known *apitypes.Problem
			if errors.As(runErr, &known) {
				transition.Reason = known.Code
			}
		case captured.State != "":
			if err := writeCapture(ctx, tx, id, req, target, now, captured); err != nil {
				return err
			}
			transition.State, transition.Business, transition.Reason = "succeeded", "success", "capture-"+captured.State
		}
		return finishDiagnosticJob(ctx, tx, job, transition, now)
	})
}

func writeCapture(ctx context.Context, tx *sql.Tx, id string, req capture.Request, target apitypes.Ref, now time.Time, captured capture.Result) error {
	hash := sha256.Sum256(captured.Bytes)
	info := captureMetadata{ID: id, Target: target, Profile: req.Profile, State: captured.State, Reason: captured.Reason,
		Packets: captured.Packets, Bytes: len(captured.Bytes), Parts: (len(captured.Bytes) + supportPartBytes - 1) / supportPartBytes,
		Digest: hex.EncodeToString(hash[:]), StartedAt: captured.StartedAt, EndedAt: captured.EndedAt, ExpiresAt: now.Add(captureTTL)}
	document, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO capture_artifacts(id,interface_id,expires_ms,digest,manifest,content) VALUES(?,?,?,?,?,?)",
		id, target.ID, info.ExpiresAt.UnixMilli(), info.Digest, document, captured.Bytes)
	return err
}

func (r *Repository) readCapture(ctx context.Context, tx *sql.Tx, claims authn.Claims, id, part string) (any, error) {
	if err := require(claims, captureCap, false, 0); err != nil {
		return nil, err
	}
	if r.capture == nil {
		return nil, apitypes.Fail(503, "CAPTURE_PROVIDER_UNAVAILABLE")
	}
	var target, digest string
	var document []byte
	now := r.now()
	var expiry int64
	err := tx.QueryRowContext(ctx, "SELECT interface_id,digest,manifest,expires_ms FROM capture_artifacts WHERE id=? AND expires_ms>?",
		id, now.UnixMilli()).Scan(&target, &digest, &document, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apitypes.Fail(404, "CAPTURE_ARTIFACT_UNAVAILABLE")
	}
	if err != nil {
		return nil, err
	}
	if !r.capture.allowed[target] {
		return nil, apitypes.Fail(404, "CAPTURE_INTERFACE_NOT_AUTHORIZED")
	}
	if err := checkRefs(ctx, tx, claims, []apitypes.Ref{{Kind: "interface", ID: target}}); err != nil {
		return nil, err
	}
	var info captureMetadata
	if len(document) > 4096 || json.Unmarshal(document, &info) != nil || info.ID != id || info.Target.ID != target ||
		info.Digest != digest || !now.Before(info.ExpiresAt) || info.ExpiresAt.UnixMilli() != expiry ||
		info.Bytes < 24 || info.Bytes > capture.MaxBytes || info.Parts != (info.Bytes+supportPartBytes-1)/supportPartBytes {
		return nil, apitypes.Fail(503, "CAPTURE_ARTIFACT_UNAVAILABLE")
	}
	if part == "" {
		return info, nil
	}
	index, err := strconv.Atoi(part)
	if err != nil || strconv.Itoa(index) != part || index < 0 || index >= info.Parts {
		return nil, apitypes.Fail(404, "CAPTURE_ARTIFACT_UNAVAILABLE")
	}
	var data []byte
	err = tx.QueryRowContext(ctx, "SELECT substr(content,?,?) FROM capture_artifacts WHERE id=?",
		index*supportPartBytes+1, supportPartBytes, id).Scan(&data)
	if err != nil {
		return nil, err
	}
	if len(data) != min(supportPartBytes, info.Bytes-index*supportPartBytes) {
		return nil, apitypes.Fail(503, "CAPTURE_ARTIFACT_UNAVAILABLE")
	}
	return map[string]any{"id": id, "part": index, "data_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func pruneCaptureArtifacts(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM capture_artifacts WHERE expires_ms<=?", now.UnixMilli())
	return err
}
