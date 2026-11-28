package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

const maxRestoreKeyVersions = 64

type BackupRestoreKeys struct {
	Partition string `json:"partition"`
	Required  []int  `json:"required"`
	Missing   []int  `json:"missing"`
	State     string `json:"state"`
}

type BackupRestoreEvidence struct {
	Pair BackupPairManifest  `json:"pair"`
	Keys []BackupRestoreKeys `json:"keys"`
}

// InspectBackupRestore verifies the exact sealed backup and reports required
// key versions. A missing map entry means no current key inventory. Presence of
// the requested versions cannot prove that any ciphertext is decryptable.
func InspectBackupRestore(ctx context.Context, outputRoot, id string, available map[string][]int) (BackupRestoreEvidence, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	manifest, err := VerifyBackupPair(ctx, outputRoot, id)
	if err != nil {
		return BackupRestoreEvidence{}, err
	}
	root := filepath.Join(outputRoot, backupPairPrefix+id)
	out := BackupRestoreEvidence{Pair: manifest, Keys: []BackupRestoreKeys{}}
	for _, slot := range []struct {
		kind       repository.Kind
		partitions []string
		schema     int
	}{
		{repository.Manager, []string{secret.Privileged}, manifest.Manager.BackupManifest.SchemaVersion},
		{repository.Web, []string{secret.Session, secret.TLS}, manifest.Web.BackupManifest.SchemaVersion},
	} {
		path := filepath.Join(root, string(slot.kind), pairDatabaseFile)
		required, err := backupKeyVersions(ctx, path, slot.schema, slot.partitions)
		if err != nil {
			return BackupRestoreEvidence{}, err
		}
		for _, partition := range slot.partitions {
			evidence := BackupRestoreKeys{Partition: partition, Required: required[partition], Missing: []int{}, State: "not-required"}
			if evidence.Required == nil {
				evidence.Required = []int{}
			}
			if len(evidence.Required) != 0 {
				coverage, known := available[partition]
				evidence.State = "unknown"
				if known {
					if len(coverage) > maxRestoreKeyVersions {
						return BackupRestoreEvidence{}, repository.ErrInvalid
					}
					evidence.State = "versions-present-unverified"
					for _, version := range evidence.Required {
						if !slices.Contains(coverage, version) {
							evidence.Missing = append(evidence.Missing, version)
						}
					}
					if len(evidence.Missing) != 0 {
						evidence.State = "missing-versions"
					}
				}
			}
			out.Keys = append(out.Keys, evidence)
		}
	}
	return out, nil
}

func backupKeyVersions(ctx context.Context, database string, schema int, partitions []string) (map[string][]int, error) {
	out := map[string][]int{}
	if schema < 5 { // The verified migration history predates SecretStore.
		return out, nil
	}
	conn, err := connect(database, true, 1)
	if err != nil {
		return nil, repository.ErrUnavailable
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT partition,provider,key_version FROM secret_records
        GROUP BY partition,provider,key_version ORDER BY partition,key_version LIMIT ?`, maxRestoreKeyVersions+1)
	if err != nil {
		return nil, pairUnavailable(ctx)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var partition, provider string
		var version int
		if err := rows.Scan(&partition, &provider, &version); err != nil || count > maxRestoreKeyVersions ||
			!slices.Contains(partitions, partition) || provider != secret.Provider || version <= 0 {
			return nil, errors.New("BACKUP_SECRETSTORE_SCOPE_UNAVAILABLE")
		}
		out[partition] = append(out[partition], version)
	}
	if rows.Err() != nil {
		return nil, pairUnavailable(ctx)
	}
	return out, nil
}
