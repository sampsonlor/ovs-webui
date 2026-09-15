//go:build linux

package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/certificates"
	"github.com/sampsonlor/ovs-webui/internal/repository/secrets"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type tlsAuthorityStub struct {
	state tlscontrol.State
	err   error
}

func (s *tlsAuthorityStub) TLSState(context.Context) (tlscontrol.State, error) { return s.state, s.err }
func (*tlsAuthorityStub) ExecuteTLS(context.Context, string, tlscontrol.Command) (apitypes.Result, error) {
	panic("unused")
}

func TestRealHandshakeProofAtomicTLSInstallAndIndependentFallback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	o := sqlite.Options{Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "tls-http-test"}
	if err := sqlite.Initialize(ctx, o); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ring, _ := secret.NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{12}, 32)})
	partition, _ := secrets.New(ctx, db, secret.TLS, ring)
	cert, key, _ := tlscontrol.Bootstrap("console.example", time.Now())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(cert)
	store, _ := certificates.New(partition, "console.example", roots)
	if err = store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := store.State(ctx)
	public, _ := store.PublicCertificate(ctx)
	roots.AppendCertsFromPEM(public)
	id := apitypes.RequestID(time.Now())
	payload, _ := json.Marshal(map[string]string{"request_id": id, "certificate_pem": string(cert), "private_key_pem": string(key.Bytes())})
	d, err := store.Stage(ctx, repository.NewID(), repository.NewID(), id, payload)
	if err != nil {
		t.Fatal(err)
	}
	authority := &tlsAuthorityStub{}
	managed, err := NewManagedTLS(ctx, store, authority)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(servedCertificate(r.Context()))) }))
	server.Config.ConnContext = managed.ConnContext
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: managed.GetCertificate, SessionTicketsDisabled: true}
	server.StartTLS()
	defer server.Close()
	makeClient := func(keepAlive bool) *http.Client {
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "console.example", MinVersion: tls.VersionTLS13}, DisableKeepAlives: !keepAlive}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 5 * time.Second}
	}
	request := func(client *http.Client, want string) {
		t.Helper()
		res, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(b) != want {
			t.Fatalf("served proof %q, want %q", b, want)
		}
	}
	old := makeClient(true)
	request(old, state.BootstrapID)
	now := tlscontrol.Now()
	authority.state = tlscontrol.State{Revision: repository.NewID(), TrialID: d.ID, BootID: now.BootID, StartedNS: now.NS, DeadlineNS: now.NS + int64(tlscontrol.RecoveryWindow), DeadlineWall: now.Wall.Add(tlscontrol.RecoveryWindow).Unix()}
	if err = managed.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	request(old, state.BootstrapID)
	request(makeClient(false), d.ID)
	// A manager outage cannot reset or extend the already persisted local lease.
	authority.err = apitypes.Fail(503, "MANAGER_UNAVAILABLE")
	if err = managed.Sync(ctx); err == nil {
		t.Fatal("manager outage hidden")
	}
	current := *managed.serving.Load()
	current.State.DeadlineNS = tlscontrol.Now().NS - 1
	managed.serving.Store(&current)
	request(makeClient(false), state.BootstrapID)
	// An unreadable candidate cannot replace a usable in-memory fallback.
	authority.err = nil
	authority.state.TrialID = repository.NewID()
	authority.state.Revision = repository.NewID()
	if err = managed.Sync(ctx); err == nil {
		t.Fatal("missing candidate installed")
	}
	request(makeClient(false), state.BootstrapID)
}
