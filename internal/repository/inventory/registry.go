// Package inventory persists identity and lifecycle evidence, never live values.
package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	domain "github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	evidenceRepo "github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

type Registry struct{ store *sqlite.Store }

func New(s *sqlite.Store) (*Registry, error) {
	if s.Kind() != repository.Manager {
		return nil, repository.ErrInvalid
	}
	return &Registry{store: s}, nil
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func previous(ctx context.Context, q rowQuery) (domain.Decision, bool, error) {
	var d domain.Decision
	var blob []byte
	var restored bool
	err := q.QueryRowContext(ctx, "SELECT generation,state,reason,evidence,pending_digest,restore_required FROM inventory_state WHERE singleton=1").Scan(&d.Generation, &d.State, &d.Reason, &blob, &d.PendingDigest, &restored)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	if json.Unmarshal(blob, &d.Evidence) != nil {
		return d, false, repository.ErrInvalid
	}
	return d, restored, nil
}
func (r *Registry) Previous(ctx context.Context) (d domain.Decision, err error) {
	err = r.store.Read(ctx, func(ctx context.Context, c *sql.Conn) error { var e error; d, _, e = previous(ctx, c); return e })
	return
}

// Classify is deliberately conservative. A daemon PID, schema version, system-id
// or row _version alone never changes a generation or proves continuity.
func Classify(old domain.Decision, e domain.Evidence, restored bool) (string, string) {
	if old.Generation == "" {
		return "new", "initial-discovery"
	}
	if restored {
		return "reconciliation-required", "management-restore"
	}
	if old.State == "reconciliation-required" {
		return old.State, old.Reason
	}
	p := old.Evidence
	if p.Endpoint != e.Endpoint || p.Database != e.Database {
		return "reconciliation-required", "endpoint-changed"
	}
	if p.Root != "" && e.Root != "" && p.Root != e.Root {
		overlap := false
		for _, a := range p.Anchors {
			if slices.Contains(e.Anchors, a) {
				overlap = true
				break
			}
		}
		if !overlap {
			return "new", "native-identity-break"
		}
		return "reconciliation-required", "root-anchor-conflict"
	}
	if p.Schema != e.Schema {
		return "reconciliation-required", "schema-changed"
	}
	if p.Root != "" && e.Root == "" {
		return "reconciliation-required", "root-disappeared"
	}
	if p.Boot != e.Boot {
		return "reconciliation-required", "host-restarted"
	}
	gap := e.ObservedAt.Sub(p.ObservedAt)
	if gap < 0 || gap > domain.ContinuityGap {
		return "reconciliation-required", "observation-gap"
	}
	if !e.File.Available || !e.File.ServerHasFile || !p.File.Available {
		return "reconciliation-required", "file-evidence-unavailable"
	}
	if p.File.Device != e.File.Device || p.File.Inode != e.File.Inode || !e.File.PriorMatches {
		return "reconciliation-required", "database-file-replaced-or-rewound"
	}
	if p.Root == e.Root || (p.Root == "" && e.Continuous) {
		if p.Peer != e.Peer {
			return "continued", "process-restarted-database-continued"
		}
		if !e.Continuous {
			return "continued", "database-reconnected"
		}
		return "continued", "monitor-continuity"
	}
	return "reconciliation-required", "identity-evidence-insufficient"
}
func (r *Registry) Reconcile(ctx context.Context, o domain.Observation) (domain.Decision, error) {
	return r.reconcile(ctx, o, "", "")
}
func (r *Registry) Accept(ctx context.Context, o domain.Observation, digest, reason string) (domain.Decision, error) {
	if len(reason) < 8 || len(reason) > 512 || strings.ContainsAny(reason, "\r\n") || len(digest) != 64 {
		return domain.Decision{}, apitypes.Fail(422, "INVALID_RECONCILIATION")
	}
	return r.reconcile(ctx, o, digest, reason)
}
func (r *Registry) reconcile(ctx context.Context, o domain.Observation, accepted, reason string) (domain.Decision, error) {
	var out domain.Decision
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		old, restored, err := previous(ctx, tx)
		if err != nil {
			return err
		}
		state, why := Classify(old, o.Evidence, restored)
		if accepted != "" {
			if old.State != "reconciliation-required" || old.PendingDigest != accepted || domain.EvidenceDigest(o.Evidence) != accepted {
				return apitypes.Fail(409, "RECONCILIATION_EVIDENCE_CHANGED")
			}
			state, why = "new", "explicit-reconciliation"
		}
		out = domain.Decision{Generation: old.Generation, State: "confirmed", Reason: why, Evidence: o.Evidence, Bindings: map[string]domain.Binding{}}
		changed := state == "new"
		if changed {
			var generations int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM generations").Scan(&generations); err != nil {
				return err
			}
			if generations >= 4096 {
				return apitypes.Fail(503, "GENERATION_REGISTRY_FULL")
			}
			out.Generation = repository.NewID()
			blob, _ := json.Marshal(o.Evidence)
			if _, err = tx.ExecContext(ctx, "INSERT INTO generations VALUES(?,?,?)", out.Generation, blob, time.Now().Unix()); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE identities SET state='tombstone' WHERE state='active'"); err != nil {
				return err
			}
		}
		if state == "reconciliation-required" {
			out.State = state
			out.PendingDigest = domain.EvidenceDigest(o.Evidence)
		}
		// Uncertain evidence never rebinds an old identity. A fresh monitor can still
		// expose schema and status, but object pages remain unavailable until review.
		if out.State == "confirmed" {
			rows, err := tx.QueryContext(ctx, "SELECT management_id,table_name,ovs_uuid,state FROM identities WHERE generation=?", out.Generation)
			if err != nil {
				return err
			}
			known := map[string]domain.Binding{}
			for rows.Next() {
				var b domain.Binding
				if err = rows.Scan(&b.ManagementID, &b.Table, &b.UUID, &b.State); err != nil {
					rows.Close()
					return err
				}
				known[domain.Key(b.Table, b.UUID)] = b
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			count := 0
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM identities").Scan(&count); err != nil {
				return err
			}
			for table, objects := range o.Rows {
				if table == "Open_vSwitch" {
					continue
				}
				for uuid := range objects {
					key := domain.Key(table, uuid)
					b, ok := known[key]
					if ok && b.State != "active" {
						return apitypes.Fail(503, "TOMBSTONED_UUID_REAPPEARED")
					}
					if !ok {
						if count >= domain.MaxIdentities {
							return apitypes.Fail(503, "IDENTITY_REGISTRY_FULL")
						}
						count++
						b = domain.Binding{ManagementID: repository.NewID(), Table: table, UUID: uuid, State: "active"}
						if _, err = tx.ExecContext(ctx, "INSERT INTO identities VALUES(?,?,?,?,'active')", b.ManagementID, out.Generation, table, uuid); err != nil {
							return err
						}
					}
					out.Bindings[key] = b
				}
			}
			for key, b := range known {
				if b.State == "active" {
					if _, ok := out.Bindings[key]; !ok {
						if _, err = tx.ExecContext(ctx, "UPDATE identities SET state='tombstone' WHERE management_id=?", b.ManagementID); err != nil {
							return err
						}
					}
				}
			}
		}
		// Bound the small decision history. Identity tombstones are never pruned or
		// recycled; reaching their explicit cap fails closed instead.
		if changed || old.State != out.State || old.Reason != out.Reason || accepted != "" {
			if _, err = tx.ExecContext(ctx, "INSERT INTO inventory_reconciliations VALUES(?,?,?,?,?,?,?)", repository.NewID(), out.Generation, old.Generation, out.Reason, domain.EvidenceDigest(o.Evidence), reason, time.Now().Unix()); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM inventory_reconciliations WHERE id IN (SELECT id FROM inventory_reconciliations ORDER BY created_at DESC,id DESC LIMIT -1 OFFSET 256)"); err != nil {
				return err
			}
			record := evidenceRepo.Record{Collection: "audit", Origin: "Manager", Operation: "inventory-" + out.Reason, Object: &apitypes.Ref{Kind: "generation", ID: out.Generation}, Correlation: out.Generation, Result: "recorded"}
			auditID, e := evidenceRepo.Append(ctx, tx, record)
			if e != nil {
				return e
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO auth_audit VALUES(?,NULL,NULL,?,?,'recorded',NULL,?)", auditID, "inventory-"+out.Reason, out.Generation, time.Now().Unix()); err != nil {
				return err
			}
			record.Collection = "event"
			eventID, e := evidenceRepo.Append(ctx, tx, record)
			if e != nil {
				return e
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO events VALUES(?,?,?,?)", eventID, out.Generation, "inventory-"+out.Reason, time.Now().Unix()); err != nil {
				return err
			}
		}
		var last string
		err = tx.QueryRowContext(ctx, "SELECT digest FROM evidence_inventory_snapshot WHERE singleton=1").Scan(&last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		digest := domain.Digest(o.Rows)
		if last != "" && last != digest {
			if _, err = evidenceRepo.Append(ctx, tx, evidenceRepo.Record{Collection: "event", Origin: "External", Operation: "ovsdb-inventory-changed", Object: &apitypes.Ref{Kind: "generation", ID: out.Generation}, Correlation: out.Generation, Result: "observed"}); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO evidence_inventory_snapshot VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET digest=excluded.digest", digest); err != nil {
			return err
		}
		blob, _ := json.Marshal(out.Evidence)
		_, err = tx.ExecContext(ctx, "INSERT INTO inventory_state VALUES(1,?,?,?,?,?,0) ON CONFLICT(singleton) DO UPDATE SET generation=excluded.generation,state=excluded.state,reason=excluded.reason,evidence=excluded.evidence,pending_digest=excluded.pending_digest,restore_required=0", out.Generation, out.State, out.Reason, blob, out.PendingDigest)
		return err
	})
	if err != nil {
		return domain.Decision{}, err
	}
	return out, nil
}
