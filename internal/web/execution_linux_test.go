//go:build linux

package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

type fixtureSafety struct{}

func (fixtureSafety) Check(context.Context, execution.Request) error { return nil }
func TestWorkspaceExecutionReservationSurvivesRestartAndBlocksNewEdits(t *testing.T) {
	_, a, store := browserFixture(t)
	manager := a.manager.(*auth.Repository)
	ctx := context.Background()
	g, err := manager.Authenticate(ctx, authn.Login{Provider: "local", Username: "admin", Password: browserPassword})
	if err != nil {
		t.Fatal(err)
	}
	b := candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: repository.NewID()}
	p := &workspaceProvider{snapshot: candidate.Snapshot{Generation: b.Generation, Revision: "native-1", Schema: "schema-1", Policy: "reviewed", Ports: map[string]candidate.Port{b.ManagementID: {Binding: b, Known: true, SchemaSupported: true, Modes: []string{"access"}, Authority: "local-managed", Dependency: "dependency", VLAN: candidate.VLAN{Mode: workspacePointer("access"), Tag: workspacePointer(10), Trunks: []int{}, CVLANs: []int{}}}}}}
	manager.WithInventory(p)
	w, err := NewWorkspace(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	s := publicapi.Subject{ID: g.Claims.PrincipalID, Credential: g.Grant}
	e, err := w.envelope(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	contract, _ := apicontract.New()
	op, path, _ := contract.Match("PATCH", "/api/v1/candidate")
	q := publicapi.Query{Operation: op, Path: path, Values: url.Values{}}
	intent := candidate.Intent{ID: repository.NewID(), Operation: "port.vlan.set", Object: b, Value: candidate.VLAN{Mode: workspacePointer("access"), Tag: workspacePointer(20), Trunks: []int{}, CVLANs: []int{}}}
	id := workspaceID()
	body, _ := json.Marshal(map[string]any{"request_id": id, "operation": "stage", "intents": []candidate.Intent{intent}})
	cmd := requests.Command{Principal: s.ID, Epoch: e.Epoch, Domain: "workspace", ID: id, Operation: "changeCandidate", Method: "PATCH", URI: "/api/v1/candidate", Precondition: `"` + e.Candidate.Revision + `"`, Payload: body}
	if _, err = w.Execute(ctx, s, q, cmd); err != nil {
		t.Fatal(err)
	}
	e, err = w.envelope(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	validateID := workspaceID()
	body, _ = json.Marshal(map[string]any{"request_id": validateID, "candidate_id": e.Candidate.ID, "candidate_revision": e.Candidate.Revision})
	ack, err := manager.ValidateCandidate(ctx, g.Grant, candidate.ValidateRequest{Envelope: e, Command: authn.Command{Method: "POST", URI: "/api/v1/validations", RequestID: validateID, Epoch: g.Claims.RequestEpoch, Payload: body}})
	if err != nil {
		t.Fatal(err)
	}
	in := execution.Request{ID: workspaceID(), ValidationID: ack.Receipt.Resource.ID, Envelope: e}
	p.snapshot.Policy = "changed-after-validation"
	if _, err = w.ReserveExecution(ctx, s, in, fixtureSafety{}); auth.ErrorCode(err) != "VALIDATION_NOT_USABLE" {
		t.Fatal(err)
	}
	if err = store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var count int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM candidate_execution_reservations").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("unusable validation froze candidate")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.snapshot.Policy = "reviewed"
	if _, err = w.ReserveExecution(ctx, s, in, nil); err == nil {
		t.Fatal("missing safety guard admitted")
	}
	lease, err := w.ReserveExecution(ctx, s, in, fixtureSafety{})
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(ctx, in); err != nil {
		t.Fatal(err)
	}
	w2, err := NewWorkspace(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	lease, err = w2.ReserveExecution(ctx, s, in, fixtureSafety{})
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(ctx, in); err != nil {
		t.Fatal(err)
	}
	if original, err := w2.Execute(ctx, s, q, cmd); err != nil || !original.Replayed {
		t.Fatal("frozen candidate hid original save receipt", err)
	}
	id = workspaceID()
	body, _ = json.Marshal(map[string]any{"request_id": id, "operation": "discard"})
	changed := cmd
	changed.ID = id
	changed.Payload = body
	changed.Precondition = `"` + e.Candidate.Revision + `"`
	if _, err = w2.Execute(ctx, s, q, changed); auth.ErrorCode(err) != "CANDIDATE_RESERVED" {
		t.Fatal(err)
	}
	other := in
	other.ID = workspaceID()
	if _, err = w2.ReserveExecution(ctx, s, other, fixtureSafety{}); auth.ErrorCode(err) != "CANDIDATE_RESERVED" {
		t.Fatal(err)
	}
	if err = manager.RevokeAuth(ctx, g.Grant); err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(ctx, in); err == nil {
		t.Fatal("reservation retained revoked authority")
	}
}
