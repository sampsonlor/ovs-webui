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
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

type Mapping struct {
	Grant       string    `json:"grant"`
	PrincipalID string    `json:"principal_id"`
	CSRF        string    `json:"csrf"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type Repository struct {
	store      *sqlite.Store
	keys       secret.Ring
	databaseID string
	now        func() time.Time
}

func New(ctx context.Context, store *sqlite.Store, key []byte) (*Repository, error) {
	keys, err := secret.NewRing(1, map[int][]byte{1: key})
	if err != nil {
		return nil, err
	}
	return NewWithKeys(ctx, store, keys)
}
func NewWithKeys(ctx context.Context, store *sqlite.Store, keys secret.Ring) (*Repository, error) {
	if store.Kind() != repository.Web {
		return nil, repository.ErrInvalid
	}
	keys, err := secret.NewRing(keys.Active, keys.Keys)
	if err != nil {
		return nil, err
	}
	r := &Repository{store: store, keys: keys, now: time.Now}
	err = store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
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
	envelope, err := r.keys.Seal(r.databaseID, secret.Session, hex.EncodeToString(hash[:]), "browser-grant", secret.NewValue(plain))
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
	plain, err := r.open(hash, envelope)
	if err != nil || json.Unmarshal(plain, &m) != nil || !m.ExpiresAt.After(r.now()) || !authn.ValidSecret(m.Grant, "ovsg_") || !authn.ValidSecret(m.CSRF, "ovsc_") || !apitypes.ManagementID(m.PrincipalID) {
		return Mapping{}, apitypes.Fail(401, "SESSION_INVALID")
	}
	return m, nil
}
func (r *Repository) open(hash [32]byte, envelope []byte) ([]byte, error) {
	if len(envelope) > 0 && envelope[0] == 1 {
		return authn.Unseal(r.keys.Keys[1], envelope, r.aad(hash))
	}
	value, err := r.keys.Open(r.databaseID, secret.Session, hex.EncodeToString(hash[:]), "browser-grant", envelope)
	return value.Bytes(), err
}
func (r *Repository) Rewrap(ctx context.Context, next secret.Ring) error {
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT session_hash,envelope FROM browser_sessions")
		if err != nil {
			return err
		}
		type entry struct{ hash, blob []byte }
		items := []entry{}
		for rows.Next() {
			var e entry
			if err = rows.Scan(&e.hash, &e.blob); err != nil {
				rows.Close()
				return err
			}
			items = append(items, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range items {
			if len(e.hash) != 32 {
				return secret.ErrUnavailable
			}
			var hash [32]byte
			copy(hash[:], e.hash)
			plain, err := r.open(hash, e.blob)
			if err != nil {
				return err
			}
			blob, err := next.Seal(r.databaseID, secret.Session, hex.EncodeToString(hash[:]), "browser-grant", secret.NewValue(plain))
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE browser_sessions SET envelope=? WHERE session_hash=?", blob, e.hash); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		r.keys = next
	}
	return err
}
func (r *Repository) PrepareRestore(ctx context.Context) error {
	return r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM browser_sessions; DELETE FROM session_mappings; DELETE FROM handoff_outbox WHERE receipt_id IS NULL;"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE api_authority SET epoch=?,high_watermark_ms=? WHERE singleton=1", repository.NewID(), r.now().UnixMilli())
		return err
	})
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
