package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func Append(ctx context.Context, tx *sql.Tx, r Record) (string, error) {
	request := RequestFrom(ctx)
	if r.Correlation == "" {
		r.Correlation = request.Correlation
	}
	if r.RequestID == "" {
		r.RequestID, r.RequestDomain, r.RequestEpoch = request.ID, request.Domain, request.Epoch
	}
	if r.Capability == "" {
		r.Capability = request.Capability
	}
	if r.Actor == "" && r.Origin == "Manager" {
		r.Actor = request.Principal
	}
	if r.Credential == "" && r.Origin == "Manager" {
		r.Credential = request.Credential
	}
	if r.Correlation == "" {
		r.Correlation = repository.NewID()
	}
	if r.ID == "" {
		r.ID = repository.NewID()
	}
	if r.Created.IsZero() {
		r.Created = time.Now().UTC()
	}
	if r.Origin != "Manager" {
		r.Actor, r.Credential = "", ""
	}
	if (r.Collection != "event" && r.Collection != "audit") || (r.Origin != "Manager" && r.Origin != "External" && r.Origin != "Unknown") || !apitypes.ManagementID(r.ID) || !apitypes.ManagementID(r.Correlation) || !optionalID(r.Actor) || !optionalID(r.Credential) || !optionalID(r.ChangeSet) || !optionalID(r.Transaction) || !optionalID(r.Job) || !optionalID(r.RequestEpoch) || !optionalRequest(r.RequestID) || !validRef(r.Object) || !code(r.Operation) || !code(r.Result) || (r.Reason != "" && !code(r.Reason)) || (r.Capability != "" && !code(r.Capability)) || (r.DedupKey != "" && !code(r.DedupKey)) || (r.RequestDomain != "" && r.RequestDomain != "management" && r.RequestDomain != "workspace") {
		return "", apitypes.Fail(422, "INVALID_EVIDENCE")
	}
	canonical := r
	canonical.ID, canonical.Sequence, canonical.Created = "", "", time.Time{}
	digest := hash(canonical)
	if r.DedupKey != "" {
		var id, prior string
		err := tx.QueryRowContext(ctx, "SELECT id,fingerprint FROM evidence_records WHERE collection=? AND source=? AND dedup_key=?", r.Collection, r.Origin, r.DedupKey).Scan(&id, &prior)
		if err == nil {
			if prior != digest {
				return "", apitypes.Fail(409, "EVIDENCE_DEDUP_MISMATCH")
			}
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM evidence_records WHERE collection=?", r.Collection).Scan(&count); err != nil {
		return "", err
	}
	limit := MaxEvents
	if r.Collection == "audit" {
		limit = MaxAudit
	}
	// New work yields before the hard cap so admitted work can record its
	// completion or recovery. Only trusted lifecycle writers use this reserve.
	if !r.Critical {
		limit -= ReservedControlRecords
	}
	if count >= limit {
		return "", apitypes.Fail(429, "EVIDENCE_CAPACITY_REACHED")
	}
	var objectID, dedup any
	if r.Object != nil {
		objectID = r.Object.ID
	}
	if r.DedupKey != "" {
		dedup = r.DedupKey
	}
	// The sequence is assigned by SQLite; it is not a caller-provided ordering.
	res, err := tx.ExecContext(ctx, "INSERT INTO evidence_records(id,collection,created_at_ms,job_id,transaction_id,request_id,correlation_id,operation,object_id,source,dedup_key,fingerprint,document) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", r.ID, r.Collection, r.Created.UnixMilli(), nullable(r.Job), nullable(r.Transaction), nullable(r.RequestID), r.Correlation, r.Operation, objectID, r.Origin, dedup, digest, []byte(`{}`))
	if err != nil {
		return "", err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return "", err
	}
	r.Sequence = strconv.FormatInt(seq, 10)
	b, err := json.Marshal(r)
	if err != nil || len(b) > 8192 {
		return "", apitypes.Fail(422, "EVIDENCE_BUDGET_EXCEEDED")
	}
	_, err = tx.ExecContext(ctx, "UPDATE evidence_records SET document=? WHERE sequence=?", b, seq)
	return r.ID, err
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func CreateJob(ctx context.Context, tx *sql.Tx, j Job) (Job, error) {
	r := RequestFrom(ctx)
	if j.ID == "" {
		j.ID = repository.NewID()
	}
	if j.Owner == "" {
		j.Owner = r.Principal
	}
	if j.Operation == "" {
		j.Operation = r.Operation
	}
	if j.Capability == "" {
		j.Capability = r.Capability
	}
	if j.Correlation == "" {
		j.Correlation = r.Correlation
	}
	if j.Correlation == "" {
		j.Correlation = repository.NewID()
	}
	if j.RequestID == "" {
		j.RequestID, j.RequestDomain, j.RequestEpoch = r.ID, r.Domain, r.Epoch
	}
	if j.Credential == "" {
		j.Credential = r.Credential
	}
	if j.Created.IsZero() {
		j.Created = time.Now().UTC()
	}
	j.Updated = j.Created
	j.Sequence = "1"
	if j.State == "" {
		j.State = "queued"
	}
	if j.Handler == "" {
		j.Handler = "none"
	}
	if j.Dispatch == "" {
		j.Dispatch = "not-started"
	}
	if j.Business == "" {
		j.Business = "unknown"
	}
	if j.Commit == "" {
		j.Commit = "not-sent"
	}
	if j.Applied == "" {
		j.Applied = "not-applicable"
	}
	if j.Confirmation == "" {
		j.Confirmation = "not-applicable"
	}
	if j.Reason == "" {
		j.Reason = "admitted"
	}
	if !apitypes.ManagementID(j.ID) || !apitypes.ManagementID(j.Owner) || !apitypes.ManagementID(j.Correlation) || !code(j.Operation) || !code(j.Capability) || !validRef(j.Resource) || !optionalID(j.ChangeSet) || !optionalID(j.Transaction) || !optionalID(j.Credential) || !optionalRequest(j.RequestID) || !optionalID(j.RequestEpoch) || (j.State != "queued" && j.State != "running" && !Terminal(j.State) && j.State != "needs-attention") || (j.Handler != "none" && j.Handler != "tls-activation" && j.Handler != "provider") {
		return Job{}, apitypes.Fail(422, "INVALID_JOB")
	}
	var total, queued, running int
	if err := tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(j.state='queued'),0),coalesce(sum(j.state IN ('running','cancel-requested')),0) FROM jobs j").Scan(&total, &queued, &running); err != nil {
		return Job{}, err
	}
	jobLimit := MaxJobs
	if !ControlOperation(j.Operation) {
		jobLimit -= ReservedControlRecords
	}
	if total >= jobLimit || j.State == "queued" && queued >= MaxQueued || j.State == "running" && running >= MaxRunning {
		return Job{}, apitypes.Fail(429, "JOB_CAPACITY_REACHED")
	}
	if Terminal(j.State) {
		j.Completed = &j.Updated
		j.Cancellable = false
	}
	if !validOutcomes(j) {
		return Job{}, apitypes.Fail(422, "INVALID_JOB_OUTCOME")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO jobs VALUES(?,?,?,?)", j.ID, nullable(j.Transaction), j.State, []byte(`{}`)); err != nil {
		return Job{}, err
	}
	if err := saveJob(ctx, tx, &j, true); err != nil {
		return Job{}, err
	}
	_, err := Append(ctx, tx, Record{Collection: "event", Origin: "Manager", Capability: j.Capability, Operation: "job-created", Critical: ControlOperation(j.Operation), Object: j.Resource, Job: j.ID, Transaction: j.Transaction, ChangeSet: j.ChangeSet, Correlation: j.Correlation, RequestID: j.RequestID, RequestDomain: j.RequestDomain, RequestEpoch: j.RequestEpoch, Result: j.State, Reason: j.Reason, Created: j.Created})
	return j, err
}
func saveJob(ctx context.Context, tx *sql.Tx, j *Job, insert bool) error {
	var version int64
	if err := tx.QueryRowContext(ctx, "UPDATE evidence_state SET revision=revision+1 WHERE singleton=1 RETURNING revision").Scan(&version); err != nil {
		return err
	}
	if insert {
		if j.Sequence == "" {
			j.Sequence = "1"
		}
		if n, err := strconv.ParseUint(j.Sequence, 10, 64); err != nil || n == 0 {
			return apitypes.Fail(503, "JOB_SEQUENCE_INVALID")
		}
	} else {
		sequence, err := strconv.ParseUint(j.Sequence, 10, 64)
		if err != nil || sequence == 0 || sequence == ^uint64(0) {
			return apitypes.Fail(503, "JOB_SEQUENCE_INVALID")
		}
		j.Sequence = strconv.FormatUint(sequence+1, 10)
	}
	b, err := json.Marshal(j)
	if err != nil || len(b) > 8192 {
		return apitypes.Fail(422, "JOB_BUDGET_EXCEEDED")
	}
	var completed any
	if j.Completed != nil {
		completed = j.Completed.UnixMilli()
	}
	if insert {
		_, err = tx.ExecContext(ctx, "INSERT INTO evidence_jobs VALUES(?,?,?,?,?,?,?,?,?,?)", j.ID, j.Owner, j.Capability, j.Operation, version, j.Created.UnixMilli(), j.Updated.UnixMilli(), completed, j.Handler, b)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE evidence_jobs SET version=?,updated_at_ms=?,completed_at_ms=?,document=? WHERE id=?", version, j.Updated.UnixMilli(), completed, b, j.ID)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE jobs SET state=?,document=? WHERE id=?", j.State, b, j.ID)
	return err
}
func LoadJob(ctx context.Context, q Query, id string) (Job, error) {
	var j Job
	var b []byte
	err := q.QueryRowContext(ctx, "SELECT document FROM evidence_jobs WHERE id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return j, apitypes.Fail(404, "NOT_FOUND")
	}
	if err != nil {
		return j, err
	}
	if json.Unmarshal(b, &j) != nil {
		return j, apitypes.Fail(503, "JOB_EVIDENCE_INVALID")
	}
	return j, nil
}

