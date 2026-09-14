//go:build linux

package requests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

var ctx = context.Background()

func fixture(t *testing.T, kind repository.Kind) (*Repository, sqlite.Options) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	o := sqlite.Options{Path: filepath.Join(root, "authority.db"), Kind: kind, SoftwareVersion: "api-test"}
	if err := sqlite.Initialize(ctx, o); err != nil {
		t.Fatal(err)
	}
	return reopen(t, o), o
}
func reopen(t *testing.T, o sqlite.Options) *Repository {
	t.Helper()
	s, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}
func command(t *testing.T, r *Repository) Command {
	t.Helper()
	epoch, err := r.Epoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return Command{Principal: repository.NewID(), Epoch: epoch, Domain: r.domain, ID: apitypes.RequestID(r.now()), Operation: "changeLabel", Method: "PATCH", URI: "/labels/one", Precondition: `"r1"`, Payload: json.RawMessage(`{"name":"synthetic"}`)}
}
func allow(context.Context) error { return nil }
func noMutation(context.Context, *sql.Tx) (Mutation, error) {
	return Mutation{Status: 200, Body: json.RawMessage(`{"ok":true}`), Terminal: true}, nil
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var p *apitypes.Problem
	if !errors.As(err, &p) || p.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func TestRestartReplayAndNoSecondMutation(t *testing.T) {
	r, o := fixture(t, repository.Web)
	c := command(t, r)
	calls := 0
	mutate := func(ctx context.Context, tx *sql.Tx) (Mutation, error) {
		calls++
		_, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES('label',?,?,?)", c.Principal, "one", []byte(`{"version":1}`))
		return Mutation{Status: 200, Body: json.RawMessage(`{"version":1}`), Terminal: true}, err
	}
	first, err := r.Execute(ctx, c, allow, mutate)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.store.Close()
	r = reopen(t, o)
	later := time.Now().Add(time.Hour)
	r.now = func() time.Time { return later }
	replay, err := r.Execute(ctx, c, allow, mutate)
	if err != nil || !replay.Replayed || calls != 1 || !bytes.Equal(first.Body, replay.Body) || first.Receipt.CorrelationID != replay.Receipt.CorrelationID {
		t.Fatal("lost durable identity", err)
	}
	for _, change := range []func(*Command){func(c *Command) { c.URI = "/labels/two" }, func(c *Command) { c.Method = "POST" }, func(c *Command) { c.Operation = "other" }, func(c *Command) { c.Precondition = `"r2"` }, func(c *Command) { c.Payload = json.RawMessage(`{"name":"different"}`) }} {
		other := c
		change(&other)
		_, err = r.Execute(ctx, other, allow, mutate)
		code(t, err, "IDEMPOTENCY_MISMATCH")
	}
	if !r.store.Status().Writable {
		t.Fatal("business rejection latched storage degradation")
	}
}
func TestPrincipalDomainAuthorizationAndEpochIsolation(t *testing.T) {
	r, _ := fixture(t, repository.Web)
	c := command(t, r)
	if _, err := r.Execute(ctx, c, allow, noMutation); err != nil {
		t.Fatal(err)
	}
	denied := func(context.Context) error { return apitypes.Fail(403, "REVOKED") }
	_, err := r.Execute(ctx, c, denied, noMutation)
	code(t, err, "REVOKED")
	if _, err = r.Find(ctx, c.Principal, c.Epoch, c.ID, func(context.Context, string, *apitypes.Ref) error { return apitypes.Fail(403, "REVOKED") }); err == nil {
		t.Fatal("receipt leaked after revoke")
	}
	if _, err = r.Find(ctx, repository.NewID(), c.Epoch, c.ID, func(context.Context, string, *apitypes.Ref) error { return nil }); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross principal receipt", err)
	}
	wrong := c
	wrong.Domain = "management"
	_, err = r.Execute(ctx, wrong, allow, noMutation)
	code(t, err, "INVALID_REQUEST")
	newEpoch := repository.NewID()
	if err = r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE api_authority SET epoch=?", newEpoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Execute(ctx, c, allow, noMutation); err != nil {
		t.Fatal("existing historical receipt not recoverable", err)
	}
	c.ID = apitypes.RequestID(time.Now())
	_, err = r.Execute(ctx, c, allow, noMutation)
	code(t, err, "REQUEST_EPOCH_CHANGED")
}
func TestTimeRollbackExpiryAndTerminalRetention(t *testing.T) {
	r, _ := fixture(t, repository.Web)
	now := time.Now()
	r.now = func() time.Time { return now }
	c := command(t, r)
	for _, delta := range []time.Duration{-6 * time.Minute, 3 * time.Minute} {
		old := c
		old.ID = apitypes.RequestID(now.Add(delta))
		_, err := r.Execute(ctx, old, allow, noMutation)
		code(t, err, "REQUEST_KEY_EXPIRED")
	}
	if _, err := r.Execute(ctx, c, allow, noMutation); err != nil {
		t.Fatal(err)
	}
	now = now.Add(-3 * time.Minute)
	fresh := c
	fresh.ID = apitypes.RequestID(now)
	_, err := r.Execute(ctx, fresh, allow, noMutation)
	code(t, err, "CLOCK_UNSAFE")
	if _, err = r.Execute(ctx, c, allow, noMutation); err != nil {
		t.Fatal("clock rollback blocked existing receipt", err)
	}
	now = now.Add(4 * time.Minute)
	unresolved := command(t, r)
	if _, err = r.Execute(ctx, unresolved, allow, func(context.Context, *sql.Tx) (Mutation, error) {
		return Mutation{Status: 200, Body: json.RawMessage(`{}`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * 24 * time.Hour)
	if err = r.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Find(ctx, c.Principal, c.Epoch, c.ID, func(context.Context, string, *apitypes.Ref) error { return nil }); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = r.Execute(ctx, c, allow, noMutation); err == nil {
		t.Fatal("pruned key admitted again")
	}
	if out, err := r.Execute(ctx, unresolved, allow, noMutation); err != nil || !out.Replayed {
		t.Fatal("unresolved receipt pruned", err)
	}
}
func TestConcurrentDuplicateAndAtomicPreconditionRejection(t *testing.T) {
	r, _ := fixture(t, repository.Web)
	c := command(t, r)
	var calls atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Execute(ctx, c, allow, func(context.Context, *sql.Tx) (Mutation, error) { calls.Add(1); return noMutation(ctx, nil) })
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate mutation", calls.Load())
	}
	c.ID = apitypes.RequestID(time.Now())
	_, err := r.Execute(ctx, c, allow, func(ctx context.Context, tx *sql.Tx) (Mutation, error) {
		_, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES('label','owner','must-rollback',?)", []byte(`{}`))
		if err != nil {
			return Mutation{}, err
		}
		return Mutation{}, apitypes.Fail(412, "PRECONDITION_FAILED")
	})
	code(t, err, "PRECONDITION_FAILED")
	var rows int
	if err = r.store.Read(ctx, func(ctx context.Context, conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, "SELECT count(*) FROM metadata").Scan(&rows)
	}); err != nil || rows != 0 {
		t.Fatal("rejection not atomic", rows, err)
	}
	if _, err = r.Find(ctx, c.Principal, c.Epoch, c.ID, func(context.Context, string, *apitypes.Ref) error { return nil }); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("rejected mutation gained successful receipt", err)
	}
}
func TestAcceptedRequiresJobInSameTransaction(t *testing.T) {
	r, _ := fixture(t, repository.Manager)
	c := command(t, r)
	job := apitypes.Ref{Kind: "job", ID: repository.NewID()}
	mutate := func(_ context.Context, _ *sql.Tx) (Mutation, error) {
		return Mutation{Status: 202, Body: json.RawMessage(`{}`), Job: &job, Resource: &job}, nil
	}
	_, err := r.Execute(ctx, c, allow, mutate)
	code(t, err, "JOB_NOT_DURABLE")
	result, err := r.Execute(ctx, c, allow, func(ctx context.Context, tx *sql.Tx) (Mutation, error) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO jobs VALUES(?,NULL,'queued',?)", job.ID, []byte(`{}`)); err != nil {
			return Mutation{}, err
		}
		return mutate(ctx, tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	var accepted apitypes.Accepted
	if json.Unmarshal(result.Body, &accepted) != nil || accepted.JobID != job.ID || accepted.CorrelationID != result.Receipt.CorrelationID {
		t.Fatal(string(result.Body))
	}
}
func TestSensitiveFingerprintAndOneTimeResponseNeverPersist(t *testing.T) {
	r, o := fixture(t, repository.Manager)
	c := command(t, r)
	c.Sensitive = true
	c.SecretResponse = true
	c.Payload = json.RawMessage(`{"password":"synthetic-private-input"}`)
	_, err := r.Execute(ctx, c, allow, noMutation)
	code(t, err, "SECRETSTORE_UNAVAILABLE")
	c.FingerprintKey = bytes.Repeat([]byte{7}, 32)
	secret := json.RawMessage(`{"secret":"synthetic-one-time-output"}`)
	first, err := r.Execute(ctx, c, allow, func(context.Context, *sql.Tx) (Mutation, error) {
		return Mutation{Status: 201, Body: secret, Terminal: true}, nil
	})
	if err != nil || !bytes.Equal(first.Body, secret) {
		t.Fatal(err)
	}
	replay, err := r.Execute(ctx, c, allow, noMutation)
	if err != nil || !replay.Replayed || replay.Status != 200 || bytes.Contains(replay.Body, []byte("one-time-output")) {
		t.Fatal("secret replayed", err)
	}
	_ = r.store.Close()
	files, _ := filepath.Glob(o.Path + "*")
	for _, file := range files {
		data, _ := os.ReadFile(file)
		for _, value := range []string{"synthetic-private-input", "synthetic-one-time-output"} {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("secret persisted", file)
			}
		}
	}
}
