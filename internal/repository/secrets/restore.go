package secrets

import (
	"context"
	"database/sql"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

// VerifyRetained detects missing or invalid owner material before the offline
// restore command changes credentials or TLS state. Browser contexts are
// discarded independently. This query checks the ciphertext retained by a
// restored SecretStore and emits no record identifiers or plaintext.
func VerifyRetained(ctx context.Context, db *sqlite.Store, stores ...*Store) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	byPartition := make(map[string]*Store, len(stores))
	for _, store := range stores {
		if store == nil {
			continue
		}
		if store.DB != db || byPartition[store.Partition] != nil {
			return secret.ErrUnavailable
		}
		byPartition[store.Partition] = store
	}
	return db.Read(ctx, func(ctx context.Context, conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `
SELECT partition,id,purpose,provider,key_version,length(envelope),
       CASE WHEN length(envelope) <= ? THEN envelope ELSE X'' END
  FROM secret_records LIMIT 8193`, 2*secret.MaximumBytes)
		if err != nil {
			return secret.ErrUnavailable
		}
		defer rows.Close()
		counts := map[string]int{}
		for rows.Next() {
			var partition, id, purpose, provider string
			var version, length int
			var encrypted []byte
			if err := rows.Scan(&partition, &id, &purpose, &provider, &version, &length, &encrypted); err != nil {
				return secret.ErrUnavailable
			}
			store := byPartition[partition]
			counts[partition]++
			if store == nil || counts[partition] > 4096 || provider != secret.Provider ||
				version < 1 || len(store.Ring.Keys[version]) != 32 || length != len(encrypted) || length == 0 {
				return secret.ErrUnavailable
			}
			// The advertised version and the version authenticated by the
			// envelope must agree. Do not accept another version for this row.
			ring := secret.Ring{Active: version, Keys: map[int][]byte{version: store.Ring.Keys[version]}}
			if _, err := ring.Open(store.DatabaseID, partition, id, purpose, encrypted); err != nil {
				return secret.ErrUnavailable
			}
		}
		if rows.Err() != nil {
			return secret.ErrUnavailable
		}
		return nil
	})
}

func HasRetainedTLSState(ctx context.Context, db *sqlite.Store) (bool, error) {
	if db.Kind() != repository.Web {
		return false, secret.ErrUnavailable
	}
	var required bool
	err := db.Read(ctx, func(ctx context.Context, conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tls_local_state)").Scan(&required)
	})
	if err != nil {
		return false, secret.ErrUnavailable
	}
	return required, nil
}
