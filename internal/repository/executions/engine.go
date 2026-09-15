// Package executions owns the mgrd field journal. Only the typed provider can
// dispatch; restart and reconciliation paths are strictly observation-only.
package executions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

const MaxRecords = 1024
const MaxUnsettled = 16

// The SQL-local callback reloads current credentials and policy. replay=true
// checks permission/ownership only, so expired validations do not hide receipts.
type Authorizer func(context.Context, evidence.Query, execution.Request, bool) (execution.Authorization, error)

type Engine struct {
	store    *sqlite.Store
	provider execution.Provider
	key      []byte
	mu       sync.Mutex
	active   map[string]bool
}
type stored struct {
	execution.Record
	Authority execution.Authorization `json:"authorization"`
	Native    execution.Plan          `json:"plan"`
}

func New(s *sqlite.Store, key []byte, p execution.Provider) (*Engine, error) {
	if s == nil || s.Kind() != repository.Manager || len(key) != 32 || p == nil {
		return nil, repository.ErrInvalid
	}
	return &Engine{store: s, provider: p, key: append([]byte(nil), key...), active: map[string]bool{}}, nil
}
func (e *Engine) claim(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active[id] {
		return false
	}
	e.active[id] = true
	return true
}
func (e *Engine) release(id string)    { e.mu.Lock(); delete(e.active, id); e.mu.Unlock() }
func terminal(r execution.Record) bool { return r.State == "succeeded" || r.State == "failed" }

func load(ctx context.Context, q evidence.Query, id string) (execution.Record, error) {
	var b []byte
	var s stored
	if err := q.QueryRowContext(ctx, "SELECT document FROM field_executions WHERE id=?", id).Scan(&b); err != nil {
		return s.Record, err
	}
	if json.Unmarshal(b, &s) != nil || s.ID != id || s.Sequence == "" {
		return s.Record, repository.ErrUnavailable
	}
	s.Authorization, s.Plan = s.Authority, s.Native
	return s.Record, nil
}
func (e *Engine) Read(ctx context.Context, id string) (execution.Record, error) {
	var r execution.Record
	err := e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { var err error; r, err = load(ctx, q, id); return err })
	return r, err
}
func encode(r execution.Record) ([]byte, error) {
	b, err := json.Marshal(stored{r, r.Authorization, r.Plan})
	if err != nil || len(b) > 320<<10 {
		return nil, apitypes.Fail(429, "EXECUTION_RECORD_BUDGET")
	}
	return b, nil
}
func (e *Engine) authorization(ctx context.Context, in execution.Request, a Authorizer, replay bool) (execution.Authorization, error) {
	var c execution.Authorization
	if a == nil {
		return c, apitypes.Fail(503, "AUTH_UNAVAILABLE")
	}
	err := e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var err error
		c, err = a(ctx, q, in, replay)
		return err
	})
	return c, err
}

