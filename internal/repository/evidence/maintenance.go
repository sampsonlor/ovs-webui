package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

// No generic job is replayed automatically. A provider may have received work
// before a crash even when its response or final journal write was lost. TLS
// activation retains its separately persisted deadline and existing recovery.
func Recover(ctx context.Context, store *sqlite.Store, now time.Time) error {
	return store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT e.id FROM evidence_jobs e JOIN jobs j ON j.id=e.id WHERE e.handler NOT IN ('tls-activation','field-execution') AND j.state IN ('running','cancel-requested') LIMIT ?", MaxRunning+1)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) > MaxRunning {
			return apitypes.Fail(503, "JOB_RECOVERY_BUDGET")
		}
		for _, id := range ids {
			j, err := LoadJob(ctx, tx, id)
			if err != nil {
				return err
			}
			if _, err = ChangeJob(ctx, tx, id, j.Sequence, Transition{State: "needs-attention", Dispatch: "unknown", Business: "unknown", Reason: "manager-restarted-reconciliation-required"}, now); err != nil {
				return err
			}
			if _, err = Append(ctx, tx, Record{Collection: "audit", Origin: "Manager", Capability: j.Capability, Operation: "job-recovery-required", Critical: true, Object: j.Resource, Job: j.ID, Transaction: j.Transaction, Correlation: j.Correlation, RequestID: j.RequestID, RequestDomain: j.RequestDomain, RequestEpoch: j.RequestEpoch, Result: "recovery-required", Created: now}); err != nil {
				return err
			}
		}
		return nil
	})
}

// A bounded maintenance transaction protects unsettled jobs and every record
// referenced by an active transaction. Pruning is time-based, never a response
// to pressure that deletes unresolved evidence to make room for another write.
func Prune(ctx context.Context, store *sqlite.Store, now time.Time) error {
	return store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var high int64
		if err := tx.QueryRowContext(ctx, "SELECT high_watermark_ms FROM evidence_state WHERE singleton=1").Scan(&high); err != nil {
			return err
		}
		if now.UnixMilli() < high {
			return apitypes.Fail(503, "EVIDENCE_CLOCK_UNSAFE")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE evidence_state SET high_watermark_ms=? WHERE singleton=1", now.UnixMilli()); err != nil {
			return err
		}
		changed := false
		for _, collection := range []string{"event", "audit"} {
			days := 30
			if collection == "audit" {
				days = 180
			}
			rows, err := tx.QueryContext(ctx, `SELECT e.id,e.created_at_ms FROM evidence_records e WHERE e.collection=? AND e.created_at_ms<?
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=e.job_id AND j.state NOT IN ('succeeded','failed','cancelled'))
 AND NOT EXISTS(SELECT 1 FROM transaction_journal t WHERE t.transaction_id=e.transaction_id AND t.state NOT IN ('succeeded','failed','cancelled','completed','confirmed','rolled-back','not-committed'))
 AND NOT EXISTS(SELECT 1 FROM jobs j JOIN evidence_jobs k ON k.id=j.id WHERE json_extract(k.document,'$.correlation_id')=e.correlation_id AND j.state NOT IN ('succeeded','failed','cancelled'))
 ORDER BY e.created_at_ms,e.id LIMIT 256`, collection, now.Add(-time.Duration(days)*24*time.Hour).UnixMilli())
			if err != nil {
				return err
			}
			ids := []string{}
			var boundary int64
			for rows.Next() {
				var id string
				var at int64
				if err = rows.Scan(&id, &at); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
				boundary = max(boundary, at)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err = tx.ExecContext(ctx, "DELETE FROM evidence_records WHERE id=?", id); err != nil {
					return err
				}
				table := "events"
				if collection == "audit" {
					table = "auth_audit"
				}
				if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=?", id); err != nil {
					return err
				}
			}
			if len(ids) > 0 {
				changed = true
				column := "event_pruned_through"
				if collection == "audit" {
					column = "audit_pruned_through"
				}
				if _, err = tx.ExecContext(ctx, "UPDATE evidence_state SET "+column+"=max("+column+",?) WHERE singleton=1", boundary); err != nil {
					return err
				}
			}
		}
		rows, err := tx.QueryContext(ctx, `DELETE FROM jobs WHERE id IN (SELECT e.id FROM evidence_jobs e JOIN jobs j ON j.id=e.id WHERE e.completed_at_ms<? AND j.state IN ('succeeded','failed','cancelled')
 AND NOT EXISTS(SELECT 1 FROM transaction_journal t WHERE t.transaction_id=j.transaction_id AND t.state NOT IN ('succeeded','failed','cancelled','completed','confirmed','rolled-back','not-committed'))
 AND NOT EXISTS(SELECT 1 FROM api_receipts r WHERE json_extract(r.receipt,'$.job_ref.id')=j.id AND r.completed_at_ms IS NULL)
 AND NOT EXISTS(SELECT 1 FROM evidence_records r WHERE r.job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM candidate_validations v WHERE v.job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM field_executions f WHERE f.job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM evidence_jobs k JOIN jobs other ON other.id=k.id WHERE json_extract(k.document,'$.resource_ref.id')=j.id AND other.state NOT IN ('succeeded','failed','cancelled'))
 ORDER BY e.completed_at_ms,e.id LIMIT 256) RETURNING json_extract(document,'$.completed_at')`, now.Add(-30*24*time.Hour).UnixMilli())
		if err != nil {
			return err
		}
		var boundary int64
		for rows.Next() {
			var completed string
			if err = rows.Scan(&completed); err != nil {
				rows.Close()
				return err
			}
			at, parseErr := time.Parse(time.RFC3339Nano, completed)
			if parseErr != nil {
				rows.Close()
				return apitypes.Fail(503, "JOB_COMPLETION_INVALID")
			}
			boundary = max(boundary, at.UnixMilli())
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if boundary > 0 {
			changed = true
			if _, err = tx.ExecContext(ctx, "UPDATE evidence_state SET job_pruned_through=max(job_pruned_through,?) WHERE singleton=1", boundary); err != nil {
				return err
			}
		}
		if changed {
			_, err = tx.ExecContext(ctx, "UPDATE evidence_state SET retention_epoch=?,revision=revision+1 WHERE singleton=1", repository.NewID())
		}
		return err
	})
}

