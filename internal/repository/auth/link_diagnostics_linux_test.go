//go:build linux

package auth

import (
	"context"
	"database/sql"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
)

type linkDiagnosticFixture struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (*linkDiagnosticFixture) Read(context.Context, string, map[string]string, url.Values, authn.Claims) (any, error) {
	return nil, apitypes.Fail(503, "NOT_AVAILABLE_IN_FIXTURE")
}
func (*linkDiagnosticFixture) CheckLinkLACPTarget(context.Context, diagnostics.LinkLACPRequest, authn.Claims) error {
	return nil
}
func (f *linkDiagnosticFixture) ObserveLinkLACP(ctx context.Context, req diagnostics.LinkLACPRequest, _ authn.Claims) (diagnostics.LinkLACPSample, error) {
	f.calls.Add(1)
	close(f.started)
	select {
	case <-ctx.Done():
		return diagnostics.LinkLACPSample{}, ctx.Err()
	case <-f.release:
		return diagnostics.LinkLACPSample{
			Target: req.Target, ObservedAt: time.Now().UTC(),
			Ports: []diagnostics.LinkLACPPort{{
				Ref: req.Target, LACP: "off",
				Members: []diagnostics.LinkLACPMember{{Ref: apitypes.Ref{Kind: "interface", ID: repository.NewID()}, Link: "up", LACPCurrent: "not-applicable"}},
			}},
		}, nil
	}
}

func TestLinkLACPDiagnosticUsesDurableOwnedJobAndCurrentCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		scenario := "completed"
		if cancel {
			scenario = "cancelled"
		}
		t.Run(scenario, func(t *testing.T) {
			r, _ := fixture(t)
			source := &linkDiagnosticFixture{started: make(chan struct{}), release: make(chan struct{})}
			r.WithInventory(source).WithDiagnosticsContext(t.Context())
			admin := login(t, r, "admin", false)
			target := apitypes.Ref{Kind: "port", ID: repository.NewID()}
			if err := r.store.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
				gen := repository.NewID()
				if _, err := tx.ExecContext(ctx, "INSERT INTO inventory_state(singleton,generation,state,reason,evidence) VALUES(1,?,'confirmed','synthetic-diagnostic',?)", gen, []byte("{}")); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "INSERT INTO identities(management_id,generation,table_name,ovs_uuid,state) VALUES(?,?,'Port',?,'active')", target.ID, gen, repository.NewID())
				return err
			}); err != nil {
				t.Fatal(err)
			}
			input := request(t, r, "POST", "/diagnostics/link-lacp", map[string]any{
				"target_ref": target, "sample_seconds": 5, "port_limit": 1,
			}, "")
			initial, err := r.ExecuteAuth(t.Context(), admin.Grant, input)
			if err != nil || initial.Receipt.Job == nil {
				t.Fatalf("no durable job: %+v, %v", initial, err)
			}
			id := initial.Receipt.Job.ID
			select {
			case <-source.started:
			case <-time.After(3 * time.Second):
				t.Fatal("worker never received the typed request")
			}
			again, err := r.ExecuteAuth(t.Context(), admin.Grant, input)
			if err != nil || again.Receipt.Job == nil || again.Receipt.Job.ID != id || source.calls.Load() != 1 {
				t.Fatalf("idempotent replay changed job: %+v, %v", again, err)
			}
			competing := request(t, r, "POST", "/diagnostics/link-lacp", map[string]any{
				"target_ref": target, "sample_seconds": 5, "port_limit": 1,
			}, "")
			_, err = r.ExecuteAuth(t.Context(), admin.Grant, competing)
			wantCode(t, err, "DIAGNOSTIC_CAPACITY_REACHED")
			want := "succeeded"
			if cancel {
				req := request(t, r, "POST", "/jobs/"+id+"/cancellations", map[string]any{}, "")
				if _, err := r.ExecuteAuth(t.Context(), admin.Grant, req); err != nil {
					t.Fatal(err)
				}
				want = "cancelled"
			} else {
				close(source.release)
			}
			until := time.Now().Add(3 * time.Second)
			var job evidence.Job
			for !evidence.Terminal(job.State) && time.Now().Before(until) {
				if err := r.store.Read(t.Context(), func(ctx context.Context, q *sql.Conn) error {
					var err error
					job, err = evidence.LoadJob(ctx, q, id)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				time.Sleep(15 * time.Millisecond)
			}
			if job.State != want {
				t.Fatalf("expected %s and got %+v", want, job)
			}
			out, err := r.ReadAuth(t.Context(), admin.Grant, authn.Query{Method: "GET", URI: "/diagnostics/link-lacp/" + id})
			if cancel {
				wantCode(t, err, "DIAGNOSTIC_RESULT_UNAVAILABLE")
			} else if err != nil || out.Status != 200 {
				t.Fatalf("completed evidence unavailable: %s %v", out.Body, err)
			}
		})
	}
}
