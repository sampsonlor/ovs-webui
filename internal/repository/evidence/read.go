package evidence

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type Query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type cursor struct {
	Principal, Permission, Operation, Filter, Correlation, Object, Job, Origin, After, Epoch, Snapshot string
	Limit                                                                                              int
	Upper, Revision, Expires                                                                           int64
}

func source(at time.Time, origin string) map[string]any {
	provider, authority, confidence := "mgrd", "durable-evidence", "proven"
	if origin == "External" {
		provider, authority = "ovsdb", "external-observation"
	}
	if origin == "Unknown" {
		authority, confidence = "unknown", "unknown"
	}
	return map[string]any{"provider_id": provider, "authority": authority, "observed_at": at, "freshness": "fresh", "confidence": confidence}
}
func jobView(j Job) map[string]any {
	b, _ := json.Marshal(j)
	v := map[string]any{}
	_ = json.Unmarshal(b, &v)
	v["resource_kind"] = "job"
	v["source"] = source(j.Updated, "Manager")
	v["allowed_actions"] = []string{}
	return v
}
func recordView(r Record) map[string]any {
	b, _ := json.Marshal(r)
	v := map[string]any{}
	_ = json.Unmarshal(b, &v)
	v["state"] = "recorded"
	v["source"] = source(r.Created, r.Origin)
	v["allowed_actions"] = []string{}
	v["kind"], v["occurred_at"], v["code"] = r.Collection, r.Created, r.Operation
	refs := []apitypes.Ref{}
	if r.Object != nil {
		refs = append(refs, *r.Object)
	}
	if r.Job != "" {
		refs = append(refs, apitypes.Ref{Kind: "job", ID: r.Job})
	}
	if r.Transaction != "" {
		refs = append(refs, apitypes.Ref{Kind: "transaction", ID: r.Transaction})
	}
	if r.ChangeSet != "" {
		refs = append(refs, apitypes.Ref{Kind: "changeset", ID: r.ChangeSet})
	}
	v["object_refs"] = refs
	v["summary"] = "Management evidence recorded."
	if r.Origin == "External" {
		v["summary"] = "External OVS inventory change observed."
	}
	if r.Origin == "Unknown" {
		v["summary"] = "Observation recorded; actor and source attribution are unavailable."
	}
	if r.Actor == "" {
		v["actor_id"] = nil
		v["actor_state"] = "unknown"
	} else {
		v["actor_state"] = "recorded"
	}
	return v
}
func Read(ctx context.Context, q Query, c authn.Claims, op string, path map[string]string, values url.Values, key []byte, now time.Time) (any, error) {
	collection, capability := "job", "jobs.read"
	if strings.Contains(strings.ToLower(op), "event") {
		collection, capability = "event", "events.read"
	}
	if strings.Contains(strings.ToLower(op), "audit") {
		collection, capability = "audit", "audit.read"
	}
	if !Operation(op) || !slices.Contains(c.Capabilities, capability) {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	viewJob := func(j Job) (map[string]any, error) {
		if j.Owner != c.PrincipalID {
			return nil, apitypes.Fail(404, "NOT_FOUND")
		}
		if !slices.Contains(c.Capabilities, j.Capability) {
			return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
		}
		v := jobView(j)
		if j.Cancellable && !Terminal(j.State) && slices.Contains(c.Capabilities, "jobs.cancel") {
			v["allowed_actions"] = []string{"cancel"}
		} else {
			v["cancellable"] = false
		}
		return v, nil
	}
	if len(path) > 0 {
		id := path[collection+"_id"]
		if collection == "job" {
			j, err := LoadJob(ctx, q, id)
			if err != nil {
				return nil, err
			}
			return viewJob(j)
		}
		var b []byte
		if err := q.QueryRowContext(ctx, "SELECT document FROM evidence_records WHERE id=? AND collection=?", id, collection).Scan(&b); err != nil {
			if err == sql.ErrNoRows {
				return nil, apitypes.Fail(404, "NOT_FOUND")
			}
			return nil, err
		}
		var r Record
		if json.Unmarshal(b, &r) != nil {
			return nil, apitypes.Fail(503, "EVIDENCE_INVALID")
		}
		return recordView(r), nil
	}
	limit := 100
	if values.Get("limit") != "" {
		var err error
		limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			return nil, apitypes.Fail(422, "INVALID_LIMIT")
		}
	}
	if len(values.Get("filter")) > 1024 {
		return nil, apitypes.Fail(422, "INVALID_FILTER")
	}
	var epoch string
	var version, pruned int64
	if err := q.QueryRowContext(ctx, "SELECT retention_epoch,revision,CASE ? WHEN 'event' THEN event_pruned_through WHEN 'audit' THEN audit_pruned_through ELSE job_pruned_through END FROM evidence_state WHERE singleton=1", collection).Scan(&epoch, &version, &pruned); err != nil {
		return nil, err
	}
	cur := cursor{Principal: c.PrincipalID, Permission: c.Revision, Operation: op, Filter: values.Get("filter"), Correlation: values.Get("correlation_id"), Object: values.Get("object_id"), Job: values.Get("job_id"), Origin: values.Get("origin"), Limit: limit, Epoch: epoch, Revision: version, Snapshot: repository.NewID(), Expires: now.Add(30 * time.Second).UnixMilli()}
	if collection != "job" {
		if err := q.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0) FROM evidence_records WHERE collection=?", collection).Scan(&cur.Upper); err != nil {
			return nil, err
		}
	}
	if token := values.Get("cursor"); token != "" {
		if len(token) > 4096 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		b, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		plain, err := authn.Unseal(key, b, []byte("durable-evidence-page-v1"))
		if err != nil {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		var old cursor
		if json.Unmarshal(plain, &old) != nil || old.Principal != cur.Principal || old.Permission != cur.Permission || old.Operation != cur.Operation || old.Filter != cur.Filter || old.Correlation != cur.Correlation || old.Object != cur.Object || old.Job != cur.Job || old.Origin != cur.Origin || old.Limit != cur.Limit || old.Epoch != cur.Epoch || old.Expires <= now.UnixMilli() || (collection == "job" && old.Revision != cur.Revision) {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		cur = old
	}
	var rows *sql.Rows
	var err error
	if collection == "job" {
		caps, _ := json.Marshal(c.Capabilities)
		rows, err = q.QueryContext(ctx, "SELECT document,id FROM evidence_jobs WHERE owner_id=? AND capability IN (SELECT value FROM json_each(?)) AND id>? AND instr(lower(operation),lower(?))>0 AND (?='' OR json_extract(document,'$.correlation_id')=?) AND (?='' OR json_extract(document,'$.resource_ref.id')=?) AND (?='' OR id=?) AND (?='' OR ?='Manager') ORDER BY id LIMIT ?", c.PrincipalID, string(caps), cur.After, cur.Filter, cur.Correlation, cur.Correlation, cur.Object, cur.Object, cur.Job, cur.Job, cur.Origin, cur.Origin, limit+1)
	} else {
		after := int64(0)
		if cur.After != "" {
			after, err = strconv.ParseInt(cur.After, 10, 64)
			if err != nil {
				return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
			}
		}
		rows, err = q.QueryContext(ctx, "SELECT document,CAST(sequence AS TEXT) FROM evidence_records WHERE collection=? AND sequence>? AND sequence<=? AND instr(lower(operation),lower(?))>0 AND (?='' OR correlation_id=?) AND (?='' OR object_id=?) AND (?='' OR job_id=?) AND (?='' OR source=?) ORDER BY sequence LIMIT ?", collection, after, cur.Upper, cur.Filter, cur.Correlation, cur.Correlation, cur.Object, cur.Object, cur.Job, cur.Job, cur.Origin, cur.Origin, limit+1)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	size := 0
	more := false
	for rows.Next() {
		var b []byte
		var after string
		if err = rows.Scan(&b, &after); err != nil {
			return nil, err
		}
		if len(items) >= limit {
			more = true
			break
		}
		var item map[string]any
		if collection == "job" {
			var j Job
			if json.Unmarshal(b, &j) != nil {
				return nil, apitypes.Fail(503, "JOB_EVIDENCE_INVALID")
			}
			item, err = viewJob(j)
			if err != nil {
				return nil, err
			}
		} else {
			var r Record
			if json.Unmarshal(b, &r) != nil {
				return nil, apitypes.Fail(503, "EVIDENCE_INVALID")
			}
			item = recordView(r)
		}
		encoded, _ := json.Marshal(item)
		if len(encoded) > PageBytes {
			return nil, apitypes.Fail(503, "EVIDENCE_REPRESENTATION_BUDGET")
		}
		if size+len(encoded) > PageBytes {
			more = true
			break
		}
		size += len(encoded)
		items = append(items, item)
		cur.After = after
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var next any
	if more {
		b, _ := json.Marshal(cur)
		sealed, err := authn.Seal(key, b, []byte("durable-evidence-page-v1"))
		if err != nil {
			return nil, err
		}
		next = base64.RawURLEncoding.EncodeToString(sealed)
	}
	days, capacity := 30, MaxJobs
	if collection == "event" {
		capacity = MaxEvents
	}
	if collection == "audit" {
		days, capacity = 180, MaxAudit
	}
	return map[string]any{"snapshot_id": cur.Snapshot, "instance_generation": nil, "items": items, "next_cursor": next, "truncated": more, "source": source(now, "Manager"), "coverage": "authorized-retained-records", "export_format": "json", "retention": map[string]any{"days": days, "max_records": capacity, "protected_records_retained": true, "reserved_control_records": ReservedControlRecords, "pruned_through_unix_ms": strconv.FormatInt(pruned, 10)}, "page_budget_bytes": PageBytes}, nil
}
