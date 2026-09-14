package certificates

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"sync"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/secrets"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type Store struct {
	Secrets *secrets.Store
	Host    string
	Roots   *x509.CertPool
	mu      sync.Mutex
}
type LocalState struct {
	BootstrapID string           `json:"bootstrap_id"`
	Authority   tlscontrol.State `json:"authority"`
}
type identity struct {
	Certificate []byte `json:"certificate"`
	PrivateKey  []byte `json:"private_key"`
	Bootstrap   bool   `json:"bootstrap"`
}

func New(s *secrets.Store, host string, roots *x509.CertPool) (*Store, error) {
	if s.Partition != secret.TLS || host == "" {
		return nil, secret.ErrUnavailable
	}
	return &Store{Secrets: s, Host: host, Roots: roots}, nil
}
func (s *Store) saveIdentity(ctx context.Context, tx *sql.Tx, id string, cert []byte, key secret.Value, bootstrap bool) error {
	// The only JSON containing a reusable private key is immediately enveloped;
	// this representation never enters a receipt, log, export or public response.
	blob, _ := json.Marshal(identity{Certificate: cert, PrivateKey: key.Bytes(), Bootstrap: bootstrap})
	return s.Secrets.Put(ctx, tx, id, "tls-identity", secret.NewValue(blob))
}
func (s *Store) Initialize(ctx context.Context) error {
	cert, key, err := tlscontrol.Bootstrap(s.Host, time.Now())
	if err != nil {
		return err
	}
	id := repository.NewID()
	return s.Secrets.DB.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tls_local_state").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return apitypes.Fail(409, "TLS_ALREADY_INITIALIZED")
		}
		if err := s.saveIdentity(ctx, tx, id, cert, key, true); err != nil {
			return err
		}
		return s.saveState(ctx, tx, LocalState{BootstrapID: id})
	})
}
func (s *Store) saveState(ctx context.Context, tx *sql.Tx, state LocalState) error {
	plain, _ := json.Marshal(state)
	blob, err := s.Secrets.Ring.Seal(s.Secrets.DatabaseID, secret.TLS, "active-state", "tls-state", secret.NewValue(plain))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tls_local_state VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET envelope=excluded.envelope", blob)
	return err
}
func (s *Store) SaveState(ctx context.Context, state LocalState) error {
	return s.Secrets.DB.Write(ctx, func(ctx context.Context, tx *sql.Tx) error { return s.saveState(ctx, tx, state) })
}
func (s *Store) State(ctx context.Context) (LocalState, error) {
	var out LocalState
	var blob []byte
	err := s.Secrets.DB.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT envelope FROM tls_local_state WHERE singleton=1").Scan(&blob)
	})
	if err != nil {
		return out, secret.ErrUnavailable
	}
	value, err := s.Secrets.Ring.Open(s.Secrets.DatabaseID, secret.TLS, "active-state", "tls-state", blob)
	if err != nil || json.Unmarshal(value.Bytes(), &out) != nil || !apitypes.ManagementID(out.BootstrapID) {
		return LocalState{}, secret.ErrUnavailable
	}
	return out, nil
}
func (s *Store) Load(ctx context.Context, id string) (tls.Certificate, tlscontrol.Descriptor, error) {
	value, err := s.Secrets.Get(ctx, id, "tls-identity")
	if err != nil {
		return tls.Certificate{}, tlscontrol.Descriptor{}, err
	}
	var v identity
	if json.Unmarshal(value.Bytes(), &v) != nil {
		return tls.Certificate{}, tlscontrol.Descriptor{}, secret.ErrUnavailable
	}
	pair, d, err := tlscontrol.KeyPair(v.Certificate, secret.NewValue(v.PrivateKey), s.Host, s.Roots, time.Now(), v.Bootstrap)
	d.ID = id
	return pair, d, err
}
func (s *Store) Stage(ctx context.Context, principal, epoch, request string, payload []byte) (tlscontrol.Descriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var d tlscontrol.Descriptor
	var version int
	var cert []byte
	err := s.Secrets.DB.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT id,fingerprint,digest_version,input_digest,certificate_pem FROM tls_candidates WHERE principal_id=? AND epoch=? AND request_id=?", principal, epoch, request).Scan(&d.ID, &d.Fingerprint, &version, &d.InputDigest, &cert)
	})
	if err == nil {
		digest, e := s.Secrets.Ring.Digest(version, "tls-candidate", payload)
		if e != nil {
			return d, e
		}
		if digest != d.InputDigest {
			return d, apitypes.Fail(409, "IDEMPOTENCY_CONFLICT")
		}
		block, _ := pem.Decode(cert)
		if block == nil {
			return d, secret.ErrUnavailable
		}
		leaf, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return d, secret.ErrUnavailable
		}
		d.Hostname = s.Host
		d.NotBefore = leaf.NotBefore.UTC()
		d.NotAfter = leaf.NotAfter.UTC()
		return d, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return d, err
	}
	var input struct {
		RequestID   string `json:"request_id"`
		Certificate string `json:"certificate_pem"`
		PrivateKey  string `json:"private_key_pem"`
	}
	if json.Unmarshal(payload, &input) != nil || input.RequestID != request {
		return d, apitypes.Fail(422, "INVALID_REQUEST")
	}
	cert = []byte(input.Certificate)
	key := secret.NewValue([]byte(input.PrivateKey))
	_, d, err = tlscontrol.KeyPair(cert, key, s.Host, s.Roots, time.Now(), false)
	if err != nil {
		return d, err
	}
	d.ID = repository.NewID()
	d.InputDigest, err = s.Secrets.Ring.Digest(s.Secrets.Ring.Active, "tls-candidate", payload)
	if err != nil {
		return d, err
	}
	err = s.Secrets.DB.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tls_candidates").Scan(&count); err != nil {
			return err
		}
		if count >= 128 {
			return apitypes.Fail(429, "TLS_CANDIDATE_CAPACITY_REACHED")
		}
		if err := s.saveIdentity(ctx, tx, d.ID, cert, key, false); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO tls_candidates VALUES(?,?,?,?,?,?,?,?)", d.ID, principal, epoch, request, d.Fingerprint, s.Secrets.Ring.Active, d.InputDigest, cert)
		return err
	})
	return d, err
}
