package fdbbuild

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCombinedProfilesPinReviewedSource(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		t.Run(version, func(t *testing.T) {
			name := "build-profile-fdb-" + version + ".json"
			data, err := profiles.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var metadata struct {
				Files  map[string]string `json:"files"`
				Parent string            `json:"parent_profile_sha256"`
				Inputs map[string]string `json:"witness_inputs"`
			}
			if json.Unmarshal(data, &metadata) != nil || len(metadata.Files) != 5 || len(metadata.Inputs) != 3 {
				t.Fatal("incomplete FDB build profile")
			}
			digest := pinnedDigest(data)
			qualified, ok := Lookup(digest)
			if !ok || qualified.Version != version || qualified.Digest != digest {
				t.Fatal("exact FDB build profile was rejected")
			}
			for _, invalid := range []string{metadata.Parent, qualified.MirrorProfile, strings.ToUpper(digest), version} {
				if _, ok := Lookup(invalid); ok {
					t.Fatal("other profile admitted", invalid)
				}
			}
			metadata.Files["internal/ovswitness/build-profile-mirror-"+version+".json"] = metadata.Parent
			metadata.Files["internal/mirror/build-profile-"+version+".json"] = qualified.MirrorProfile
			for path, expected := range metadata.Files {
				file, err := os.ReadFile(filepath.Join("../..", filepath.FromSlash(path)))
				if err != nil || pinnedDigest(file) != expected {
					t.Fatal("reviewed FDB source changed", path, err)
				}
			}
		})
	}
}

func pinnedDigest(data []byte) string {
	hash := sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	return hex.EncodeToString(hash[:])
}
