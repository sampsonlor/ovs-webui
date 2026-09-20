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
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type SafetyOptions struct {
	Probe safety.Probe
	Clock func() tlscontrol.Clock
	// Authority reloads credential IDs and current policy without persisting a
	// reusable bearer. Loss of permission starts compensation, never confirmation.
	Authority func(context.Context, evidence.Query, execution.Authorization) error
	Window    time.Duration
}
type safeAdmission struct {
	command     requests.Command
	reservation string
}

func (e *Engine) ConfigureSafety(o SafetyOptions) error {
	if _, ok := e.provider.(safety.Provider); !ok || o.Probe == nil || o.Probe.Domain() == "" || o.Authority == nil {
		return apitypes.Fail(409, "SAFE_APPLY_CAPABILITY_UNAVAILABLE")
	}
	if o.Clock == nil {
		o.Clock = tlscontrol.Now
	}
	if o.Window == 0 {
		o.Window = safety.ConfirmationWindow
	}
	if o.Window < 30*time.Second || o.Window > 600*time.Second {
		return repository.ErrInvalid
	}
	e.safety = &o
	return nil
}
func (e *Engine) SafetyAvailable() bool { return e.safety != nil && e.store.Status().Writable }

func RequireSafetyConfiguration(ctx context.Context, store *sqlite.Store, configured bool) error {
	return store.Read(sqlite.RecoveryContext(ctx), func(ctx context.Context, q *sql.Conn) error {
		var n int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM safe_applies WHERE state NOT IN ('confirmed','rolled-back','not-committed')").Scan(&n); err != nil {
			return err
		}
		if n != 0 && !configured {
			return apitypes.Fail(503, "SAFE_APPLY_RECOVERY_CONFIGURATION_REQUIRED")
		}
		return nil
	})
}
func (e *Engine) safetyReady(ctx context.Context) error {
	if !e.SafetyAvailable() || e.safety.Clock().BootID == "" {
		return apitypes.Fail(409, "SAFE_APPLY_CAPABILITY_UNAVAILABLE")
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if e.safety.Probe.Check(probe) != nil {
		return apitypes.Fail(409, "MANAGEMENT_PATH_UNVERIFIED")
	}
	return nil
}
func (e *Engine) SubmitSafe(ctx context.Context, in execution.Request, a Authorizer, lease execution.Lease, command requests.Command, reservation string) (apitypes.Result, error) {
	if !apitypes.ManagementID(reservation) {
		return apitypes.Result{}, apitypes.Fail(422, "RESERVATION_REQUIRED")
	}
	return e.submit(ctx, in, a, lease, &safeAdmission{command, reservation})
}
func loadSafety(ctx context.Context, q evidence.Query, id string) (safety.Record, error) {
	var b []byte
	var s safety.Record
	if err := q.QueryRowContext(ctx, "SELECT document FROM safe_applies WHERE id=?", id).Scan(&b); err != nil {
		return s, err
	}
	if json.Unmarshal(b, &s) != nil || s.State == "" || s.Domain == "" {
		return s, repository.ErrUnavailable
	}
	return s, nil
}
func (e *Engine) admitSafety(ctx context.Context, tx *sql.Tx, r execution.Record, a safeAdmission) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM safe_resolutions WHERE owner_id=? AND request_epoch=? AND request_id=?", r.Owner, a.command.Epoch, r.RequestID).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return apitypes.Fail(409, "SAFE_APPLY_ALREADY_RESOLVED")
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM safe_applies WHERE domain=? AND state NOT IN ('confirmed','rolled-back','not-committed')", e.safety.Probe.Domain()).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return apitypes.Fail(409, "MANAGEMENT_PATH_PROTECTED")
	}
	if err := e.safety.Authority(ctx, tx, r.Authorization); err != nil {
		return err
	}
	c := e.safety.Clock()
	if c.BootID == "" {
		return apitypes.Fail(503, "SAFE_CLOCK_UNAVAILABLE")
	}
	s := safety.Record{State: "preparing", Reason: "checkpoint-durable", Domain: e.safety.Probe.Domain(), Reservation: a.reservation, Preparation: safety.NewDeadline(c, safety.PreparationBudget)}
	sum := sha256.Sum256(a.command.Payload)
	s.Fingerprint = hex.EncodeToString(sum[:])
	b, _ := json.Marshal(s)
	_, err := tx.ExecContext(ctx, "INSERT INTO safe_applies VALUES(?,?,?,?,?,?,?)", r.ID, r.Owner, a.command.Epoch, r.RequestID, s.State, s.Domain, b)
	return err
}

