//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type planProvider struct {
	snapshot plan.Snapshot
	failure  error
}

func (p *planProvider) Read(context.Context, string, map[string]string, url.Values, authn.Claims) (any, error) {
	return nil, apitypes.Fail(503, "UNUSED_READ")
}
func (p *planProvider) CandidateSnapshot(context.Context, []plan.Binding) (plan.Snapshot, error) {
	return p.snapshot, p.failure
}
func planPointer[T any](v T) *T { return &v }
func planRequestID() string     { id, _ := uuid.NewV7(); return id.String() }
func planSetup(t *testing.T, r *Repository, login authn.LoginResult) (plan.Envelope, *planProvider, plan.Intent) {
	t.Helper()
	b := plan.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: repository.NewID()}
	p := &planProvider{snapshot: plan.Snapshot{Generation: b.Generation, Revision: "native-1", Schema: "real-schema-digest", Policy: "reviewed-authority", Ports: map[string]plan.Port{b.ManagementID: {Binding: b, VLAN: plan.VLAN{Mode: planPointer("access"), Tag: planPointer(10), Trunks: []int{}, CVLANs: []int{}}, Known: true, SchemaSupported: true, Modes: []string{"access", "trunk", "native-tagged", "native-untagged"}, Dependency: "member-dependency", Authority: "local-managed"}}}}
	r.WithInventory(p)
	e := plan.Envelope{Owner: login.Claims.PrincipalID, Epoch: repository.NewID(), Candidate: plan.Candidate{ID: repository.NewID(), Revision: repository.NewID(), State: "empty", Intents: []plan.StoredIntent{}}}
	i := plan.Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: b, Value: plan.VLAN{Mode: planPointer("native-untagged"), Tag: planPointer(20), Trunks: []int{}, CVLANs: []int{}}}
	return e, p, i
}
func preparePlan(t *testing.T, r *Repository, g string, e plan.Envelope, i plan.Intent) plan.Envelope {
	t.Helper()
	id := planRequestID()
	b, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []plan.Intent{i}})
	out, err := r.PrepareCandidate(testContext, g, plan.PrepareRequest{Envelope: e, Command: authn.Command{Method: "PATCH", URI: "/api/v1/candidate", Epoch: e.Epoch, RequestID: id, Precondition: `"` + e.Candidate.Revision + `"`, Payload: b}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func validationRequest(e plan.Envelope, epoch string) plan.ValidateRequest {
	id := planRequestID()
	b, _ := json.Marshal(map[string]any{"request_id": id, "candidate_id": e.Candidate.ID, "candidate_revision": e.Candidate.Revision})
	return plan.ValidateRequest{Envelope: e, Command: authn.Command{Method: "POST", URI: "/api/v1/validations", Epoch: epoch, RequestID: id, Payload: b}}
}
func readPlanValidation(t *testing.T, r *Repository, g string, e plan.Envelope, id string) plan.Validation {
	t.Helper()
	out, err := r.ReadCandidate(testContext, g, plan.ReadRequest{Envelope: e, ValidationID: id})
	if err != nil {
		t.Fatal(err)
	}
	var v plan.Validation
	if json.Unmarshal(out.Body, &v) != nil {
		t.Fatal("bad validation response")
	}
	op, _, _ := r.contract.Match("GET", "/api/v1/validations/"+id)
	if err = op.ValidateResponse(out.Status, out.Body); err != nil {
		t.Fatal(err, string(out.Body))
	}
	return v
}
func TestFormalValidationReceiptRestartAndIndependentBusinessOutcome(t *testing.T) {
	r, options := fixture(t)
	g := login(t, r, "admin", false)
	e, p, i := planSetup(t, r, g)
	e = preparePlan(t, r, g.Grant, e, i)
	in := validationRequest(e, g.Claims.RequestEpoch)
	out, err := r.ValidateCandidate(testContext, g.Grant, in)
	if err != nil {
		t.Fatal(err)
	}
	v := readPlanValidation(t, r, g.Grant, e, out.Receipt.Resource.ID)
	if out.Status != 202 || v.State != "passed" || !v.Usable || v.ExecutionReady || len(v.Diff) != 4 || v.ChangeSetID == "" {
		t.Fatal(v, out)
	}
	if err = r.store.Close(); err != nil {
		t.Fatal(err)
	}
	r = openFixture(t, options)
	r.WithInventory(p)
	v = readPlanValidation(t, r, g.Grant, e, v.ID)
	if !v.Usable {
		t.Fatal("restart lost persisted validation", v)
	}
	p.failure = apitypes.Fail(503, "PROVIDER_UNAVAILABLE")
	replay, err := r.ValidateCandidate(testContext, g.Grant, in)
	if err != nil || !replay.Replayed || *replay.Receipt.Resource != *out.Receipt.Resource || replay.Receipt.Job.ID != out.Receipt.Job.ID {
		t.Fatal("lost reply changed receipt", replay, err)
	}
	if readPlanValidation(t, r, g.Grant, e, v.ID).Usable {
		t.Fatal("provider loss kept result usable")
	}
	p.failure = nil
	port := p.snapshot.Ports[i.Object.ManagementID]
	port.Authority = "unknown"
	p.snapshot.Ports[i.Object.ManagementID] = port
	blocked, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	v = readPlanValidation(t, r, g.Grant, e, blocked.Receipt.Resource.ID)
	if v.State != "blocked" || v.Usable {
		t.Fatal(v)
	}
	job, err := r.ReadAuth(testContext, g.Grant, authn.Query{Method: "GET", URI: "/api/v1/jobs/" + blocked.Receipt.Job.ID})
	if err != nil {
		t.Fatal(err)
	}
	var j map[string]any
	_ = json.Unmarshal(job.Body, &j)
	if j["state"] != "succeeded" || j["applied_outcome"] != "not-applicable" {
		t.Fatal("job success was confused with validation success", j)
	}
	if *p.snapshot.Ports[i.Object.ManagementID].VLAN.Tag != 10 {
		t.Fatal("validation modified provider")
	}
}
func TestFormalValidationInvalidatesDraftPolicyCredentialExpiryAndGeneration(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, p, i := planSetup(t, r, g)
	e = preparePlan(t, r, g.Grant, e, i)
	out, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Receipt.Resource.ID
	// Equality of an unrelated global revision is not a dependency.
	p.snapshot.Revision = "unrelated-change"
	if !readPlanValidation(t, r, g.Grant, e, id).Usable {
		t.Fatal("global revision gate")
	}
	i.Value.Tag = planPointer(30)
	newer := preparePlan(t, r, g.Grant, e, i)
	if readPlanValidation(t, r, g.Grant, newer, id).Usable {
		t.Fatal("changed draft reused old validation")
	}
	if _, err = r.ReadCandidate(testContext, g.Grant, plan.ReadRequest{Envelope: e}); err == nil {
		t.Fatal("old workspace witness rolled back")
	}
	out, err = r.ValidateCandidate(testContext, g.Grant, validationRequest(newer, g.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	id = out.Receipt.Resource.ID
	p.snapshot.Generation = repository.NewID()
	if readPlanValidation(t, r, g.Grant, newer, id).Usable {
		t.Fatal("cross-generation validation survived")
	}
	p.snapshot.Generation = i.Object.Generation
	g2 := login(t, r, "admin", false)
	if readPlanValidation(t, r, g2.Grant, newer, id).Usable {
		t.Fatal("new grant adopted old validation")
	}
	err = r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE auth_state SET revision=?", repository.NewID())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if readPlanValidation(t, r, g.Grant, newer, id).Usable {
		t.Fatal("policy change ignored")
	}
	if err = r.RevokeAuth(testContext, g.Grant); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadCandidate(testContext, g.Grant, plan.ReadRequest{Envelope: newer, ValidationID: id}); err == nil {
		t.Fatal("revoked grant reads validation")
	}
	out, err = r.ValidateCandidate(testContext, g2.Grant, validationRequest(newer, g2.Claims.RequestEpoch))
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Now().Add(plan.ValidFor + time.Second) }
	v := readPlanValidation(t, r, g2.Grant, newer, out.Receipt.Resource.ID)
	if v.Usable || v.State != "passed" {
		t.Fatal("expiry must invalidate use without rewriting historical result", v)
	}
}
func TestFormalValidationRejectsForgedOriginalAndForeignOwnership(t *testing.T) {
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	e, _, i := planSetup(t, r, g)
	e = preparePlan(t, r, g.Grant, e, i)
	tampered := e
	tampered.Candidate.Intents = append([]plan.StoredIntent{}, e.Candidate.Intents...)
	tampered.Candidate.Intents[0].Before.Tag = planPointer(777)
	if _, err := r.ValidateCandidate(testContext, g.Grant, validationRequest(tampered, g.Claims.RequestEpoch)); err == nil {
		t.Fatal("web.db forged original accepted")
	}
	tampered = e
	tampered.Owner = repository.NewID()
	if _, err := r.ReadCandidate(testContext, g.Grant, plan.ReadRequest{Envelope: tampered}); err == nil {
		t.Fatal("foreign draft accepted")
	}
	var n int
	err := r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT count(*) FROM candidate_validations").Scan(&n)
	})
	if err != nil || n != 0 {
		t.Fatal("rejected command persisted a successful validation", n, err)
	}
	// Fail after Job creation but before Validation insertion. The enclosing
	// manager transaction must also remove the Job, event, audit and receipt.
	err = r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_validation BEFORE INSERT ON candidate_validations BEGIN SELECT RAISE(ABORT,'synthetic_validation_failure'); END`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ValidateCandidate(testContext, g.Grant, validationRequest(e, g.Claims.RequestEpoch)); err == nil {
		t.Fatal("injected validation write failure ignored")
	}
	err = r.store.Read(testContext, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM candidate_validations)+(SELECT count(*) FROM jobs)+(SELECT count(*) FROM api_receipts)+(SELECT count(*) FROM evidence_records WHERE operation IN ('job-created','validate-candidate'))`).Scan(&n)
	})
	if err != nil || n != 0 {
		t.Fatal("failed admission left partial validation evidence", n, err)
	}
}
