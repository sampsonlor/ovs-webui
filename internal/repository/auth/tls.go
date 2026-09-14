package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func tlsOperation(id string) bool {
	return id == "createCertificate" || id == "activateCertificate" || id == "confirmCertificate"
}

// ExecuteTLS is a separate typed consumer command. HTTP body validation occurs
// in webd; mgrd validates this closed metadata schema and its actual operation.
// A TLS private key is never a legal field of the private manager protocol.
func (r *Repository) ExecuteTLS(ctx context.Context, credential string, input tlscontrol.Command) (apitypes.Result, error) {
	var out apitypes.Result
	op, path, query, err := r.operation(input.Method, input.URI)
	if err != nil {
		return out, err
	}
	if !tlsOperation(op.ID) || op.Domain != "management" {
		return out, apitypes.Fail(403, "OPERATION_DENIED")
	}
	headers := map[string][]string{"Idempotency-Key": {input.RequestID}, "X-OVS-Request-Epoch": {input.Epoch}, "If-Match": {input.Precondition}}
	if err = op.ValidateParameters(path, query, func(n string) []string { return headers[n] }); err != nil {
		return out, err
	}
	if op.ID == "createCertificate" {
		d := input.Candidate
		if d == nil || !apitypes.ManagementID(d.ID) || !digestPattern.MatchString(d.Fingerprint) || !digestPattern.MatchString(d.InputDigest) || d.Hostname == "" || len(d.Hostname) > 253 || !d.NotAfter.After(d.NotBefore) || input.ServedID != "" || input.Precondition != "" {
			return out, apitypes.Fail(422, "TLS_DESCRIPTOR_INVALID")
		}
	} else if input.Candidate != nil || !etagPattern.MatchString(input.Precondition) || (op.ID == "activateCertificate" && input.ServedID != "") {
		return out, apitypes.Fail(422, "INVALID_REQUEST")
	}
	if _, err = r.TLSState(ctx); err != nil {
		return out, err
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
	payload, _ := json.Marshal(input)
	command := requests.Command{Principal: c.PrincipalID, Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID, Method: input.Method, URI: input.URI, Precondition: input.Precondition, Payload: payload, Sensitive: true, FingerprintKey: r.key}
	check := func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
			var err error
			c, err = r.checkOperation(ctx, q, credential, op, refsFor(op, path), true)
			return err
		})
	}
	return r.receipts.Execute(ctx, command, check, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		result := requests.Mutation{Status: 202, Terminal: true}
		var err error
		c, err = r.checkOperation(ctx, tx, credential, op, refsFor(op, path), true)
		if err != nil {
			return result, err
		}
		id := path["certificate_id"]
		jobID := repository.NewID()
		state := "succeeded"
		s, err := tlsState(ctx, tx)
		if err != nil {
			return result, err
		}
		switch op.ID {
		case "createCertificate":
			if input.Candidate.NotBefore.After(r.now()) || !input.Candidate.NotAfter.After(r.now().Add(tlscontrol.RecoveryWindow)) {
				return result, apitypes.Fail(422, "TLS_CANDIDATE_EXPIRED")
			}
			id = input.Candidate.ID
			var count int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tls_certificates").Scan(&count); err != nil {
				return result, err
			}
			if count >= 128 {
				return result, apitypes.Fail(429, "TLS_CANDIDATE_CAPACITY_REACHED")
			}
			blob, _ := json.Marshal(input.Candidate)
			if _, err = tx.ExecContext(ctx, "INSERT INTO tls_certificates(id,principal_id,revision,descriptor,state) VALUES(?,?,?,?,'validated')", id, c.PrincipalID, repository.NewID(), blob); err != nil {
				return result, err
			}
		case "activateCertificate":
			if err = revision(ctx, tx, "tls_certificates", "id", id, input.Precondition); err != nil {
				return result, err
			}
			if s.TrialID != "" || s.ActiveID == id {
				return result, apitypes.Fail(409, "TLS_ACTIVATION_CONFLICT")
			}
			var descriptor []byte
			var d tlscontrol.Descriptor
			if err = tx.QueryRowContext(ctx, "SELECT descriptor FROM tls_certificates WHERE id=?", id).Scan(&descriptor); err != nil {
				return result, err
			}
			clock := r.tlsNow()
			if json.Unmarshal(descriptor, &d) != nil || !d.NotAfter.After(clock.Wall.Add(tlscontrol.RecoveryWindow)) || clock.BootID == "" {
				return result, apitypes.Fail(422, "TLS_CANDIDATE_EXPIRED")
			}
			if _, err = tx.ExecContext(ctx, "UPDATE tls_authority SET revision=?,trial_id=?,job_id=?,principal_id=?,request_id=?,boot_id=?,started_ns=?,deadline_ns=?,deadline_wall=? WHERE singleton=1", repository.NewID(), id, jobID, c.PrincipalID, input.RequestID, clock.BootID, clock.NS, clock.NS+int64(tlscontrol.RecoveryWindow), clock.Wall.Add(tlscontrol.RecoveryWindow).Unix()); err != nil {
				return result, err
			}
			result.Terminal = false
			state = "running"
			if _, err = tx.ExecContext(ctx, "UPDATE tls_certificates SET state='awaiting-confirmation',revision=?,sequence=sequence+1 WHERE id=?", repository.NewID(), id); err != nil {
				return result, err
			}
		case "confirmCertificate":
			if err = revision(ctx, tx, "tls_certificates", "id", id, input.Precondition); err != nil {
				return result, err
			}
			if s.TrialID != id || input.ServedID != id || s.Expired(r.tlsNow()) {
				return result, apitypes.Fail(409, "TLS_FRESH_CONNECTION_REQUIRED")
			}
			if err = r.finishTLS(ctx, tx, s, true); err != nil {
				return result, err
			}
		}
		result.Resource = &apitypes.Ref{Kind: "certificate", ID: id}
		result.Job = &apitypes.Ref{Kind: "job", ID: jobID}
		doc, _ := json.Marshal(map[string]any{"operation": op.ID, "owner_id": c.PrincipalID, "resource_ref": result.Resource})
		if _, err = tx.ExecContext(ctx, "INSERT INTO jobs VALUES(?,NULL,?,?)", jobID, state, doc); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE auth_state SET revision=? WHERE singleton=1", repository.NewID()); err != nil {
			return result, err
		}
		if err = r.audit(ctx, tx, c, op.ID, id, "success", input.RequestID); err != nil {
			return result, err
		}
		return result, r.touch(ctx, tx, credential, c)
	})
}
func tlsState(ctx context.Context, q querier) (tlscontrol.State, error) {
	var s tlscontrol.State
	err := q.QueryRowContext(ctx, "SELECT revision,active_id,trial_id,job_id,boot_id,started_ns,deadline_ns,deadline_wall FROM tls_authority WHERE singleton=1").Scan(&s.Revision, &s.ActiveID, &s.TrialID, &s.JobID, &s.BootID, &s.StartedNS, &s.DeadlineNS, &s.DeadlineWall)
	return s, err
}
func (r *Repository) TLSState(ctx context.Context) (tlscontrol.State, error) {
	var s tlscontrol.State
	unlock, err := r.lock(ctx)
	if err != nil {
		return s, err
	}
	defer unlock()
	err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		s, err = tlsState(ctx, tx)
		if err != nil {
			return err
		}
		if s.Expired(r.tlsNow()) {
			if err = r.finishTLS(ctx, tx, s, false); err != nil {
				return err
			}
			s, err = tlsState(ctx, tx)
			return err
		}
		return nil
	})
	return s, err
}
func (r *Repository) finishTLS(ctx context.Context, tx *sql.Tx, s tlscontrol.State, confirmed bool) error {
	state := "failed"
	targetState := "rolled-back"
	active := s.ActiveID
	if confirmed {
		state = "succeeded"
		targetState = "active"
		active = s.TrialID
	}
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state=?,document=CAST(json_set(document,'$.sequence','2') AS BLOB) WHERE id=?", state, s.JobID); err != nil {
		return err
	}
	var blob []byte
	var principal, request, epoch string
	err := tx.QueryRowContext(ctx, "SELECT receipt,principal_id,request_id,epoch FROM api_receipts WHERE domain='management' AND json_extract(receipt,'$.job_ref.id')=?", s.JobID).Scan(&blob, &principal, &request, &epoch)
	if err != nil {
		return err
	}
	var receipt apitypes.Receipt
	if json.Unmarshal(blob, &receipt) != nil {
		return apitypes.Fail(503, "TLS_EVIDENCE_INVALID")
	}
	receipt.State = "completed"
	encoded, _ := json.Marshal(receipt)
	if _, err = tx.ExecContext(ctx, "UPDATE api_receipts SET receipt=?,completed_at_ms=? WHERE principal_id=? AND request_id=? AND epoch=? AND domain='management'", encoded, r.now().UnixMilli(), principal, request, epoch); err != nil {
		return err
	}
	if confirmed && s.ActiveID != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE tls_certificates SET state='superseded',revision=?,sequence=sequence+1 WHERE id=?", repository.NewID(), s.ActiveID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tls_certificates SET state=?,revision=?,sequence=sequence+1 WHERE id=?", targetState, repository.NewID(), s.TrialID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tls_authority SET revision=?,active_id=?,trial_id='',job_id='',principal_id='',request_id='',boot_id='',started_ns=0,deadline_ns=0,deadline_wall=0 WHERE singleton=1", repository.NewID(), active); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE auth_state SET revision=? WHERE singleton=1", repository.NewID()); err != nil {
		return err
	}
	return r.audit(ctx, tx, authn.Claims{PrincipalID: principal}, "tls-recovery", s.TrialID, targetState, request)
}
func (r *Repository) MaintainTLS(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = r.TLSState(ctx)
		}
	}
}
func (r *Repository) certificate(ctx context.Context, q querier, id string) (map[string]any, error) {
	var revision, state string
	var blob []byte
	var d tlscontrol.Descriptor
	var sequence int64
	err := q.QueryRowContext(ctx, "SELECT revision,state,descriptor,sequence FROM tls_certificates WHERE id=?", id).Scan(&revision, &state, &blob, &sequence)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(blob, &d) != nil {
		return nil, apitypes.Fail(503, "TLS_METADATA_INVALID")
	}
	actions := []string{}
	if state == "validated" || state == "rolled-back" || state == "superseded" {
		actions = append(actions, "activateCertificate")
	}
	if state == "awaiting-confirmation" {
		actions = append(actions, "confirmCertificate")
	}
	var deadline any
	s, err := tlsState(ctx, q)
	if err != nil {
		return nil, err
	}
	if s.TrialID == id {
		deadline = time.Unix(s.DeadlineWall, 0).UTC()
	}
	return map[string]any{"id": id, "revision": revision, "state": state, "sequence": strconv.FormatInt(sequence, 10), "resource_kind": "certificate", "source": map[string]any{"provider_id": "local-tls", "authority": "mgrd", "observed_at": nil, "freshness": "fresh", "confidence": "proven"}, "allowed_actions": actions, "fingerprint": d.Fingerprint, "hostname": d.Hostname, "not_before": d.NotBefore, "not_after": d.NotAfter, "private_key_configured": true, "confirmation_deadline": deadline}, nil
}