// updateSafety shares a single writer transaction with the decision, protection,
// Job, receipt, audit and rolling checkpoint. It never performs provider I/O.
func (e *Engine) updateSafety(ctx context.Context, tx *sql.Tx, r execution.Record, s safety.Record) error {
	seq, err := strconv.ParseInt(r.Sequence, 10, 64)
	if err != nil || seq <= 0 || seq == int64(^uint64(0)>>1) {
		return repository.ErrUnavailable
	}
	r.Sequence = strconv.FormatInt(seq+1, 10)
	r.Updated = e.safety.Clock().Wall.UTC()
	b, err := json.Marshal(s)
	if err != nil || len(b) > 640000 {
		return repository.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, "UPDATE safe_applies SET state=?,document=? WHERE id=?", s.State, b, r.ID); err != nil {
		return err
	}
	b, err = encode(r)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE field_executions SET document=?,updated_at_ms=? WHERE id=?", b, r.Updated.UnixMilli(), r.ID); err != nil {
		return err
	}
	public, _ := json.Marshal(r)
	if _, err = tx.ExecContext(ctx, "UPDATE transaction_journal SET sequence=?,state=?,document=? WHERE transaction_id=?", seq+1, s.State, public, r.ID); err != nil {
		return err
	}
	j, err := evidence.LoadJob(ctx, tx, r.JobID)
	if err != nil {
		return err
	}
	state, business, confirmation := "running", "unknown", "pending"
	commit, applied := r.Outcome.Commit, r.Outcome.Applied
	if s.State == "recovery-required" || s.State == "rollback-conflict" || j.State == "needs-attention" {
		state = "needs-attention"
	}
	if s.State == "confirmed" {
		state, business, confirmation = "succeeded", "success", "confirmed"
	}
	if s.State == "rolled-back" || s.State == "not-committed" {
		state, business, confirmation = "failed", "failure", "expired"
	}
	if s.Rollback != nil {
		commit, applied = s.Outcome.Commit, s.Outcome.Applied
	}
	if _, err = evidence.ChangeJob(ctx, tx, j.ID, j.Sequence, evidence.Transition{State: state, Business: business, Commit: commit, Applied: applied, Confirmation: confirmation, Reason: s.Reason}, r.Updated); err != nil {
		return err
	}
	if safety.Terminal(s.State) {
		if _, err = tx.ExecContext(ctx, "DELETE FROM operation_protections WHERE transaction_id=?", r.ID); err != nil {
			return err
		}
	}
	if s.State == "confirmed" {
		checkpoint, _ := json.Marshal(r.Plan)
		if _, err = tx.ExecContext(ctx, `INSERT INTO last_known_good VALUES(1,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET transaction_id=excluded.transaction_id,generation=excluded.generation,confirmed_at_ms=excluded.confirmed_at_ms,document=excluded.document`, r.ID, r.Generation, r.Updated.UnixMilli(), checkpoint); err != nil {
			return err
		}
	}
	_, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "safe-apply-state", Result: s.State, Reason: s.Reason, Object: &apitypes.Ref{Kind: "transaction", ID: r.ID}, Transaction: r.ID, Job: r.JobID, ChangeSet: r.Authorization.ChangeSet, Actor: r.Owner, Credential: r.Authorization.Credential, Capability: "configuration.apply", Correlation: r.Correlation, RequestID: r.RequestID, RequestDomain: "management", RequestEpoch: r.Authorization.Epoch, Critical: true, Created: r.Updated})
	return err
}
func (e *Engine) saveSafety(ctx context.Context, r execution.Record, s safety.Record) error {
	return e.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		current, err := load(ctx, tx, r.ID)
		if err != nil {
			return err
		}
		if current.Sequence != r.Sequence {
			return apitypes.Fail(409, "TRANSACTION_VERSION_CHANGED")
		}
		return e.updateSafety(ctx, tx, current, s)
	})
}
func (e *Engine) readSafety(ctx context.Context, id string) (execution.Record, safety.Record, error) {
	var r execution.Record
	var s safety.Record
	err := e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var err error
		r, err = load(ctx, q, id)
		if err == nil {
			s, err = loadSafety(ctx, q, id)
		}
		return err
	})
	return r, s, err
}

