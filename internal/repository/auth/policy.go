// Package auth owns manager.db authentication and security admission. All
// credential, policy, revocation and dispatch decisions pass the same gate.
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"sort"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

// Public v1 uses transport aliases; manager authority persists only these
// canonical capability codes from the accepted implementation design.
var aliases = map[string]string{
	"state.read": "state.read", "workspace.read": "workspace.read", "workspace.write": "workspace.write",
	"config.stage": "workspace.write", "config.validate": "configuration.validate", "config.read": "configuration.read",
	"config.apply": "configuration.apply", "config.decide": "configuration.confirm", "request.read": "requests.read",
	"job.read": "jobs.read", "job.cancel": "jobs.cancel", "event.read": "events.read", "audit.read": "audit.read",
	"user.read": "access.users.manage", "user.manage": "access.users.manage", "role.read": "access.roles.manage", "role.manage": "access.roles.manage",
	"token.read": "tokens.self", "token.manage": "tokens.self", "access.read": "access.aaa.manage", "access.configure": "access.aaa.manage",
	"certificate.read": "access.tls.manage", "certificate.manage": "access.tls.manage", "backup.read": "backup.create", "backup.manage": "backup.create",
	"recovery.read": "configuration.read", "recovery.manage": "backup.restore", "diagnostic.read": "diagnostics.read", "diagnostic.run": "diagnostics.active",
	"capture.run": "diagnostics.disruptive", "support.export": "support_bundle.create", "lifecycle.manage": "ovs.lifecycle.manage",
	"config.export": "configuration.read", "artifact.read": "artifacts.read", "artifact.write": "artifacts.write", "openflow.manage": "openflow.local.write",
}
var readCapabilities = []string{"inventory.read", "state.read", "capabilities.read", "jobs.read", "events.read", "audit.read", "workspace.read", "configuration.read", "requests.read", "diagnostics.read", "artifacts.read"}
var securityCapabilities = []string{"access.users.manage", "access.roles.manage", "access.aaa.manage", "access.tokens.manage", "access.tls.manage"}
var networkCapabilities = []string{"workspace.write", "configuration.validate", "configuration.apply", "configuration.confirm", "configuration.rollback", "ovs.port.vlan.write", "diagnostics.active", "diagnostics.disruptive", "support_bundle.create", "backup.create", "backup.restore", "configuration.import", "management_network.write", "ovs.lifecycle.manage", "jobs.cancel", "artifacts.write"}
var managementCapabilities = []string{"management.policy.write", "management.debug.write", "management.services.control", "openflow.local.write"}

func unique(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return slices.Compact(out)
}
func allCapabilities() []string {
	out := append([]string{}, readCapabilities...)
	out = append(out, networkCapabilities...)
	out = append(out, securityCapabilities...)
	out = append(out, managementCapabilities...)
	return unique(out)
}
func Templates() map[string][]string {
	return map[string][]string{
		"Reader":        unique(readCapabilities),
		"Operator":      unique(append(append([]string{}, readCapabilities...), "diagnostics.active", "jobs.cancel")),
		"NetworkAdmin":  unique(append(append([]string{}, readCapabilities...), networkCapabilities...)),
		"SecurityAdmin": unique(append(append([]string{}, readCapabilities...), securityCapabilities...)),
		"Administrator": allCapabilities(),
	}
}
func validateCapabilities(values []string) error {
	if len(values) > 128 || len(unique(values)) != len(values) {
		return apitypes.Fail(422, "INVALID_CAPABILITIES")
	}
	for _, c := range values {
		if !slices.Contains(allCapabilities(), c) {
			return apitypes.Fail(422, "UNKNOWN_CAPABILITY")
		}
	}
	return nil
}
func subset(values, ceiling []string) bool {
	for _, c := range values {
		if !slices.Contains(ceiling, c) {
			return false
		}
	}
	return true
}
func intersection(values, ceiling []string) []string {
	out := []string{}
	for _, c := range values {
		if slices.Contains(ceiling, c) {
			out = append(out, c)
		}
	}
	return unique(out)
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func capabilities(ctx context.Context, q querier, principal string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT r.capabilities FROM roles r JOIN auth_roles a ON a.role_id=r.id JOIN principal_roles p ON p.role_id=r.id WHERE p.principal_id=?", principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var blob []byte
		var caps []string
		if err = rows.Scan(&blob); err != nil {
			return nil, err
		}
		if json.Unmarshal(blob, &caps) != nil || validateCapabilities(caps) != nil {
			return nil, apitypes.Fail(503, "AUTH_POLICY_INVALID")
		}
		out = append(out, caps...)
	}
	return unique(out), rows.Err()
}
func require(c authn.Claims, capability string, elevated bool, now int64) error {
	if capability == "session.read" || capability == "session.end" {
		if c.CredentialKind != "grant" {
			return apitypes.Fail(403, "BROWSER_SESSION_REQUIRED")
		}
		return nil
	}
	canonical, ok := aliases[capability]
	if !ok {
		canonical = capability
		ok = slices.Contains(allCapabilities(), capability)
	}
	if !ok {
		return apitypes.Fail(403, "CAPABILITY_UNKNOWN")
	}
	if canonical != "tokens.self" && !slices.Contains(c.Capabilities, canonical) {
		return apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	if elevated && (c.CredentialKind != "grant" || c.ElevatedUntil.Unix() <= now) {
		return apitypes.Fail(403, "REAUTHENTICATION_REQUIRED")
	}
	return nil
}
func checkRefs(ctx context.Context, q querier, c authn.Claims, refs []apitypes.Ref) error {
	if len(refs) > 32 {
		return apitypes.Fail(422, "INVALID_RESOURCE_SCOPE")
	}
	for _, ref := range refs {
		if (ref.Kind != "request" && !apitypes.ManagementID(ref.ID)) || (ref.Kind == "request" && !apitypes.UUID(ref.ID)) {
			return apitypes.Fail(422, "INVALID_RESOURCE_SCOPE")
		}
		switch ref.Kind {
		case "request", "job":
			var count int
			query := "SELECT count(*) FROM api_receipts WHERE principal_id=? AND request_id=?"
			if ref.Kind == "job" {
				query = "SELECT count(*) FROM api_receipts WHERE principal_id=? AND json_extract(receipt,'$.job_ref.id')=?"
			}
			if err := q.QueryRowContext(ctx, query, c.PrincipalID, ref.ID).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				return apitypes.Fail(404, "NOT_FOUND")
			}
		case "token":
			var owner string
			if err := q.QueryRowContext(ctx, "SELECT t.principal_id FROM api_tokens t JOIN auth_tokens a ON a.token_hash=t.token_hash WHERE a.id=?", ref.ID).Scan(&owner); err != nil {
				return apitypes.Fail(404, "NOT_FOUND")
			}
			if owner != c.PrincipalID && !slices.Contains(c.Capabilities, "access.tokens.manage") {
				return apitypes.Fail(403, "RESOURCE_DENIED")
			}
		case "user":
			if err := require(c, "user.read", false, 0); err != nil {
				return err
			}
		case "role":
			if err := require(c, "role.read", false, 0); err != nil {
				return err
			}
		default:
			// Field/ownership decisions belong to the real domain authority. Until it
			// is implemented, even Administrator cannot obtain a fabricated admission.
			return apitypes.Fail(503, "OBJECT_AUTHORITY_UNAVAILABLE")
		}
	}
	return nil
}