// Submit is an internal worker entry point, never an HTTP/IPC raw-write route.
// The admission coordinator must supply a persisted workspace reservation AND
// the independent Safe Apply guard. #40 owns that public admission coordinator.
// Network work runs outside SQLite transactions and the auth dispatch gate.
func (e *Engine) Submit(ctx context.Context, in execution.Request, a Authorizer, lease execution.Lease) (apitypes.Result, error) {
	var out apitypes.Result
	if candidate.Budget(in.Envelope) != nil || !apitypes.ManagementID(in.ValidationID) {
		return out, apitypes.Fail(422, "INVALID_EXECUTION")
	}
	c, err := e.authorization(ctx, in, a, true)
	if err != nil {
		return out, err
	}
	payload, _ := json.Marshal(in)
	command := requests.Command{Principal: c.Owner, Epoch: c.Epoch, Domain: "management", ID: in.ID, Operation: "createTransaction", Method: "POST", URI: "/api/v1/transactions", Payload: payload, Credential: c.Credential, Capability: "configuration.apply"}
	authorize := func(ctx context.Context) error { _, err := e.authorization(ctx, in, a, true); return err }
	receipts := requests.New(e.store)
	if prior, found, err := receipts.Replay(ctx, command, authorize); err != nil || found {
		return prior, err
	}
	if lease == nil {
		return out, apitypes.Fail(409, "SAFE_APPLY_REQUIRED")
	}
	if err = lease.Check(ctx, in); err != nil {
		return out, err
	}
	c, err = e.authorization(ctx, in, a, false)
	if err != nil {
		return out, err
	}
	id := repository.NewID()
	h := hmac.New(sha256.New, e.key)
	_, _ = h.Write([]byte("ovs-webui/field-commit/v1\x00" + id + "\x00" + c.Scope))
	p, err := e.provider.Prepare(ctx, id, hex.EncodeToString(h.Sum(nil)), in.Envelope)
	if err != nil {
		return out, err
	}
	if !e.claim(id) {
		return out, apitypes.Fail(409, "EXECUTION_BUSY")
	}
	defer e.release(id)
	var r execution.Record
	out, err = receipts.Execute(ctx, command, authorize, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		fresh, err := a(ctx, tx, in, false)
		if err != nil {
			return requests.Mutation{}, err
		}
		if fresh != c {
			return requests.Mutation{}, apitypes.Fail(409, "EXECUTION_AUTHORITY_CHANGED")
		}
		var total, unsettled int
		if err = tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(state NOT IN ('succeeded','failed')),0) FROM field_executions").Scan(&total, &unsettled); err != nil {
			return requests.Mutation{}, err
		}
		if total >= MaxRecords || unsettled >= MaxUnsettled {
			return requests.Mutation{}, apitypes.Fail(429, "EXECUTION_CAPACITY_REACHED")
		}
		now := time.Now().UTC()
		request := evidence.RequestFrom(ctx)
		r = execution.Record{ID: id, Sequence: "1", State: "admitted", Owner: c.Owner, RequestID: in.ID, JobID: repository.NewID(), Correlation: request.Correlation, ValidationID: in.ValidationID, CandidateID: in.Envelope.Candidate.ID, CandidateRevision: in.Envelope.Candidate.Revision, Generation: p.Generation, Created: now, Updated: now, Outcome: execution.Outcome{Commit: "not-sent", Applied: "not-applied", Reason: "admitted"}, Authorization: c, Plan: p}
		b, err := encode(r)
		if err != nil {
			return requests.Mutation{}, err
		}
		// This legacy transport anchor gets an internal receive ID. Public
		// idempotency remains principal + epoch + domain + request_id above.
		_, err = tx.ExecContext(ctx, "INSERT INTO request_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)", repository.NewID(), repository.NewID(), id, r.Correlation, c.Owner, r.CandidateID, r.CandidateRevision, c.Scope, []byte(`{}`), "received", now.UnixMilli())
		if err != nil {
			return requests.Mutation{}, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO transaction_journal VALUES(?,?,?,?)", id, 1, r.State, []byte(`{}`))
		if err != nil {
			return requests.Mutation{}, err
		}
		// VLAN's four columns form one semantic protection unit. A different
		// Port or an unrelated non-VLAN field does not take this protection.
		for _, intent := range in.Envelope.Candidate.Intents {
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM operation_protections WHERE resource_id=? AND field_path='port.vlan'", intent.Object.ManagementID).Scan(&n); err != nil {
				return requests.Mutation{}, err
			}
			if n != 0 {
				return requests.Mutation{}, apitypes.Fail(409, "FIELD_PROTECTED")
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO operation_protections VALUES(?,'port.vlan',?)", intent.Object.ManagementID, id); err != nil {
				return requests.Mutation{}, err
			}
		}
		ref := &apitypes.Ref{Kind: "transaction", ID: id}
		if _, err = evidence.CreateJob(ctx, tx, evidence.Job{ID: r.JobID, State: "running", Resource: ref, Transaction: id, ChangeSet: c.ChangeSet, Handler: "field-execution", Dispatch: "prepared", Applied: "unknown", Reason: "execution-admitted", Created: now}); err != nil {
			return requests.Mutation{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO field_executions VALUES(?,?,?,?,?,?,?)", id, r.JobID, c.Owner, in.ValidationID, r.State, now.UnixMilli(), b); err != nil {
			return requests.Mutation{}, err
		}
		if _, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "execute-fields", Result: "admitted", Object: ref, Job: r.JobID, Transaction: id, ChangeSet: c.ChangeSet, Created: now}); err != nil {
			return requests.Mutation{}, err
		}
		return requests.Mutation{Status: 202, Body: json.RawMessage(`{}`), Resource: ref, Job: &apitypes.Ref{Kind: "job", ID: r.JobID}}, nil
	})
	if err != nil || out.Replayed {
		return out, err
	}
	beforeSend := func() error {
		if err := lease.Check(ctx, in); err != nil {
			return err
		}
		return e.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			fresh, err := a(ctx, tx, in, false)
			if err != nil {
				return err
			}
			if fresh != c || !time.Now().Before(fresh.Expires) {
				return apitypes.Fail(409, "EXECUTION_AUTHORITY_CHANGED")
			}
			current, err := load(ctx, tx, id)
			if err != nil {
				return err
			}
			if current.State != "admitted" {
				return apitypes.Fail(409, "EXECUTION_ALREADY_DISPATCHED")
			}
			r, err = e.transition(ctx, tx, current, execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: "dispatch-intent-durable"}, "committing")
			return err
		})
	}
	result := e.provider.Commit(ctx, p, beforeSend)
	// Persist the exact returned next_cfg before resync. Losing this durable
	// write loses Applied proof, even when a subsequent marker proves commit.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), sqlite.WriteTimeout)
	defer cancel()
	if err = e.saveOutcome(finish, id, result); err != nil {
		return out, err
	}
	if result.Commit == "committed" {
		result = e.provider.Observe(finish, p, result)
		err = e.saveOutcome(finish, id, result)
	}
	return out, err
}