// SafetyTick is the independent recovery lane. Reads never call it. Each step
// uses bounded provider/probe I/O and cannot be queued behind HTTP diagnostics.
func (e *Engine) SafetyTick(ctx context.Context) error {
	ctx = sqlite.RecoveryContext(ctx)
	if e.safety == nil {
		return nil
	}
	var ids []string
	err := e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		rows, err := q.QueryContext(ctx, "SELECT id FROM safe_applies WHERE state NOT IN ('confirmed','rolled-back','not-committed') ORDER BY id LIMIT 33")
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
	if len(ids) > 32 {
		return apitypes.Fail(503, "SAFETY_RECOVERY_BUDGET")
	}
	for _, id := range ids {
		if err = e.safetyStep(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) safetyStep(ctx context.Context, id string) error {
	if !e.claim(id) {
		return nil
	}
	defer e.release(id)
	r, s, err := e.readSafety(ctx, id)
	if err != nil || safety.Terminal(s.State) {
		return err
	}
	if s.Domain != e.safety.Probe.Domain() {
		return e.holdSafety(ctx, r, s, "recovery-required", "management-probe-policy-changed")
	}
	if s.State == "rollback-conflict" {
		return nil
	}
	if s.Rollback != nil {
		// A durable rollback dispatch intent is NEVER sent again, even after a
		// lost reply or SIGKILL. Reconcile its own marker/target/Applied evidence.
		s.Outcome = e.provider.Observe(ctx, *s.Rollback, s.Outcome)
		if s.Outcome.Commit == "committed" && s.Outcome.Applied == "applied" {
			if e.probe(ctx) == nil {
				s.State, s.Reason = "rolled-back", "rollback-applied-management-reachable"
				now := e.safety.Clock().Wall
				s.HealthyAt = &now
			} else {
				s.State, s.Reason = "recovery-required", "rollback-management-path-unverified"
			}
		} else {
			s.State, s.Reason = "recovery-required", s.Outcome.Reason
		}
		return e.saveSafety(ctx, r, s)
	}
	if r.Outcome.Commit == "rejected" {
		return e.holdSafety(ctx, r, s, "not-committed", "apply-not-committed")
	}
	if r.State == "admitted" {
		return nil
	} // field recovery proves pre-dispatch rejection on restart.
	o := e.provider.Observe(ctx, r.Plan, r.Outcome)
	if o.Commit != "committed" {
		return e.holdSafety(ctx, r, s, "recovery-required", "apply-outcome-unknown")
	}
	clock := e.safety.Clock()
	expired := s.Preparation.Expired(clock)
	if s.Confirmation != nil {
		expired = s.Confirmation.Expired(clock)
	}
	if clock.Wall.Before(r.Updated) || clock.Wall.Before(s.LastWall) {
		expired = true
	}
	authorized := e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { return e.safety.Authority(ctx, q, r.Authorization) })
	rollback := expired || authorized != nil || s.State == "rollback-requested"
	if s.Trigger == "" && rollback {
		s.Trigger = "preparation-budget-exhausted"
		if s.Confirmation != nil {
			s.Trigger = "confirmation-window-expired"
		}
		if clock.BootID != s.Preparation.Boot {
			s.Trigger = "boot-changed"
		}
		if clock.Wall.Before(r.Updated) || clock.Wall.Before(s.LastWall) {
			s.Trigger = "clock-reversed"
		}
		if authorized != nil {
			s.Trigger = "apply-authority-revoked"
		}
		if s.State == "rollback-requested" {
			s.Trigger = "authorized-rollback-requested"
		}
	}
	if o.Applied != "applied" {
		if rollback {
			return e.rollback(ctx, r, s)
		}
		return e.holdSafety(ctx, r, s, "recovery-required", o.Reason)
	}
	if rollback {
		return e.rollback(ctx, r, s)
	}
	if err = e.probe(ctx); err != nil {
		s.ProbeFailures++
		s.HealthyAt = nil
		if s.ProbeFailures >= 3 {
			s.Trigger = "management-path-probes-failed"
			return e.rollback(ctx, r, s)
		}
		s.Reason = "management-path-unverified"
		return e.saveSafety(ctx, r, s)
	}
	stable := s.State == "awaiting-confirmation" && s.ProbeFailures == 0 && s.HealthyAt != nil
	s.ProbeFailures = 0
	now := clock.Wall.UTC()
	s.HealthyAt = &now
	s.LastWall = now
	if s.Confirmation == nil {
		d := safety.NewDeadline(e.safety.Clock(), e.safety.Window)
		s.Confirmation = &d
	}
	s.State, s.Reason = "awaiting-confirmation", "applied-management-reachable"
	if stable {
		// Refresh proof without manufacturing a user-visible state transition,
		// new decision sequence, audit row or a later confirmation deadline.
		return e.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			current, err := load(ctx, tx, id)
			if err != nil {
				return err
			}
			if current.Sequence != r.Sequence {
				return apitypes.Fail(409, "TRANSACTION_VERSION_CHANGED")
			}
			b, _ := json.Marshal(s)
			_, err = tx.ExecContext(ctx, "UPDATE safe_applies SET document=? WHERE id=?", b, id)
			return err
		})
	}
	return e.saveSafety(ctx, r, s)
}
func (e *Engine) probe(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return e.safety.Probe.Check(c)
}
func (e *Engine) holdSafety(ctx context.Context, r execution.Record, s safety.Record, state, reason string) error {
	if s.State == state && s.Reason == reason {
		return nil
	}
	s.State, s.Reason = state, reason
	s.HealthyAt = nil
	return e.saveSafety(ctx, r, s)
}
func (e *Engine) rollback(ctx context.Context, r execution.Record, s safety.Record) error {
	h := hmac.New(sha256.New, e.key)
	h.Write([]byte("ovs-webui/rollback/v1\x00" + r.ID + "\x00" + r.Plan.Marker))
	p, err := e.provider.(safety.Provider).PrepareRollback(ctx, r.Plan, hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		state, reason := "recovery-required", "rollback-provider-unavailable"
		var problem *apitypes.Problem
		if errors.As(err, &problem) && problem.Code == "ROLLBACK_CONFLICT" {
			state, reason = "rollback-conflict", "rollback-conflict"
		}
		if errors.As(err, &problem) && problem.Code == "ROLLBACK_GENERATION_CHANGED" {
			reason = "rollback-generation-changed"
		}
		return e.holdSafety(ctx, r, s, state, reason)
	}
	// Keep the original plan immutable. The rollback plan/outcome has its own
	// write-ahead intent; CAS includes the original marker and four after fields.
	s.Rollback = &p
	s.State, s.Reason = "rolling-back", "rollback-dispatch-durable"
	s.HealthyAt = nil
	s.Outcome = execution.Outcome{Commit: "unknown", Applied: "unknown", Reason: s.Reason}
	durable := false
	o := e.provider.Commit(ctx, p, func() error {
		err := e.saveSafety(ctx, r, s)
		durable = err == nil
		return err
	})
	if !durable {
		s.Rollback = nil
		return e.holdSafety(ctx, r, s, "recovery-required", "rollback-not-dispatched")
	}
	r, s, err = e.readSafety(ctx, r.ID)
	if err != nil {
		return err
	}
	s.Outcome = o
	if o.Commit == "rejected" {
		s.State, s.Reason = "rollback-conflict", "rollback-transaction-rejected"
	} else {
		s.Reason = o.Reason
	}
	return e.saveSafety(ctx, r, s)
}

