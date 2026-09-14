// Package sessions stores only encrypted webd mappings, never role authority.
package sessions

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
)

type Mapping struct {
	Grant       string    `json:"grant"`
	PrincipalID string    `json:"principal_id"`
	CSRF        string    `json:"csrf"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type Repository struct {
	store      *sqlite.Store
	key        []byte
	databaseID string
	now        func() time.Time
}

func New(ctx context.Context, store *sqlite.Store, key []byte) (*Repository, error) {
	if store.Kind() != repository.Web || len(key) != 32 {
		return nil, repository.ErrInvalid
	}
	r := &Repository{store: store, key: append([]byte{}, key...), now: time.Now}
	err := store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT database_id FROM database_meta WHERE singleton=1").Scan(&r.databaseID)
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Repository) Epoch(ctx context.Context) (string, error) {
	return requests.New(r.store).Epoch(ctx)
}
func (r *Repository) aad(hash [32]byte) []byte {
	return []byte("webd-session/v1/" + r.databaseID + "/" + hex.EncodeToString(hash[:]))
}
func (r *Repository) Save(ctx context.Context, cookie string, m Mapping) error {
	if !authn.ValidSecret(cookie, "ovss_") || !authn.ValidSecret(m.Grant, "ovsg_") || !authn.ValidSecret(m.CSRF, "ovsc_") || !apitypes.ManagementID(m.PrincipalID) || !m.ExpiresAt.After(r.now()) || m.ExpiresAt.After(r.now().Add(authn.SessionAbsolute)) {
		return repository.ErrInvalid
	}
	hash := authn.Hash(cookie)
	plain, _ := json.Marshal(m)
	envelope, err := authn.Seal(r.key, plain, r.aad(hash))
	if err != nil {
		return err
	}
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM browser_sessions WHERE expires_at<=?", r.now().Unix()); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browser_sessions").Scan(&count); err != nil {
			return err
		}
		if count >= 4096 {
			return apitypes.Fail(429, "SESSION_CAPACITY_REACHED")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO browser_sessions VALUES(?,?,?)", hash[:], envelope, m.ExpiresAt.Unix())
		return err
	})
}
func (r *Repository) Load(ctx context.Context, cookie string) (Mapping, error) {
	var m Mapping
	if !authn.ValidSecret(cookie, "ovss_") {
		return m, apitypes.Fail(401, "UNAUTHENTICATED")
	}
	hash := authn.Hash(cookie)
	var envelope []byte
	err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT envelope FROM browser_sessions WHERE session_hash=? AND expires_at>?", hash[:], r.now().Unix()).Scan(&envelope)
	})
	if err != nil {
		return m, err
	}
	plain, err := authn.Unseal(r.key, envelope, r.aad(hash))
	if err != nil || json.Unmarshal(plain, &m) != nil || !m.ExpiresAt.After(r.now()) || !authn.ValidSecret(m.Grant, "ovsg_") || !authn.ValidSecret(m.CSRF, "ovsc_") || !apitypes.ManagementID(m.PrincipalID) {
		return Mapping{}, apitypes.Fail(401, "SESSION_INVALID")
	}
	return m, nil
}
func (r *Repository) Delete(ctx context.Context, cookie string) error {
	if !authn.ValidSecret(cookie, "ovss_") {
		return apitypes.Fail(401, "UNAUTHENTICATED")
	}
	hash := authn.Hash(cookie)
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM browser_sessions WHERE session_hash=?", hash[:])
		return err
	})
}
