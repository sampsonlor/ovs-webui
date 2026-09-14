//go:build linux

package certificates

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/secrets"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCandidateIdempotencyEncryptedIdentityRotationAndRestore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	o := sqlite.Options{Path: filepath.Join(root, "web.db"), Kind: repository.Web, SoftwareVersion: "tls-store-test"}
	if err := sqlite.Initialize(ctx, o); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ring, _ := secret.NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{7}, 32)})
	partition, _ := secrets.New(ctx, db, secret.TLS, ring)
	cert, key, _ := tlscontrol.Bootstrap("console.example", time.Now())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(cert)
	s, _ := New(partition, "console.example", roots)
	if err = s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Initialize(ctx); err == nil {
		t.Fatal("bootstrap overwritten")
	}
	initial, err := s.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	public, err := s.PublicCertificate(ctx)
	if err != nil || bytes.Contains(public, []byte("PRIVATE KEY")) {
		t.Fatal(err)
	}
	principal, epoch, id := repository.NewID(), repository.NewID(), apitypes.RequestID(time.Now())
	payload, _ := json.Marshal(map[string]string{"request_id": id, "certificate_pem": string(cert), "private_key_pem": string(key.Bytes())})
	d, err := s.Stage(ctx, principal, epoch, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Stage(ctx, principal, epoch, id, payload)
	if err != nil || d != again {
		t.Fatal("unstable replay", err)
	}
	if _, err = s.Stage(ctx, principal, epoch, id, append(payload, ' ')); err == nil {
		t.Fatal("changed private input accepted")
	}
	if _, loaded, err := s.Load(ctx, d.ID); err != nil || loaded.Fingerprint != d.Fingerprint {
		t.Fatal("load failed", err)
	}
	next, _ := secret.NewRing(2, map[int][]byte{1: ring.Keys[1], 2: bytes.Repeat([]byte{8}, 32)})
	if err = s.Rewrap(ctx, next); err != nil {
		t.Fatal(err)
	}
	if after, err := s.State(ctx); err != nil || after != initial {
		t.Fatal("rotation changed active state", err)
	}
	if again, err = s.Stage(ctx, principal, epoch, id, payload); err != nil || again != d {
		t.Fatal("rotation invalidated receipt replay", err)
	}
	state := initial
	state.Authority = tlscontrol.State{ActiveID: d.ID, TrialID: repository.NewID(), DeadlineNS: 100}
	if err = s.SaveState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareRestore(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = s.State(ctx)
	if err != nil || state.Authority.TrialID != "" || state.Authority.ActiveID != d.ID {
		t.Fatal("restore replayed TLS trial", err)
	}
	backup, err := db.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(backup, "database.sqlite"))
	var private struct {
		PrivateKey []byte `json:"private_key"`
	}
	_ = private
	if bytes.Contains(data, key.Bytes()) || bytes.Contains(data, []byte("BEGIN PRIVATE KEY")) {
		t.Fatal("backup contains private key")
	}
}
