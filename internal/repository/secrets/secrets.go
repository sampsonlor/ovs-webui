// Package secrets persists only ciphertext and non-secret consumer metadata.
package secrets

import (
	"context"
	"database/sql"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

type Store struct {
	DB                    *sqlite.Store
	Partition, DatabaseID string
	Ring                  secret.Ring
}

func New(ctx context.Context, db *sqlite.Store, partition string, ring secret.Ring) (*Store, error) {
	if (db.Kind() == repository.Web && partition != secret.TLS && partition != secret.Session) || (db.Kind() == repository.Manager && partition != secret.Privileged) {
		return nil, secret.ErrUnavailable
	}
	checked, err := secret.NewRing(ring.Active, ring.Keys)
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db, Partition: partition, Ring: checked}
	err = db.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT database_id FROM database_meta WHERE singleton=1").Scan(&s.DatabaseID)
	})
	return s, err
}
func (s *Store) Put(ctx context.Context, tx *sql.Tx, id, purpose string, value secret.Value) error {
	blob, err := s.Ring.Seal(s.DatabaseID, s.Partition, id, purpose, value)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM secret_records WHERE partition=? AND id<>?", s.Partition, id).Scan(&count); err != nil {
		return err
	}
	if count >= 4096 {
		return secret.ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO secret_records VALUES(?,?,?,?,?,?) ON CONFLICT(partition,id) DO UPDATE SET purpose=excluded.purpose,provider=excluded.provider,key_version=excluded.key_version,envelope=excluded.envelope", s.Partition, id, purpose, secret.Provider, s.Ring.Active, blob)
	return err
}
func (s *Store) Get(ctx context.Context, id, purpose string) (secret.Value, error) {
	var blob []byte
	err := s.DB.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT envelope FROM secret_records WHERE partition=? AND id=? AND purpose=? AND provider=?", s.Partition, id, purpose, secret.Provider).Scan(&blob)
	})
	if err != nil {
		return secret.Value{}, secret.ErrUnavailable
	}
	return s.Ring.Open(s.DatabaseID, s.Partition, id, purpose, blob)
}

// Rewrap is offline: the caller holds the database's exclusive daemon lock.
// Old keys remain available until the new key and all ciphertext are durable.
func (s *Store) Rewrap(ctx context.Context, next secret.Ring) error {
	return s.DB.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id,purpose,envelope FROM secret_records WHERE partition=?", s.Partition)
		if err != nil {
			return err
		}
		type row struct {
			id, purpose string
			blob        []byte
		}
		items := []row{}
		for rows.Next() {
			var v row
			if err = rows.Scan(&v.id, &v.purpose, &v.blob); err != nil {
				rows.Close()
				return err
			}
			items = append(items, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, v := range items {
			value, err := next.Open(s.DatabaseID, s.Partition, v.id, v.purpose, v.blob)
			if err != nil {
				return err
			}
			blob, err := next.Seal(s.DatabaseID, s.Partition, v.id, v.purpose, value)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE secret_records SET key_version=?,envelope=? WHERE partition=? AND id=?", next.Active, blob, s.Partition, v.id); err != nil {
				return err
			}
		}
		return nil
	})
}