func stateFor(r execution.Record, o execution.Outcome) string {
	if o.Commit == "rejected" {
		return "failed"
	}
	if o.Commit == "committed" && o.Applied == "applied" {
		return "succeeded"
	}
	if o.Commit == "committed" && o.Applied == "pending" && r.State != "recovery-required" && time.Since(r.Created) < execution.AppliedFor {
		return "applying"
	}
	return "recovery-required"
}
func (e *Engine) saveOutcome(ctx context.Context, id string, o execution.Outcome) error {
	return e.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		r, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if terminal(r) {
			return nil
		}
		state := stateFor(r, o)
		if state == "recovery-required" && o.Applied == "pending" {
			o.Reason = "applied-budget-exhausted"
		}
		if candidate.Digest(r.Outcome) == candidate.Digest(o) && r.State == state {
			return nil
		}
		_, err = e.transition(ctx, tx, r, o, state)
		return err
	})
}
func (e *Engine) transition(ctx context.Context, tx *sql.Tx, r execution.Record, o execution.Outcome, state string) (execution.Record, error) {
	seq, err := strconv.ParseInt(r.Sequence, 10, 64)
	if err != nil || seq <= 0 || seq == int64(^uint64(0)>>1) {
		return r, repository.ErrUnavailable
	}
	r.Sequence = strconv.FormatInt(seq+1, 10)
	r.State = state
	r.Outcome = o
	r.Updated = time.Now().UTC()
	b, err := encode(r)
	if err != nil {
		return r, err
	}
	job, err := evidence.LoadJob(ctx, tx, r.JobID)
	if err != nil {
		return r, err
	}
	jobState, dispatch, business := "running", "sent", "unknown"
	if state == "recovery-required" {
		jobState = "needs-attention"
	}
	if o.Commit == "unknown" {
		dispatch = "unknown"
	}
	if o.Commit == "rejected" {
		jobState, dispatch, business = "failed", "completed", "failure"
	}
	if state == "succeeded" {
		jobState, dispatch, business = "succeeded", "completed", "success"
	}
	ctx = evidence.WithRequest(ctx, evidence.Request{Principal: r.Owner, Credential: r.Authorization.Credential, Capability: "configuration.apply", Operation: "createTransaction", Domain: "management", Epoch: r.Authorization.Epoch, ID: r.RequestID, Correlation: r.Correlation})
	if _, err = evidence.ChangeJob(ctx, tx, job.ID, job.Sequence, evidence.Transition{State: jobState, Dispatch: dispatch, Business: business, Commit: o.Commit, Applied: o.Applied, Reason: o.Reason}, r.Updated); err != nil {
		return r, err
	}
	public, _ := json.Marshal(r)
	if _, err = tx.ExecContext(ctx, "UPDATE transaction_journal SET sequence=?,state=?,document=? WHERE transaction_id=?", seq+1, state, public, r.ID); err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE field_executions SET state=?,updated_at_ms=?,document=? WHERE id=?", state, r.Updated.UnixMilli(), b, r.ID); err != nil {
		return r, err
	}
	if terminal(r) {
		if _, err = tx.ExecContext(ctx, "DELETE FROM operation_protections WHERE transaction_id=?", r.ID); err != nil {
			return r, err
		}
	}
	_, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "field-execution-state", Result: state, Reason: o.Reason, Object: &apitypes.Ref{Kind: "transaction", ID: r.ID}, Job: r.JobID, Transaction: r.ID, ChangeSet: r.Authorization.ChangeSet, Critical: true, Created: r.Updated})
	return r, err
}

// Recover is safe to run before inventory has reconnected: absence of fresh
// evidence yields RecoveryRequired and preserves all protections. It never
// calls Prepare or Commit, including for a journal interrupted before dispatch.
func (e *Engine) Recover(ctx context.Context) error {
	var ids []string
	err := e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		rows, err := q.QueryContext(ctx, "SELECT id FROM field_executions WHERE state NOT IN ('succeeded','failed') ORDER BY updated_at_ms,id LIMIT ?", MaxUnsettled+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	if len(ids) > MaxUnsettled {
		return apitypes.Fail(503, "EXECUTION_RECOVERY_BUDGET")
	}
	for _, id := range ids {
		if err = e.Reconcile(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return ctx.Err()
}
func (e *Engine) Reconcile(ctx context.Context, id string) error {
	if !e.claim(id) {
		return nil
	}
	defer e.release(id)
	r, err := e.Read(ctx, id)
	if err != nil {
		return err
	}
	if terminal(r) {
		return nil
	}
	if r.State == "admitted" {
		return e.saveOutcome(ctx, id, execution.Outcome{Commit: "rejected", Applied: "not-applied", Reason: "recovered-before-dispatch"})
	}
	o := e.provider.Observe(ctx, r.Plan, r.Outcome)
	return e.saveOutcome(ctx, id, o)
}
func (e *Engine) Maintain(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = e.Recover(ctx)
		}
	}
}
