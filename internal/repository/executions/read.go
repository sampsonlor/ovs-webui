package executions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

func View(r execution.Record, now time.Time) map[string]any {
	phase, knowledge, commit, applied, state := r.State, "known", "not-started", r.Outcome.Applied, "unknown"
	switch r.Outcome.Commit {
	case "committed":
		commit = "committed"
	case "rejected":
		commit = "not-committed"
	case "unknown":
		commit = "ambiguous"
		knowledge = "outcome-unknown"
	}
	if applied == "pending" {
		applied = "unknown"
		state = "waiting"
	}
	if applied == "applied" {
		state = "proven"
	}
	if r.State == "succeeded" {
		phase = "health-check"
	} // Applied alone never means confirmed/healthy.
	if r.State == "failed" {
		phase = "settled"
	}
	safe := "preparing"
	if r.State == "recovery-required" {
		safe = "recovery-required"
	}
	var observed any = r.Outcome.Observed
	return map[string]any{"id": r.ID, "instance_generation": r.Generation, "sequence": r.Sequence, "phase": phase, "knowledge": knowledge, "commit_outcome": commit, "applied_outcome": applied, "safe_apply": safe, "health": "unknown", "applied_evidence": map[string]any{"provider_id": "ovsdb", "instance_generation": r.Generation, "observed_at": observed, "next_cfg_target": r.Outcome.Target, "cur_cfg": r.Outcome.Current, "state": state, "reason": r.Outcome.Reason}, "confirmation_deadline": nil, "server_time": now, "allowed_actions": []string{}, "job_ref": apitypes.Ref{Kind: "job", ID: r.JobID}, "correlation_id": r.Correlation, "candidate_id": r.CandidateID, "candidate_revision": r.CandidateRevision, "validation_id": r.ValidationID, "field_execution_state": r.State, "resource_kind": "transaction", "source": map[string]any{"provider_id": "mgrd", "authority": "durable-evidence", "observed_at": r.Updated, "freshness": "fresh", "confidence": "proven"}}
}

type pageCursor struct {
	Owner, Policy, After, Snapshot, Page, Filter string
	Limit                                        int
	Expires                                      int64
}

func signCursor(c pageCursor, key []byte) string {
	b, _ := json.Marshal(c)
	h := hmac.New(sha256.New, key)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func Read(ctx context.Context, q evidence.Query, c authn.Claims, op string, path map[string]string, values url.Values, key []byte, now time.Time, configured ...bool) (any, error) {
	available := len(configured) > 0 && configured[0]
	if op == "readTransaction" {
		r, err := load(ctx, q, path["transaction_id"])
		if err != nil {
			return nil, err
		}
		if r.Owner != c.PrincipalID {
			return nil, apitypes.Fail(404, "NOT_FOUND")
		}
		return safeView(ctx, q, r, c, now, available)
	}
	limit := 100
	if values.Get("limit") != "" {
		n, err := strconv.Atoi(values.Get("limit"))
		if err != nil || n < 1 || n > 500 {
			return nil, apitypes.Fail(422, "INVALID_QUERY")
		}
		limit = n
	}
	filter := values.Get("filter")
	if filter != "" {
		return nil, apitypes.Fail(422, "UNSUPPORTED_FILTER")
	}
	var snapshot string
	if err := q.QueryRowContext(ctx, "SELECT CAST(count(*) AS TEXT)||':'||CAST(coalesce(sum(j.sequence),0) AS TEXT) FROM field_executions f JOIN transaction_journal j ON j.transaction_id=f.id WHERE f.owner_id=?", c.PrincipalID).Scan(&snapshot); err != nil {
		return nil, err
	}
	cursor := pageCursor{Owner: c.PrincipalID, Policy: candidatePermission(c), Snapshot: snapshot, Page: repository.NewID(), Filter: filter, Limit: limit, Expires: now.Add(90 * time.Second).Unix()}
	if raw := values.Get("cursor"); raw != "" {
		parts := strings.Split(raw, ".")
		if len(parts) != 2 {
			return nil, apitypes.Fail(422, "CURSOR_INVALID")
		}
		b, e1 := base64.RawURLEncoding.DecodeString(parts[0])
		sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
		h := hmac.New(sha256.New, key)
		h.Write(b)
		var prior pageCursor
		if e1 != nil || e2 != nil || !hmac.Equal(sig, h.Sum(nil)) || json.Unmarshal(b, &prior) != nil {
			return nil, apitypes.Fail(422, "CURSOR_INVALID")
		}
		if prior.Owner != cursor.Owner || prior.Policy != cursor.Policy || prior.Limit != limit || prior.Filter != filter || prior.Snapshot != snapshot || now.Unix() >= prior.Expires {
			return nil, apitypes.Fail(409, "CURSOR_STALE")
		}
		cursor = prior
	}
	rows, err := q.QueryContext(ctx, "SELECT document FROM field_executions WHERE owner_id=? AND id>? ORDER BY id LIMIT ?", c.PrincipalID, cursor.After, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	bytes := 0
	more := false
	for rows.Next() {
		var b []byte
		var s stored
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if json.Unmarshal(b, &s) != nil {
			return nil, apitypes.Fail(503, "EXECUTION_EVIDENCE_INVALID")
		}
		v, err := safeView(ctx, q, s.Record, c, now, available)
		if err != nil {
			return nil, err
		}
		blob, _ := json.Marshal(v)
		if len(items) >= limit || bytes+len(blob) > 36<<10 {
			more = true
			break
		}
		items = append(items, v)
		bytes += len(blob)
		cursor.After = s.ID
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var next any
	if more {
		next = signCursor(cursor, key)
	}
	return map[string]any{"items": items, "next_cursor": next, "snapshot_id": cursor.Page, "instance_generation": nil, "truncated": more, "server_time": now}, nil
}
func safeView(ctx context.Context, q evidence.Query, r execution.Record, c authn.Claims, now time.Time, configured bool) (map[string]any, error) {
	v := View(r, now)
	s, err := loadSafety(ctx, q, r.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	v["safe_apply"], v["recovery_reason"] = s.State, s.Reason
	v["recovery_trigger"] = s.Trigger
	if s.State != "preparing" {
		v["phase"] = s.State
	}
	if safety.Terminal(s.State) {
		v["phase"] = "settled"
	}
	if s.Confirmation != nil {
		v["confirmation_deadline"] = s.Confirmation.Wall
	}
	fresh := s.HealthyAt != nil && !now.Before(*s.HealthyAt) && now.Sub(*s.HealthyAt) < safety.EvidenceFreshFor
	if fresh {
		v["health"] = "healthy"
	}
	v["reachability_observed_at"] = s.HealthyAt
	v["rollback_evidence"] = s.Outcome
	actions := []string{}
	if configured && !safety.Terminal(s.State) {
		if s.State == "awaiting-confirmation" && fresh && s.Confirmation != nil && !s.Confirmation.Expired(tlscontrol.Now()) && slices.Contains(c.Capabilities, "configuration.confirm") {
			actions = append(actions, "confirm")
		}
		if s.Rollback == nil && s.State != "rollback-conflict" && slices.Contains(c.Capabilities, "configuration.rollback") {
			actions = append(actions, "rollback")
		}
	}
	v["allowed_actions"] = actions
	return v, nil
}
func candidatePermission(c authn.Claims) string {
	b, _ := json.Marshal([]any{c.Revision, c.Epoch, c.CredentialID, c.Capabilities})
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
