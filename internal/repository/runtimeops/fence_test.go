package runtimeops

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	_ "modernc.org/sqlite"
)

func TestRuntimeRestartAdmissionMutuallyExcludesSafetyAndTLS(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	execute := func(statement string) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(err error, code string) {
		t.Helper()
		var problem *apitypes.Problem
		if !errors.As(err, &problem) || problem.Code != code {
			t.Fatalf("want %s, got %v", code, err)
		}
	}
	execute(`CREATE TABLE runtime_service_restart(singleton INTEGER PRIMARY KEY);
CREATE TABLE safe_applies(state TEXT NOT NULL);
CREATE TABLE tls_authority(singleton INTEGER PRIMARY KEY,trial_id TEXT NOT NULL);
INSERT INTO tls_authority VALUES(1,'');`)
	for _, state := range []string{"preparing", "awaiting-confirmation", "rollback-conflict", "recovery-required"} {
		if _, err := db.ExecContext(t.Context(), "INSERT INTO safe_applies VALUES(?)", state); err != nil {
			t.Fatal(err)
		}
		expect(RequireRestartAdmission(t.Context(), db), "MANAGEMENT_PATH_PROTECTED")
		execute("DELETE FROM safe_applies")
	}
	execute("INSERT INTO safe_applies VALUES('confirmed'),('rolled-back'),('not-committed')")
	if err := RequireRestartAdmission(t.Context(), db); err != nil {
		t.Fatalf("settled history prevented restart: %v", err)
	}
	execute("UPDATE tls_authority SET trial_id='trial' WHERE singleton=1")
	expect(RequireRestartAdmission(t.Context(), db), "TLS_RECOVERY_IN_PROGRESS")
	execute("UPDATE tls_authority SET trial_id='' WHERE singleton=1")
	execute("INSERT INTO runtime_service_restart VALUES(1)")
	expect(RequireNoRestart(t.Context(), db), "MANAGEMENT_RESTART_IN_PROGRESS")
	expect(RequireRestartAdmission(t.Context(), db), "MANAGEMENT_RESTART_IN_PROGRESS")
}
