//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

func TestSafeApplyWatchdogProofAndLegacyConfirmationSettleFieldJournal(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "watchdog-before-field-recovery"
		if legacy {
			name = "older-unpersisted-confirmation-proof"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := fixture(t)
			g := login(t, r, "admin", false)
			in := safeTestRequest(t, r, g)
			p, probe := &safeTestProvider{}, &safeTestProbe{}
			clock := tlscontrol.Now()
			e := safeConfigure(t, r, p, probe, &clock)
			id := safeAdmit(t, r, e, g, in)
			p.applied.Store(true)
			// Deliberately omit field Reconcile. The watchdog gets Applied first.
			safeTick(t, e)
			record, err := e.Read(testContext, id)
			if err != nil || record.Outcome.Applied != "applied" || record.State != "succeeded" || safeTestState(t, r, id).State != "awaiting-confirmation" {
				t.Fatal("watchdog proof was not durable", record, err)
			}
			if legacy {
				if err = r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
					var body []byte
					if err := tx.QueryRowContext(ctx, "SELECT document FROM field_executions WHERE id=?", id).Scan(&body); err != nil {
						return err
					}
					var doc map[string]json.RawMessage
					if err := json.Unmarshal(body, &doc); err != nil {
						return err
					}
					doc["state"], _ = json.Marshal("applying")
					record.Outcome.Applied = "pending"
					doc["outcome"], _ = json.Marshal(record.Outcome)
					body, _ = json.Marshal(doc)
					_, err := tx.ExecContext(ctx, "UPDATE field_executions SET state='applying',document=? WHERE id=?", body, id)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			cmd := request(t, r, "POST", "/transactions/"+id+"/decisions", map[string]any{"decision": "confirm", "expected_sequence": record.Sequence}, "")
			if _, err = r.ExecuteAuth(testContext, g.Grant, cmd); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(testContext); err != nil {
				t.Fatal("settled Job reopened", err)
			}
			record, err = e.Read(testContext, id)
			if err != nil || record.Outcome.Applied != "applied" || record.State != "succeeded" {
				t.Fatal("confirmation lost proof", record, err)
			}
			var job evidence.Job
			if err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
				job, err = evidence.LoadJob(ctx, q, record.JobID)
				return err
			}); err != nil || job.State != "succeeded" || p.sends.Load() != 1 {
				t.Fatal(job, err, p.sends.Load())
			}
		})
	}
}

func TestSafeApplySettledRollbackKeepsOriginalUncertaintyWithoutReopeningJob(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	in := safeTestRequest(t, r, g)
	p, probe := &safeTestProvider{}, &safeTestProbe{}
	p.unknown.Store(true)
	clock := tlscontrol.Now()
	e := safeConfigure(t, r, p, probe, &clock)
	id := safeAdmit(t, r, e, g, in)
	p.unknown.Store(false)
	record, err := e.Read(testContext, id)
	if err != nil {
		t.Fatal(err)
	}
	cmd := request(t, r, "POST", "/transactions/"+id+"/decisions", map[string]any{"decision": "rollback", "expected_sequence": record.Sequence}, "")
	if _, err = r.ExecuteAuth(testContext, g.Grant, cmd); err != nil {
		t.Fatal(err)
	}
	safeTick(t, e)
	safeTick(t, e)
	if s := safeTestState(t, r, id); s.State != "rolled-back" || s.Outcome.Applied != "applied" {
		t.Fatal(s)
	}
	before, err := e.Read(testContext, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(testContext); err != nil {
		t.Fatal(err)
	}
	if err = e.Reconcile(testContext, id); err != nil {
		t.Fatal("late field observation reopened terminal Job", err)
	}
	after, err := e.Read(testContext, id)
	if err != nil || after.Sequence != before.Sequence || after.Outcome.Target != nil || after.Outcome.Applied == "applied" || p.sends.Load() != 2 {
		t.Fatal("original uncertainty was replaced by rollback proof", after, err, p.sends.Load())
	}
}
