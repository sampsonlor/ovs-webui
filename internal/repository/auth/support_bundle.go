package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository/evidence"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/supportbundle"
)

const (
	supportOperation    = "createSupportBundle"
	supportCapability   = "support_bundle.create"
	supportMaxArtifacts = 32
	supportMaxOwner     = 3
	supportPartBytes    = 24 << 10
)

var supportScopeCaps = map[supportbundle.Kind]string{
	supportbundle.KindRuntime: "state.read", supportbundle.KindHealth: "state.read",
	supportbundle.KindOVS: "inventory.read", supportbundle.KindEvents: "events.read", supportbundle.KindAudit: "audit.read",
}

func requireSupportScope(c authn.Claims, kinds []supportbundle.Kind) error {
	if err := supportbundle.ValidateKinds(kinds); err != nil {
		return apitypes.Fail(422, "INVALID_SUPPORT_BUNDLE_SCOPE")
	}
	for _, kind := range kinds {
		if err := require(c, supportScopeCaps[kind], false, 0); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) WithSupportHealth(source func() ipc.Health) *Repository {
	r.supportHealth = source
	return r
}

func (r *Repository) createSupportBundle(ctx context.Context, credential string, input authn.Command) (apitypes.Result, error) {
	op, body, err := r.candidateCommand(input, supportOperation)
	if err != nil {
		return apitypes.Result{}, err
	}
	var wire struct {
		Scope  []supportbundle.Kind `json:"include_kinds"`
		Redact bool                 `json:"redact"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || !wire.Redact || supportbundle.ValidateKinds(wire.Scope) != nil {
		return apitypes.Result{}, apitypes.Fail(422, "INVALID_SUPPORT_BUNDLE_SCOPE")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	unlock, err := r.lock(ctx)
	if err != nil {
		return apitypes.Result{}, err
	}
	defer unlock()
	claims, err := r.InspectAuth(ctx, credential)
	if err != nil {
		return apitypes.Result{}, err
	}
	check := func(ctx context.Context, q querier) error {
		claims, err = r.checkOperation(ctx, q, credential, op, nil, false)
		if err != nil {
			return err
		}
		return requireSupportScope(claims, wire.Scope)
	}
	command := requests.Command{Principal: claims.PrincipalID, Credential: claims.CredentialID, Capability: supportCapability,
		Epoch: input.Epoch, Domain: "management", ID: input.RequestID, Operation: op.ID, Method: input.Method, URI: input.URI, Payload: body}
	return r.receipts.Execute(ctx, command, func(ctx context.Context) error {
		return r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error { return check(ctx, q) })
	}, func(ctx context.Context, tx *sql.Tx) (requests.Mutation, error) {
		out := requests.Mutation{Status: 202, Terminal: true, Body: json.RawMessage(`{}`)}
		if err := check(ctx, tx); err != nil {
			return out, err
		}
		now := r.now()
		if err := pruneSupportBundles(ctx, tx, now); err != nil {
			return out, err
		}
		var saved, owned int
		if err := tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(owner_id=?),0) FROM support_bundles", claims.PrincipalID).Scan(&saved, &owned); err != nil {
			return out, err
		}
		if saved >= supportMaxArtifacts || owned >= supportMaxOwner {
			return out, apitypes.Fail(429, "SUPPORT_BUNDLE_CAPACITY_REACHED")
		}
		snapshots := r.supportInputs(ctx, tx, wire.Scope)
		archive, err := supportbundle.Build(ctx, now, wire.Scope, snapshots)
		if err != nil {
			return out, apitypes.Fail(503, "SUPPORT_BUNDLE_UNAVAILABLE")
		}
		manifest, err := json.Marshal(archive.Manifest)
		if err != nil {
			return out, err
		}
		job, err := evidence.CreateJob(ctx, tx, evidence.Job{
			State: "succeeded", Business: "success", Reason: "support-bundle-published", Created: now,
		})
		if err != nil {
			return out, err
		}
		digest := sha256.Sum256(archive.Bytes)
		if _, err = tx.ExecContext(ctx, "INSERT INTO support_bundles(id,owner_id,expires_ms,digest,manifest,content) VALUES(?,?,?,?,?,?)",
			job.ID, claims.PrincipalID, archive.Manifest.ExpiresAt.UnixMilli(), hex.EncodeToString(digest[:]), manifest, archive.Bytes); err != nil {
			return out, err
		}
		out.Resource, out.Job = &apitypes.Ref{Kind: "artifact", ID: job.ID}, &apitypes.Ref{Kind: "job", ID: job.ID}
		if err := r.audit(ctx, tx, claims, op.ID, job.ID, "support-bundle-published", input.RequestID, out.Resource, out.Job); err != nil {
			return out, err
		}
		return out, r.touch(ctx, tx, credential, claims)
	})
}

func (r *Repository) supportInputs(ctx context.Context, tx *sql.Tx, scope []supportbundle.Kind) supportbundle.Inputs {
	var in supportbundle.Inputs
	var sample ipc.Health
	if r.supportHealth != nil && (slices.Contains(scope, supportbundle.KindRuntime) || slices.Contains(scope, supportbundle.KindHealth)) {
		sample = r.supportHealth()
	}
	for _, kind := range scope {
		switch kind {
		case supportbundle.KindRuntime:
			if r.supportHealth != nil {
				in.Runtime = &supportbundle.RuntimeSnapshot{Version: buildinfo.SoftwareVersion(), Architecture: runtime.GOARCH,
					Platform: runtime.GOOS, ConfigurationReady: sample.ConfigurationReady, AuthenticationReady: sample.AuthenticationReady}
			}
		case supportbundle.KindHealth:
			if r.supportHealth != nil {
				view := &supportbundle.HealthSnapshot{
					Components: []supportbundle.HealthComponent{{Key: "web", State: "unknown"}},
				}
				for _, component := range sample.Components {
					view.Components = append(view.Components, supportbundle.HealthComponent{
						Key: component.Key, State: component.State, ObservedAt: component.ObservedAt,
						Active: component.Active, Capacity: component.Capacity,
					})
				}
				in.Health = view
			}
		case supportbundle.KindOVS:
			if source, ok := r.inventory.(interface {
				SupportOVSSnapshot() *supportbundle.OVSSnapshot
			}); ok {
				in.OVS = source.SupportOVSSnapshot()
			}
		case supportbundle.KindEvents:
			in.Events = readSupportEvidence(ctx, tx, "event")
		case supportbundle.KindAudit:
			in.Audit = readSupportEvidence(ctx, tx, "audit")
		}
	}
	return in
}

func readSupportEvidence(ctx context.Context, q querier, collection string) *supportbundle.EvidencePage {
	// Read fixed scalar metadata only. The append path keeps full evidence
	// bodies, native names, actor references, and correlation IDs out of this query.
	rows, err := q.QueryContext(ctx, `SELECT operation,json_extract(document,'$.result'),created_at_ms
         FROM evidence_records WHERE collection=? ORDER BY created_at_ms DESC,id DESC LIMIT ?`, collection, supportbundle.MaxEvidenceRows+1)
	if err != nil {
		return nil
	}
	defer rows.Close()
	view := &supportbundle.EvidencePage{}
	for rows.Next() {
		if len(view.Rows) == supportbundle.MaxEvidenceRows {
			view.Truncated = true
			break
		}
		var row supportbundle.EvidenceRow
		var at int64
		if err := rows.Scan(&row.Operation, &row.Outcome, &at); err != nil {
			return nil
		}
		row.ObservedAt = time.UnixMilli(at).UTC()
		view.Rows = append(view.Rows, row)
	}
	if rows.Err() != nil {
		return nil
	}
	return view
}

type supportBundleMetadata struct {
	ID       string                 `json:"id"`
	Manifest supportbundle.Manifest `json:"manifest"`
	Digest   string                 `json:"sha256"`
	Bytes    int                    `json:"bytes"`
	Parts    int                    `json:"parts"`
}

func (r *Repository) getSupportBundle(ctx context.Context, tx *sql.Tx, claims authn.Claims, id string, part string) (any, error) {
	if err := require(claims, supportCapability, false, 0); err != nil {
		return nil, err
	}
	var info supportBundleMetadata
	var document []byte
	var expiry int64
	now := r.now()
	err := tx.QueryRowContext(ctx, `SELECT length(content),digest,manifest,expires_ms
          FROM support_bundles WHERE id=? AND owner_id=? AND expires_ms>?`, id, claims.PrincipalID, now.UnixMilli()).Scan(&info.Bytes, &info.Digest, &document, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apitypes.Fail(404, "SUPPORT_BUNDLE_UNAVAILABLE")
	}
	if err != nil {
		return nil, err
	}
	var scope []supportbundle.Kind
	if len(document) > 16384 || json.Unmarshal(document, &info.Manifest) != nil || info.Manifest.Version != 1 ||
		info.Manifest.Expired(now) || info.Manifest.ExpiresAt.UnixMilli() != expiry || info.Bytes < 1 || info.Bytes > supportbundle.MaxBundleBytes {
		return nil, apitypes.Fail(503, "SUPPORT_BUNDLE_UNAVAILABLE")
	}
	for _, section := range info.Manifest.Sections {
		scope = append(scope, section.Kind)
	}
	if err := requireSupportScope(claims, scope); err != nil {
		return nil, err
	}
	info.ID, info.Parts = id, (info.Bytes+supportPartBytes-1)/supportPartBytes
	if part == "" {
		return info, nil
	}
	index, err := strconv.Atoi(part)
	if err != nil || strconv.Itoa(index) != part || index < 0 || index >= info.Parts {
		return nil, apitypes.Fail(404, "SUPPORT_BUNDLE_PART_UNAVAILABLE")
	}
	var content []byte
	err = tx.QueryRowContext(ctx, "SELECT substr(content,?,?) FROM support_bundles WHERE id=? AND owner_id=?",
		index*supportPartBytes+1, supportPartBytes, id, claims.PrincipalID).Scan(&content)
	if err != nil {
		return nil, err
	}
	if len(content) != min(supportPartBytes, info.Bytes-index*supportPartBytes) {
		return nil, apitypes.Fail(503, "SUPPORT_BUNDLE_UNAVAILABLE")
	}
	return map[string]any{"id": id, "part": index, "data_base64": base64.StdEncoding.EncodeToString(content)}, nil
}

func pruneSupportBundles(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM support_bundles WHERE expires_ms<=?", now.UnixMilli())
	return err
}
