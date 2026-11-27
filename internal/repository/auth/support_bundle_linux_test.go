//go:build linux

package auth

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/supportbundle"
)

func TestSupportBundleRequiresExplicitPerSectionAuthority(t *testing.T) {
	current := authn.Claims{Capabilities: []string{"state.read", "inventory.read", "support_bundle.create"}}
	if err := requireSupportScope(current, []supportbundle.Kind{supportbundle.KindRuntime, supportbundle.KindOVS}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, requireSupportScope(current, []supportbundle.Kind{supportbundle.KindAudit}), "CAPABILITY_DENIED")
	wantCode(t, requireSupportScope(current, []supportbundle.Kind{supportbundle.KindOVS, supportbundle.KindOVS}), "INVALID_SUPPORT_BUNDLE_SCOPE")
}

func TestAuthSupportBundleReturnsOwnedExpiringVerifiedArchive(t *testing.T) {
	r, _ := fixture(t)
	r.WithSupportHealth(func() ipc.Health {
		return ipc.Health{AuthenticationReady: true, Components: []ipc.HealthComponent{{Key: "manager", State: "ready"}}}
	})
	admin := login(t, r, "admin", false)
	command := request(t, r, "POST", "/support-bundles",
		map[string]any{"include_kinds": []string{"health", "audit"}, "redact": true}, "")
	initial, err := r.ExecuteAuth(t.Context(), admin.Grant, command)
	if err != nil || initial.Receipt.Job == nil || initial.Receipt.Resource == nil || initial.Receipt.Resource.Kind != "artifact" {
		t.Fatalf("support archive has no original job: %+v, %v", initial, err)
	}
	id := initial.Receipt.Job.ID
	same, err := r.ExecuteAuth(t.Context(), admin.Grant, command)
	if err != nil || same.Receipt.Resource == nil || same.Receipt.Resource.ID != id {
		t.Fatalf("support archive replay changed identity: %+v, %v", same, err)
	}
	base := "/support-bundles/" + id
	metadata, err := r.ReadAuth(t.Context(), admin.Grant, authn.Query{Method: "GET", URI: base})
	if err != nil || metadata.Status != 200 {
		t.Fatalf("owned artifact metadata unavailable: %v, %s", err, metadata.Body)
	}
	var info supportBundleMetadata
	if err := json.Unmarshal(metadata.Body, &info); err != nil || info.ID != id || info.Parts != 1 || len(info.Manifest.Sections) != 2 {
		t.Fatalf("invalid metadata: %+v, %v", info, err)
	}
	part, err := r.ReadAuth(t.Context(), admin.Grant, authn.Query{Method: "GET", URI: base + "/parts?part=0"})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Data string `json:"data_base64"`
	}
	if err := json.Unmarshal(part.Body, &wire); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(wire.Data)
	digest := sha256.Sum256(raw)
	if err != nil || info.Digest != hex.EncodeToString(digest[:]) || len(raw) != info.Bytes {
		t.Fatal("artifact retrieval changed verified bytes")
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, file := range archive.File {
		files = append(files, file.Name)
	}
	slices.Sort(files)
	if !slices.Equal(files, []string{"audit.json", "health.json", "manifest.json"}) {
		t.Fatalf("archive crossed selected scope: %v", files)
	}
	if err := r.store.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE support_bundles SET expires_ms=? WHERE id=?", time.Now().Add(-time.Minute).UnixMilli(), id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = r.ReadAuth(t.Context(), admin.Grant, authn.Query{Method: "GET", URI: base})
	wantCode(t, err, "SUPPORT_BUNDLE_UNAVAILABLE")
}
