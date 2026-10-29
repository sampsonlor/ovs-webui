package mirror

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
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
		// Inventory seals exact raw get_schema JSON. The STP witness uses a
		// distinct native-codec digest; those encodings must not be conflated.
		wire, err := os.ReadFile("testdata-schema-" + version + ".json")
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(wire))
		decoder.UseNumber()
		var raw map[string]any
		if decoder.Decode(&raw) != nil {
			t.Fatal("native schema fixture")
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		rawHash := sha256.Sum256(encoded)
		if !LookupBuildProfile(digest, hex.EncodeToString(rawHash[:]), "fixed") {
			t.Fatal("actual raw native schema not enrolled", version)
		}
		var codec native.DatabaseSchema
		if json.Unmarshal(wire, &codec) != nil {
			t.Fatal("schema codec")
		}
		normalized, err := json.Marshal(codec)
		if err != nil {
			t.Fatal(err)
		}
		codecHash := sha256.Sum256(normalized)
		if codecHash == rawHash || LookupBuildProfile(digest, hex.EncodeToString(codecHash[:]), "fixed") {
			t.Fatal("raw and STP codec schema qualification conflated", version)
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
