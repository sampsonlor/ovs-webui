//go:build linux

package publicapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

func TestHTTPAdmissionDurableReplayReceiptAndCurrentAuthority(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	o := sqlite.Options{Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "http-test"}
	if err := sqlite.Initialize(ctx, o); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ledger := requests.New(store)
	requestEpoch, err := ledger.Epoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	auth := &testAuthority{}
	executions := 0
	gateway := testGateway{
		execute: func(ctx context.Context, s Subject, q Query, c requests.Command) (apitypes.Result, error) {
			return ledger.Execute(ctx, c, func(ctx context.Context) error { _, err := auth.Revalidate(ctx, s); return err }, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
				executions++
				if c.Precondition != `"r1"` {
					return requests.Mutation{}, apitypes.Fail(412, "PRECONDITION_FAILED")
				}
				body := json.RawMessage(fmt.Sprintf(`{"id":%q,"revision":"r2","kind":"label","name":"synthetic","description":""}`, resource))
				_, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES('label',?,?,?)", s.ID, resource, []byte(body))
				return requests.Mutation{Status: 200, Body: body, Resource: &apitypes.Ref{Kind: "label", ID: resource}, Terminal: true}, err
			})
		},
		read: func(ctx context.Context, s Subject, q Query) (Response, error) {
			receipt, err := ledger.Find(ctx, s.ID, q.Values.Get("epoch"), q.Path["request_id"], func(ctx context.Context, op string, ref *apitypes.Ref) error {
				if op != "saveLabel" || ref == nil || ref.ID != resource {
					t.Fatal("original operation/resource authorization missing")
				}
				_, err := auth.Revalidate(ctx, s)
				return err
			})
			if err != nil {
				return Response{}, err
			}
			body, _ := json.Marshal(receipt)
			return Response{Status: 200, Body: body}, nil
		},
	}
	h := newTestHandler(t, Options{Authorizer: auth, Gateway: gateway})
	id := apitypes.RequestID(time.Now())
	headers := commandHeaders(id)
	headers["X-OVS-Request-Epoch"] = requestEpoch
	body := fmt.Sprintf(`{"request_id":%q,"name":"synthetic","description":""}`, id)
	first := call(h, "PATCH", "/api/v1/labels/"+resource, body, headers)
	if first.Code != 200 || first.Header().Get("ETag") != `"r2"` {
		t.Fatal(first.Code, first.Body)
	}
	// Simulate response loss and a web process restart: reopen the same DB and
	// retry the original key, then query the original receipt. No new mutation.
	_ = store.Close()
	store, err = sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	ledger = requests.New(store)
	replay := call(h, "PATCH", "/api/v1/labels/"+resource, body, headers)
	if replay.Code != 200 || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Body.String() != first.Body.String() || executions != 1 {
		t.Fatal("replay failed", replay.Code, replay.Body, executions)
	}
	receiptPath := fmt.Sprintf("/api/v1/requests/%s?domain=workspace&epoch=%s", id, requestEpoch)
	if w := call(h, "GET", receiptPath, "", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	headers["If-Match"] = `"r2"`
	assertProblem(t, h, call(h, "PATCH", "/api/v1/labels/"+resource, body, headers), 409, "IDEMPOTENCY_MISMATCH")
	auth.revoked.Store(true)
	assertProblem(t, h, call(h, "GET", receiptPath, "", nil), 403, "REVOKED")
}
