//go:build linux

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"
)

func TestSpanningTreeExecutionCapabilityIsExplicitCurrentAndCeilingBound(t *testing.T) {
	for name, caps := range Templates() {
		if slices.Contains(caps, SpanningTreeExecutionCapability) {
			t.Fatal("builtin role silently acquired a reserved execution grant", name)
		}
	}
	r, _ := fixture(t)
	g := login(t, r, "admin", false)
	check := r.SpanningTreeExecutionAuthorizer(g.Grant)
	wantCode(t, check(testContext), "CAPABILITY_DENIED")
	set := func(caps []string) {
		t.Helper()
		if validateCapabilities(caps) != nil {
			t.Fatal("explicit capability not recognized")
		}
		body, _ := json.Marshal(caps)
		if err := r.store.Write(testContext, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE roles SET capabilities=? WHERE id IN (SELECT role_id FROM principal_roles WHERE principal_id=?)", body, g.Claims.PrincipalID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	caps := append(slices.Clone(g.Claims.Capabilities), SpanningTreeExecutionCapability)
	set(caps)
	wantCode(t, check(testContext), "CAPABILITY_DENIED") // old ceiling
	fresh := login(t, r, "admin", false)
	check = r.SpanningTreeExecutionAuthorizer(fresh.Grant)
	if err := check(testContext); err != nil {
		t.Fatal("explicit current capability denied", err)
	}
	for _, removed := range []string{SpanningTreeExecutionCapability, "configuration.read", "inventory.read", "workspace.write", "configuration.apply"} {
		set(slices.DeleteFunc(slices.Clone(caps), func(c string) bool { return c == removed }))
		wantCode(t, check(testContext), "CAPABILITY_DENIED")
		set(caps)
		if err := check(testContext); err != nil {
			t.Fatal("restored current authority denied", err)
		}
	}
}