func ImportLegacy(ctx context.Context, store *sqlite.Store, resolve func(string) string) error {
	// Each batch is independently durable and can resume after interruption.
	for _, source := range []string{"auth_audit", "events", "audit", "jobs"} {
		for {
			count := 0
			err := store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
				var done int
				if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM evidence_imports WHERE source=?", source).Scan(&done); err != nil {
					return err
				}
				if done != 0 {
					return nil
				}
				if source == "jobs" {
					rows, err := tx.QueryContext(ctx, `SELECT j.id,j.state,j.document,r.receipt,r.principal_id,r.epoch,r.request_id,r.created_at_ms FROM jobs j JOIN api_receipts r ON json_extract(r.receipt,'$.job_ref.id')=j.id WHERE j.transaction_id IS NULL AND NOT EXISTS(SELECT 1 FROM evidence_jobs e WHERE e.id=j.id) ORDER BY j.id LIMIT 128`)
					if err != nil {
						return err
					}
					items := []Job{}
					for rows.Next() {
						var j Job
						var blob, receiptBlob []byte
						var at int64
						if err = rows.Scan(&j.ID, &j.State, &blob, &receiptBlob, &j.Owner, &j.RequestEpoch, &j.RequestID, &at); err != nil {
							rows.Close()
							return err
						}
						var old struct {
							Operation string        `json:"operation"`
							Sequence  string        `json:"sequence"`
							Resource  *apitypes.Ref `json:"resource_ref"`
						}
						var receipt apitypes.Receipt
						if json.Unmarshal(blob, &old) != nil || json.Unmarshal(receiptBlob, &receipt) != nil {
							rows.Close()
							return apitypes.Fail(503, "LEGACY_JOB_INVALID")
						}
						j.Operation, j.Resource, j.Correlation, j.Sequence = old.Operation, old.Resource, receipt.CorrelationID, old.Sequence
						j.Capability = resolve(j.Operation)
						j.RequestDomain = "management"
						j.Created = time.UnixMilli(at).UTC()
						j.Updated = j.Created
						j.Handler = "none"
						j.Dispatch = "not-started"
						j.Business = "unknown"
						j.Commit = "not-sent"
						j.Applied = "not-applicable"
						j.Confirmation = "not-applicable"
						j.Reason = "legacy-evidence-imported"
						if j.Operation == "activateCertificate" {
							j.Handler = "tls-activation"
							j.Confirmation = "pending"
						}
						if Terminal(j.State) {
							j.Completed = &j.Updated
							if j.State == "succeeded" {
								j.Business = "success"
							} else {
								j.Business = "failure"
							}
						}
						items = append(items, j)
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						return err
					}
					count = len(items)
					for _, j := range items {
						if err = saveJob(ctx, tx, &j, true); err != nil {
							return err
						}
					}
				} else {
					query := `SELECT id,coalesce(principal_id,''),coalesce(credential_id,''),operation,coalesce(target_id,''),result,coalesce(request_id,''),'',created_at FROM auth_audit a WHERE NOT EXISTS(SELECT 1 FROM evidence_records e WHERE e.id=a.id) ORDER BY id LIMIT 128`
					if source == "events" {
						query = `SELECT id,'','',kind,'','recorded','',correlation_id,created_at FROM events a WHERE NOT EXISTS(SELECT 1 FROM evidence_records e WHERE e.id=a.id) ORDER BY id LIMIT 128`
					}
					if source == "audit" {
						query = `SELECT id,'','',kind,'','recorded',request_id,coalesce((SELECT correlation_id FROM request_receipts WHERE request_id=a.request_id),''),created_at FROM audit a WHERE NOT EXISTS(SELECT 1 FROM evidence_records e WHERE e.id=a.id) ORDER BY id LIMIT 128`
					}
					rows, err := tx.QueryContext(ctx, query)
					if err != nil {
						return err
					}
					items := []Record{}
					for rows.Next() {
						var r Record
						var target string
						var at int64
						if err = rows.Scan(&r.ID, &r.Actor, &r.Credential, &r.Operation, &target, &r.Result, &r.RequestID, &r.Correlation, &at); err != nil {
							rows.Close()
							return err
						}
						r.Collection = "audit"
						r.Origin = "Unknown"
						if source == "auth_audit" {
							r.Origin = "Manager"
						}
						if source == "events" {
							r.Collection = "event"
						}
						if !optionalID(r.Correlation) {
							r.Correlation = ""
						}
						if !optionalID(r.Actor) {
							r.Actor = ""
						}
						if !optionalID(r.Credential) {
							r.Credential = ""
						}
						if !optionalRequest(r.RequestID) {
							r.RequestID = ""
						}
						if apitypes.ManagementID(target) {
							r.Object = &apitypes.Ref{Kind: "legacy-resource", ID: target}
						}
						r.Created = time.Unix(at, 0).UTC()
						r.Reason = "legacy-source-coverage-limited"
						items = append(items, r)
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						return err
					}
					count = len(items)
					for _, r := range items {
						// A legacy auth row has no correlation column. Recover only a
						// uniquely matching receipt; ambiguous identity remains unknown.
						if r.RequestID != "" && r.Actor != "" && r.Correlation == "" {
							var count int
							var blob []byte
							var epoch string
							if err = tx.QueryRowContext(ctx, "SELECT count(*),coalesce(min(receipt),'{}'),coalesce(min(epoch),'') FROM api_receipts WHERE principal_id=? AND request_id=? AND domain='management'", r.Actor, r.RequestID).Scan(&count, &blob, &epoch); err != nil {
								return err
							}
							if count == 1 {
								var receipt apitypes.Receipt
								if json.Unmarshal(blob, &receipt) != nil {
									return apitypes.Fail(503, "LEGACY_RECEIPT_INVALID")
								}
								r.Correlation, r.RequestDomain, r.RequestEpoch = receipt.CorrelationID, "management", epoch
								if receipt.Job != nil {
									r.Job = receipt.Job.ID
								}
							}
						}
						if _, err = Append(ctx, tx, r); err != nil {
							return err
						}
					}
				}
				if count == 0 {
					_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO evidence_imports VALUES(?)", source)
					return err
				}
				return nil
			})
			if err != nil {
				return err
			}
			if count == 0 {
				break
			}
		}
	}
	return nil
}