// Transition contains outcome evidence from a trusted typed executor. A request
// to cancel does not prove that a sent provider operation was cancelled.
type Transition struct{ State, Dispatch, Business, Commit, Applied, Confirmation, Reason string }

func ChangeJob(ctx context.Context, tx *sql.Tx, id, expected string, t Transition, now time.Time) (Job, error) {
	j, err := LoadJob(ctx, tx, id)
	if err != nil {
		return j, err
	}
	if expected != j.Sequence {
		return Job{}, apitypes.Fail(409, "JOB_REVISION_CHANGED")
	}
	if Terminal(j.State) {
		return Job{}, apitypes.Fail(409, "JOB_TERMINAL")
	}
	valid := false
	switch j.State {
	case "queued":
		valid = t.State == "running" || t.State == "cancelled" || t.State == "failed" || t.State == "needs-attention"
	case "running", "cancel-requested", "needs-attention":
		valid = t.State == "cancel-requested" || t.State == "needs-attention" || Terminal(t.State)
	}
	if !valid || !code(t.Reason) {
		return Job{}, apitypes.Fail(422, "INVALID_JOB_TRANSITION")
	}
	if j.State == "queued" && t.State == "running" {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE state IN ('running','cancel-requested')").Scan(&n); err != nil {
			return Job{}, err
		}
		if n >= MaxRunning {
			return Job{}, apitypes.Fail(429, "JOB_RUNNING_CAPACITY_REACHED")
		}
	}
	j.State, j.Updated, j.Reason = t.State, now.UTC(), t.Reason
	for field, value := range map[*string]string{&j.Dispatch: t.Dispatch, &j.Business: t.Business, &j.Commit: t.Commit, &j.Applied: t.Applied, &j.Confirmation: t.Confirmation} {
		if value != "" {
			if !code(value) {
				return Job{}, apitypes.Fail(422, "INVALID_JOB_OUTCOME")
			}
			*field = value
		}
	}
	if j.State == "cancel-requested" {
		j.CancelRequested = true
		j.Cancellable = false
	}
	if Terminal(j.State) {
		j.Completed = &j.Updated
		j.Cancellable = false
	}
	if !validOutcomes(j) {
		return Job{}, apitypes.Fail(422, "INVALID_JOB_OUTCOME")
	}
	if err = saveJob(ctx, tx, &j, false); err != nil {
		return Job{}, err
	}
	if j.Completed != nil {
		if err = completeReceipts(ctx, tx, j, now); err != nil {
			return Job{}, err
		}
	}
	_, err = Append(ctx, tx, Record{Collection: "event", Origin: "Manager", Capability: j.Capability, Operation: "job-state-changed", Critical: true, Object: j.Resource, Job: j.ID, Transaction: j.Transaction, ChangeSet: j.ChangeSet, Correlation: j.Correlation, RequestID: j.RequestID, RequestDomain: j.RequestDomain, RequestEpoch: j.RequestEpoch, Result: j.State, Reason: j.Reason, Created: now})
	return j, err
}
func completeReceipts(ctx context.Context, tx *sql.Tx, j Job, now time.Time) error {
	rows, err := tx.QueryContext(ctx, "SELECT principal_id,epoch,request_id,receipt FROM api_receipts WHERE json_extract(receipt,'$.job_ref.id')=? AND completed_at_ms IS NULL", j.ID)
	if err != nil {
		return err
	}
	type row struct {
		principal, epoch, id string
		receipt              apitypes.Receipt
	}
	pending := []row{}
	for rows.Next() {
		var v row
		var b []byte
		if err = rows.Scan(&v.principal, &v.epoch, &v.id, &b); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(b, &v.receipt) != nil {
			rows.Close()
			return apitypes.Fail(503, "REQUEST_EVIDENCE_INVALID")
		}
		pending = append(pending, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range pending {
		v.receipt.State = "completed"
		b, _ := json.Marshal(v.receipt)
		if _, err = tx.ExecContext(ctx, "UPDATE api_receipts SET receipt=?,completed_at_ms=? WHERE principal_id=? AND epoch=? AND domain='management' AND request_id=?", b, now.UnixMilli(), v.principal, v.epoch, v.id); err != nil {
			return err
		}
	}
	return nil
}
