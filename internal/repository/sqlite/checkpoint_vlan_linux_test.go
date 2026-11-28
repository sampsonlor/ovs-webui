//go:build linux

package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestVLANCheckpointOfflinePublicationAndIndependentVerification(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for the isolated owner-only offline checkpoint fixture")
	}
	manager, _ := backupPairStores(t)
	source, bindings := vlanCheckpointFixture(t)
	req := VLANCheckpointRequest{Root: privateBackupPairRoot(t), Generation: source.manifest.InstanceGeneration, TransactionID: source.manifest.TransactionID}
	ctx := t.Context()
	proof, _ := json.Marshal(map[string]string{"schema": source.schema, "root": source.root})
	if err := manager.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO generations VALUES(?,?,1)`, req.Generation, proof); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_state VALUES(1,?,'confirmed','fixture',?,'',0)`, req.Generation, proof); err != nil {
			return err
		}
		for _, entry := range bindings {
			if _, err := tx.ExecContext(ctx, `INSERT INTO identities VALUES(?,?,?,?,'active')`, entry.ManagementID, entry.Generation, entry.Table, entry.OVSUUID); err != nil {
				return err
			}
		}
		for _, fixture := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO principals VALUES('test-principal','synthetic-owner',1,X'01')`, nil},
			{`INSERT INTO request_receipts VALUES('test-receipt','test-request',?,'test-corr','test-principal','test-candidate','rev','digest',X'03','received',1)`, []any{req.TransactionID}},
			{`INSERT INTO transaction_journal VALUES(?,1,'confirmed',X'04')`, []any{req.TransactionID}},
			{`INSERT INTO jobs VALUES('test-job',?,'succeeded',X'05')`, []any{req.TransactionID}},
			{`INSERT INTO candidate_validations VALUES('test-validation','test-principal','test-candidate','rev','test-job','test-changeset',1,X'06',X'07')`, nil},
			{`INSERT INTO field_executions VALUES(?,'test-job','test-principal','test-validation','succeeded',1,X'08')`, []any{req.TransactionID}},
			{`INSERT INTO safe_applies VALUES(?,'test-principal','epoch','test-request','confirmed','manager',X'09')`, []any{req.TransactionID}},
			{`INSERT INTO last_known_good VALUES(1,?,?,?,?)`, []any{req.TransactionID, req.Generation, source.manifest.ConfirmedAt.UnixMilli(), source.document}},
		} {
			if _, err := tx.ExecContext(ctx, fixture.sql, fixture.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	dbPath := manager.options.Path
	if _, err := PublishVLANCheckpoint(ctx, dbPath, req); err == nil || !exactPairEntries(req.Root) {
		t.Fatal("live Manager or partial checkpoint accepted")
	}
	manager.Close()
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	bad := req
	bad.Generation = repository.NewID()
	if _, err := PublishVLANCheckpoint(ctx, dbPath, bad); err == nil || !exactPairEntries(req.Root) {
		t.Fatal("unreviewed generation changed the destination")
	}
	report, err := PublishVLANCheckpoint(ctx, dbPath, req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(req.Root, checkpointFileName)
	info, err := os.Lstat(path)
	if err != nil || !safeFile(info, 0) {
		t.Fatalf("published checkpoint must be owner-only: %v", err)
	}
	if after, err := os.ReadFile(dbPath); err != nil || !bytes.Equal(before, after) {
		t.Fatal("publication changed the original Manager database")
	}
	if err := os.Rename(dbPath, dbPath+".offline"); err != nil {
		t.Fatal(err)
	}
	result, err := VerifyVLANCheckpoint(ctx, req)
	if err != nil || result.SHA256 != report.SHA256 || result.Manifest.TransactionID != req.TransactionID {
		t.Fatal("failed independent verification with no database path", err)
	}
	if _, err := VerifyVLANCheckpoint(ctx, bad); err == nil {
		t.Fatal("unrelated generation verified")
	}
	newer := report
	newer.Manifest.ConfirmedAt = newer.Manifest.ConfirmedAt.Add(time.Millisecond)
	newer.Manifest.TransactionID = repository.NewID()
	newer.SHA256 = checkpointDigest(newer)
	if _, err := publishVLANCheckpointFile(ctx, req.Root, newer); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || os.SameFile(info, after) {
		t.Fatal("checkpoint overwrote its previous inode in place")
	}
	if _, err := publishVLANCheckpointFile(ctx, req.Root, report); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("older file could replace newer: %v", err)
	}
	if _, err := VerifyVLANCheckpoint(ctx, req); err == nil {
		t.Fatal("superseded transaction still resolves as the active file")
	}
}

func TestVLANCheckpointInterruptedStagingPreservesVerifiedCheckpoint(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for the isolated owner-only offline checkpoint fixture")
	}
	source, bindings := vlanCheckpointFixture(t)
	report, err := projectVLANCheckpoint(source, checkpointFixtureBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	req := VLANCheckpointRequest{Root: privateBackupPairRoot(t), Generation: report.Manifest.InstanceGeneration, TransactionID: report.Manifest.TransactionID}
	ctx := t.Context()
	if _, err := publishVLANCheckpointFile(ctx, req.Root, report); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(req.Root, checkpointPendingName)
	bytesBeforeFailure := []byte("{incomplete publication")
	if err := os.WriteFile(pending, bytesBeforeFailure, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireGuard(filepath.Join(req.Root, checkpointFileName))
	if err != nil {
		t.Fatal(err)
	}
	_, busy := VerifyVLANCheckpoint(ctx, req)
	lock.close()
	if busy == nil {
		t.Fatal("verification did not honor the publisher lock")
	}
	got, err := VerifyVLANCheckpoint(ctx, req)
	held, readErr := os.ReadFile(pending)
	if err != nil || readErr != nil || got.SHA256 != report.SHA256 || !bytes.Equal(held, bytesBeforeFailure) {
		t.Fatalf("interrupted staging altered or blocked the prior verified checkpoint: %v %v", err, readErr)
	}
	if _, err := publishVLANCheckpointFile(ctx, req.Root, report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(pending); !os.IsNotExist(err) {
		t.Fatalf("publisher did not clear the exact interrupted slot: %v", err)
	}
	if got, err := VerifyVLANCheckpoint(ctx, req); err != nil || got.SHA256 != report.SHA256 {
		t.Fatalf("idempotent publication failed to preserve the prior checkpoint: %v", err)
	}
}
