package mirror

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorBuildProfilesRequireExactReviewedComponents(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		data, err := profiles.ReadFile("build-profile-" + version + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var p BuildProfile
		if json.Unmarshal(data, &p) != nil || len(p.Schemas) == 0 {
			t.Fatal("invalid profile")
		}
		h := sha256.Sum256(data)
		digest := hex.EncodeToString(h[:])
		for _, variant := range []string{"fixed", "witness-enabled", "witness-sanitized"} {
			if !LookupBuildProfile(digest, p.Schemas[0], variant) {
				t.Fatal("reviewed profile rejected", version, variant)
			}
		}
		for _, bad := range []struct{ profile, schema, variant string }{{version, p.Schemas[0], "fixed"}, {strings.ToUpper(digest), p.Schemas[0], "fixed"}, {digest, "unreviewed-schema", "fixed"}, {digest, p.Schemas[0], "default"}, {digest, p.Schemas[0], ""}} {
			if LookupBuildProfile(bad.profile, bad.schema, bad.variant) {
				t.Fatal("unqualified build enrolled", bad)
			}
		}
		file := filepath.Join("../../extensions/ovs-mirror-delete", version, "build-profile.json")
		reviewed, err := os.ReadFile(file)
		if err != nil || string(reviewed) != string(data) {
			t.Fatal("embedded profile diverged from reviewed source", file)
		}
	}
}
