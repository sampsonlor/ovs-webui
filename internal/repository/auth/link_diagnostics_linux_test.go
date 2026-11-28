//go:build linux

package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type linkDiagnosticFixture struct {
	started    chan struct{}
	release    chan struct{}
	admissions atomic.Int32
	calls      atomic.Int32
}

func (*linkDiagnosticFixture) Read(context.Context, string, map[string]string, url.Values, authn.Claims) (any, error) {
	return nil, apitypes.Fail(503, "NOT_AVAILABLE_IN_FIXTURE")
}
func (f *linkDiagnosticFixture) CheckLinkLACPTarget(context.Context, diagnostics.LinkLACPRequest, authn.Claims) error {
	f.admissions.Add(1)
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

func seedLinkLACPPort(t *testing.T, r *Repository, target apitypes.Ref, generation, ovsUUID string) {
	t.Helper()
	if err := r.store.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO generations(id,evidence,created_at) VALUES(?,?,?)", generation, []byte("{}"), r.now().Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO inventory_state(singleton,generation,state,reason,evidence) VALUES(1,?,'confirmed','synthetic-diagnostic',?)", generation, []byte("{}")); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO identities(management_id,generation,table_name,ovs_uuid,state) VALUES(?,?,'Port',?,'active')", target.ID, generation, ovsUUID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func loadLinkLACPJob(t *testing.T, r *Repository, id string) evidence.Job {
	t.Helper()
	var job evidence.Job
	if err := r.store.Read(t.Context(), func(ctx context.Context, q *sql.Conn) error {
		var err error
		job, err = evidence.LoadJob(ctx, q, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return job
}

func awaitLinkLACPTerminal(t *testing.T, r *Repository, id, state string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; {
		job := loadLinkLACPJob(t, r, id)
		if job.State == state {
			return
		}
		if evidence.Terminal(job.State) || !time.Now().Before(deadline) {
			t.Fatalf("expected %s and got %+v", state, job)
		}
		time.Sleep(15 * time.Millisecond)
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
			seedLinkLACPPort(t, r, target, repository.NewID(), repository.NewID())
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
			awaitLinkLACPTerminal(t, r, id, want)
			out, err := r.ReadAuth(t.Context(), admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/diagnostics/link-lacp/" + id})
			if cancel {
				wantCode(t, err, "DIAGNOSTIC_RESULT_UNAVAILABLE")
			} else if err != nil || out.Status != 200 {
				t.Fatalf("completed evidence unavailable: %s %v", out.Body, err)
			}
		})
	}
}

type linkLACPCrashInput struct {
	Options sqlite.Options
	Grant   string
	Command authn.Command
}

func TestLinkLACPDiagnosticManagerReopenRetiresJobWithoutReplay(t *testing.T) {
	r, options := fixture(t)
	admin := login(t, r, "admin", false)
	target := apitypes.Ref{Kind: "port", ID: repository.NewID()}
	seedLinkLACPPort(t, r, target, repository.NewID(), repository.NewID())
	command := request(t, r, "POST", "/diagnostics/link-lacp", map[string]any{
		"target_ref": target, "sample_seconds": 5, "port_limit": 1,
	}, "")
	input, err := json.Marshal(linkLACPCrashInput{Options: options, Grant: admin.Grant, Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinkLACPDiagnosticCrashFixtureChild$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "OVS_LINK_DIAGNOSTIC_CRASH_FIXTURE=1")
	child.Stdin = bytes.NewReader(input)
	output, err := child.CombinedOutput()
	var exited *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exited) || exited.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("isolated diagnostic worker did not crash after admission: %v\n%s", err, output)
	}

	r = openFixture(t, options)
	source := &linkDiagnosticFixture{started: make(chan struct{}), release: make(chan struct{})}
	r.WithInventory(source).WithDiagnosticsContext(t.Context())
	original := read(t, r, admin, "/requests/"+command.RequestID+"?domain=management&epoch="+command.Epoch)
	ref, ok := original["job_ref"].(map[string]any)
	if !ok || ref["kind"] != "job" || original["state"] != "accepted" {
		t.Fatalf("original accepted Job receipt is unavailable: %+v", original)
	}
	id, _ := ref["id"].(string)
	before := loadLinkLACPJob(t, r, id)
	if before.ID != id || before.State != "running" || before.Dispatch != "sent" || before.Owner != admin.Claims.PrincipalID || before.RequestID != command.RequestID {
		t.Fatalf("restart fixture did not preserve the in-flight Job: %+v", before)
	}
	if err = r.InitializeEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := loadLinkLACPJob(t, r, id)
	if after.State != "failed" || after.Business != "failure" || after.Reason != "diagnostic-interrupted-by-manager-restart" || after.Completed == nil {
		t.Fatalf("restart did not retire the interrupted Job explicitly: %+v", after)
	}
	replay, err := r.ExecuteAuth(t.Context(), admin.Grant, command)
	if err != nil || !replay.Replayed || replay.Receipt.Job == nil || replay.Receipt.Job.ID != id ||
		replay.Receipt.CorrelationID != before.Correlation || source.admissions.Load() != 0 || source.calls.Load() != 0 {
		t.Fatalf("retry did not return the original Job without provider replay: %+v, %v", replay, err)
	}
	if err = r.InitializeEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if current := loadLinkLACPJob(t, r, id); current.Sequence != after.Sequence || current.Reason != after.Reason {
		t.Fatalf("repeated startup or request changed the terminal Job: %+v", current)
	}
	_, err = r.ReadAuth(t.Context(), admin.Grant, authn.Query{Method: "GET", URI: "/api/v1/diagnostics/link-lacp/" + id})
	wantCode(t, err, "DIAGNOSTIC_RESULT_UNAVAILABLE")
}

func TestLinkLACPDiagnosticCrashFixtureChild(t *testing.T) {
	if os.Getenv("OVS_LINK_DIAGNOSTIC_CRASH_FIXTURE") != "1" {
		t.Skip("separate process for interrupted diagnostic recovery")
	}
	var input linkLACPCrashInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	r := openFixture(t, input.Options)
	source := &linkDiagnosticFixture{started: make(chan struct{}), release: make(chan struct{})}
	r.WithInventory(source).WithDiagnosticsContext(t.Context())
	ack, err := r.ExecuteAuth(t.Context(), input.Grant, input.Command)
	if err != nil || ack.Receipt.Job == nil || ack.Replayed {
		t.Fatalf("new diagnostic admission failed: %+v, %v", ack, err)
	}
	select {
	case <-source.started:
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic never reached its controlled observer")
	}
	if job := loadLinkLACPJob(t, r, ack.Receipt.Job.ID); job.State != "running" || job.Dispatch != "sent" {
		t.Fatalf("diagnostic had not durably started: %+v", job)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	t.Fatal("fixture unexpectedly survived SIGKILL")
}

type linkLACPSafetyInventory struct {
	*linkDiagnosticFixture
	candidate *planProvider
}

func (f *linkLACPSafetyInventory) CandidateSnapshot(ctx context.Context, bindings []plan.Binding) (plan.Snapshot, error) {
	return f.candidate.CandidateSnapshot(ctx, bindings)
}

func TestLinkLACPDiagnosticPreservesCandidateAndSafeApplyOwner(t *testing.T) {
	r, _ := fixture(t)
	owner := login(t, r, "admin", true)
	createUser(t, r, owner, "diagnostic-reader", roleID(t, r, owner, "Reader"))
	reader := login(t, r, "diagnostic-reader", false)
	in := safeTestRequest(t, r, owner)
	binding := in.Envelope.Candidate.Intents[0].Object
	target := apitypes.Ref{Kind: "port", ID: binding.ManagementID}
	seedLinkLACPPort(t, r, target, binding.Generation, binding.OVSUUID)
	source := &linkDiagnosticFixture{started: make(chan struct{}), release: make(chan struct{})}
	r.WithInventory(&linkLACPSafetyInventory{linkDiagnosticFixture: source, candidate: r.inventory.(*planProvider)}).
		WithDiagnosticsContext(t.Context())
	clock := tlscontrol.Now()
	e := safeConfigure(t, r, &safeTestProvider{}, &safeTestProbe{}, &clock)
	transactionID := safeAdmit(t, r, e, owner, in)

	type configAuthority struct {
		candidateOwner, candidateID, candidateRevision, candidateEpoch, candidateDigest string
		transactionOwner, safetyOwner, safetyState, journalState                        string
		candidateSequence, journalSequence, protections                                 int
	}
	snapshot := func() (value configAuthority) {
		t.Helper()
		if err := r.store.Read(t.Context(), func(ctx context.Context, q *sql.Conn) error {
			return q.QueryRowContext(ctx, `SELECT c.owner_id,c.candidate_id,c.revision,c.workspace_epoch,c.envelope_hash,
				f.owner_id,s.owner_id,s.state,j.state,c.sequence,j.sequence,
				(SELECT count(*) FROM operation_protections WHERE transaction_id=f.id AND resource_id=?)
				FROM field_executions f JOIN safe_applies s ON s.id=f.id JOIN transaction_journal j ON j.transaction_id=f.id
				JOIN candidate_witnesses c ON c.candidate_id=? WHERE f.id=?`,
				binding.ManagementID, in.Envelope.Candidate.ID, transactionID).Scan(
				&value.candidateOwner, &value.candidateID, &value.candidateRevision, &value.candidateEpoch, &value.candidateDigest,
				&value.transactionOwner, &value.safetyOwner, &value.safetyState, &value.journalState,
				&value.candidateSequence, &value.journalSequence, &value.protections)
		}); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	if before.candidateOwner != owner.Claims.PrincipalID || before.candidateID != in.Envelope.Candidate.ID ||
		before.candidateRevision != in.Envelope.Candidate.Revision || before.transactionOwner != before.candidateOwner ||
		before.safetyOwner != before.transactionOwner || safety.Terminal(before.safetyState) || before.protections == 0 ||
		owner.Claims.PrincipalID == reader.Claims.PrincipalID {
		t.Fatalf("fixture requires an existing owned Candidate and active protected transaction: %+v", before)
	}
	diagnostic := execute(t, r, reader, "POST", "/diagnostics/link-lacp", map[string]any{
		"target_ref": target, "sample_seconds": 5, "port_limit": 1,
	}, "")
	if diagnostic.Receipt.Job == nil {
		t.Fatal("diagnostic did not acknowledge a Job")
	}
	close(source.release)
	id := diagnostic.Receipt.Job.ID
	awaitLinkLACPTerminal(t, r, id, "succeeded")
	job := loadLinkLACPJob(t, r, id)
	if job.Owner != reader.Claims.PrincipalID || job.Transaction != "" || source.calls.Load() != 1 {
		t.Fatalf("diagnostic Job did not retain the separate reader and operation: %+v", job)
	}
	read(t, r, reader, "/diagnostics/link-lacp/"+id)
	if after := snapshot(); after != before {
		t.Fatalf("diagnostic changed configuration authority: before=%+v after=%+v", before, after)
	}
}
