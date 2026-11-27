package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/runtimehealth"
)

func (r *Repository) WithManagementDebug(debug *runtimehealth.Debug) {
	r.debug = debug
}

func (r *Repository) setManagementDebug(ctx context.Context, credential string, input authn.Command, op *apicontract.Operation, path map[string]string, query url.Values) (apitypes.Result, error) {
	var out apitypes.Result
	if r.debug == nil {
		return out, apitypes.Fail(503, "MANAGEMENT_DEBUG_UNAVAILABLE")
	}
	headers := map[string][]string{"Idempotency-Key": {input.RequestID}, "X-OVS-Request-Epoch": {input.Epoch}}
	if err := op.ValidateParameters(path, query, func(name string) []string { return headers[name] }); err != nil {
		return out, err
	}
	values, body, err := op.Decode(input.Payload)
	if err != nil {
		return out, err
	}
	if values["request_id"] != input.RequestID {
		return out, apitypes.Fail(409, "IDEMPOTENCY_KEY_MISMATCH")
	}
	// The contract checked this exact integer against a finite enum. Request
	// receipts already canonicalize equivalent JSON number spellings.
	number, _ := values["duration_seconds"].(json.Number)
	seconds, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return out, apitypes.Fail(422, "INVALID_REQUEST")
	}
	duration := int(seconds)
	reason := "debug-lease-disabled"
	switch duration {
	case 0:
	case 300, 600, 900:
		reason = "debug-lease-authorized-" + strconv.Itoa(duration) + "s"
	default:
		return out, apitypes.Fail(422, "INVALID_REQUEST")
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	c, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return out, err
	}
	command := requests.Command{
		Principal: c.PrincipalID, Credential: c.CredentialID, Capability: op.Capability,
		Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID,
		Method: input.Method, URI: input.URI, Payload: body,
	}
	var next runtimehealth.DebugLease
	out, err = r.receipts.Execute(ctx, command, func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			_, err := r.checkOperation(ctx, q, credential, op, nil, true)
			return err
		})
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		target := &apitypes.Ref{Kind: "health", ID: runtimehealth.ResourceID}
		result := requests.Mutation{Status: 202, Terminal: true, Body: json.RawMessage(`{}`), Resource: target}
		checked, err := r.checkOperation(ctx, tx, credential, op, nil, true)
		if err != nil {
			return result, err
		}
		if old, active := r.debug.State(); old.JobID != "" && !active {
			if err := r.appendDebugExpiry(ctx, tx, old); err != nil {
				return result, err
			}
		}
		next = r.debug.Prepare(duration)
		if duration != 0 && next.StartedAt.IsZero() {
			return result, apitypes.Fail(503, "MANAGEMENT_DEBUG_CLOCK_UNAVAILABLE")
		}
		job, err := evidence.CreateJob(ctx, tx, evidence.Job{
			Resource: target, State: "succeeded", Business: "success", Reason: reason, Created: r.now(),
		})
		if err != nil {
			return result, err
		}
		result.Job = &apitypes.Ref{Kind: "job", ID: job.ID}
		if err = r.audit(ctx, tx, checked, op.ID, target.ID, reason, input.RequestID, target, result.Job); err != nil {
			return result, err
		}
		if duration != 0 {
			next.JobID, next.Correlation = job.ID, job.Correlation
			next.Principal, next.Credential = checked.PrincipalID, checked.CredentialID
			next.RequestID, next.RequestEpoch = input.RequestID, input.Epoch
		}
		return result, r.touch(ctx, tx, credential, checked)
	})
	if err == nil && out.Status == 202 && !out.Replayed {
		r.debug.Publish(next)
	}
	return out, err
}

func (r *Repository) appendDebugExpiry(ctx context.Context, tx *sql.Tx, lease runtimehealth.DebugLease) error {
	_, err := evidence.Append(ctx, tx, evidence.Record{
		Collection: "audit", Origin: "Manager", Critical: true, Operation: "management-debug-expire",
		Object: &apitypes.Ref{Kind: "health", ID: runtimehealth.ResourceID},
		Actor:  lease.Principal, Credential: lease.Credential, Job: lease.JobID,
		RequestID: lease.RequestID, RequestDomain: "management", RequestEpoch: lease.RequestEpoch,
		Correlation: lease.Correlation, Capability: "management.debug.write",
		Result: "debug-lease-expired", DedupKey: "debug-expiry-" + lease.JobID, Created: r.now(),
	})
	return err
}

func (r *Repository) recordDebugExpiry(ctx context.Context) error {
	if lease, active := r.debug.State(); lease.JobID == "" || active {
		return nil
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	lease, active := r.debug.State()
	if lease.JobID == "" || active {
		return nil
	}
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return r.appendDebugExpiry(ctx, tx, lease)
	})
	if err == nil {
		r.debug.Publish(runtimehealth.DebugLease{})
	}
	return err
}

// MaintainManagementDebug records expiry independently of log level admission.
// An unavailable audit store cannot keep Debug active past its deadline.
func (r *Repository) MaintainManagementDebug(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			budget, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			_ = r.recordDebugExpiry(budget)
			cancel()
		}
	}
}

func (r *Repository) SampleManagementDebug(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !logger.Enabled(ctx, slog.LevelDebug) {
				continue
			}
			code := "DEBUG_MANAGER_STORAGE_WRITABLE"
			if !r.store.Status().Writable {
				code = "DEBUG_MANAGER_STORAGE_DEGRADED"
			}
			logger.DebugContext(ctx, "management_debug_sample", "code", code, "scope", "manager")
		}
	}
}