// Decide rechecks OVS and reachability outside SQL, then arbitrates against
// timeout/revocation using a single journal CAS. No client clock is evidence.
func (e *Engine) Decide(ctx context.Context, id, sequence, decision string, check func(context.Context, evidence.Query) error, command requests.Command) (apitypes.Result, error) {
	ctx = sqlite.RecoveryContext(ctx)
	var out apitypes.Result
	authorize := func(ctx context.Context) error {
		return e.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { return check(ctx, q) })
	}
	if out, found, err := requests.New(e.store).Replay(ctx, command, authorize); err != nil || found {
		return out, err
	}
	if !e.SafetyAvailable() {
		return out, apitypes.Fail(503, "SAFE_APPLY_CAPABILITY_UNAVAILABLE")
	}
	if !e.claim(id) {
		return out, apitypes.Fail(409, "TRANSACTION_BUSY")
	}
	defer e.release(id)
	r, s, err := e.readSafety(ctx, id)
	if err != nil {
		return out, err
	}
	if r.Sequence != sequence {
		return out, apitypes.Fail(409, "TRANSACTION_VERSION_CHANGED")
	}
	if safety.Terminal(s.State) {
		return out, apitypes.Fail(409, "TRANSACTION_SETTLED")
	}
	if decision == "confirm" {
		if s.State != "awaiting-confirmation" || s.Confirmation == nil || s.Confirmation.Expired(e.safety.Clock()) || s.Domain != e.safety.Probe.Domain() {
			return out, apitypes.Fail(409, "DECISION_EXPIRED")
		}
		o := e.provider.Observe(ctx, r.Plan, r.Outcome)
		if o.Commit != "committed" || o.Applied != "applied" || e.probe(ctx) != nil {
			return out, apitypes.Fail(409, "CONFIRMATION_EVIDENCE_UNAVAILABLE")
		}
	}
	return requests.New(e.store).Execute(ctx, command, authorize, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		if err := check(ctx, tx); err != nil {
			return requests.Mutation{}, err
		}
		current, err := load(ctx, tx, id)
		if err != nil {
			return requests.Mutation{}, err
		}
		if current.Sequence != sequence {
			return requests.Mutation{}, apitypes.Fail(409, "TRANSACTION_VERSION_CHANGED")
		}
		if decision == "confirm" {
			if s.Confirmation.Expired(e.safety.Clock()) || e.safety.Clock().Wall.Before(current.Updated) || e.safety.Clock().Wall.Before(s.LastWall) {
				return requests.Mutation{}, apitypes.Fail(409, "DECISION_EXPIRED")
			}
			if err = e.safety.Authority(ctx, tx, r.Authorization); err != nil {
				return requests.Mutation{}, err
			}
			s.State, s.Reason = "confirmed", "connectivity-confirmed"
		} else if decision == "rollback" {
			if s.Rollback != nil || s.State == "rollback-conflict" {
				return requests.Mutation{}, apitypes.Fail(409, "ROLLBACK_ALREADY_DISPATCHED")
			}
			s.State, s.Reason = "rollback-requested", "authorized-rollback-requested"
		} else {
			return requests.Mutation{}, apitypes.Fail(422, "INVALID_DECISION")
		}
		if err = e.updateSafety(ctx, tx, current, s); err != nil {
			return requests.Mutation{}, err
		}
		ref := &apitypes.Ref{Kind: "transaction", ID: id}
		if _, err = evidence.Append(ctx, tx, evidence.Record{Collection: "audit", Origin: "Manager", Operation: "safe-apply-decision", Result: decision, Object: ref, Transaction: id, Job: r.JobID, Critical: true}); err != nil {
			return requests.Mutation{}, err
		}
		return requests.Mutation{Status: 202, Body: json.RawMessage(`{}`), Resource: ref, Job: &apitypes.Ref{Kind: "job", ID: r.JobID}, Terminal: decision == "confirm"}, nil
	})
}

// SafetyState is an owner-checked coordinator primitive; it performs no writes.
func (e *Engine) SafetyState(ctx context.Context, q evidence.Query, id string) (safety.Record, error) {
	return loadSafety(ctx, q, id)
}
