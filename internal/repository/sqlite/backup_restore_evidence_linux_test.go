package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/secret"
)

func TestInspectBackupRestoreReportsMissingKeysWithoutClaimingDecryption(t *testing.T) {
	manager, web := backupPairStores(t)
	for _, item := range []struct {
		store     *Store
		partition string
		version   int
	}{{manager, secret.Privileged, 3}, {web, secret.TLS, 4}, {web, secret.Session, 2}} {
		if err := item.store.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO secret_records VALUES(?,'synthetic-id','synthetic-purpose',?,?,X'AA')",
				item.partition, secret.Provider, item.version)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	root := privateBackupPairRoot(t)
	pair, err := PublishBackupPair(t.Context(), root, manager, web)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := InspectBackupRestore(t.Context(), root, pair.ID, map[string][]int{
		secret.TLS: {4}, secret.Session: {},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{secret.Privileged: "unknown", secret.TLS: "versions-present-unverified", secret.Session: "missing-versions"}
	if len(proof.Keys) != len(want) || proof.Pair.ID != pair.ID {
		t.Fatalf("incomplete sealed restore evidence: %+v", proof)
	}
	for _, key := range proof.Keys {
		if key.State != want[key.Partition] || len(key.Required) != 1 || key.Partition == secret.Session && (len(key.Missing) != 1 || key.Missing[0] != 2) {
			t.Fatalf("wrong evidence for %s: %+v", key.Partition, key)
		}
	}
}
