package ovswitness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWitnessBuildProfilePinsReviewedInputs(t *testing.T) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		name := "build-profile.json"
		if version != "3.3.9" {
			name = "build-profile-" + version + ".json"
		}
		buildProfile, err := buildProfiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var profile struct {
			Files map[string]string `json:"files"`
		}
		p, ok := BuildProfile(version)
		if json.Unmarshal(buildProfile, &profile) != nil || len(profile.Files) != 8 || !ok || !hex64(p.Digest) || !hex64(p.Schema) {
			t.Fatal("invalid build profile")
		}
		for name, want := range profile.Files {
			data, err := os.ReadFile(filepath.Join("../..", filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
			h := sha256.Sum256(data)
			if hex.EncodeToString(h[:]) != want {
				t.Fatal("reviewed source changed", name)
			}
		}
	}
	columns := InputColumns()
	delete(columns, "Bridge")
	columns["Port"][0] = "other"
	if len(InputColumns()) != 4 || InputColumns()["Port"][0] == "other" {
		t.Fatal("registry is mutable")
	}
}

func TestWitnessBuildProfilesAreClosedAndSchemaBound(t *testing.T) {
	seen := map[string]bool{}
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		p, ok := BuildProfile(version)
		q, found := LookupBuildProfile(p.Digest)
		if !ok || !found || q != p || p.Version != version || seen[p.Digest] {
			t.Fatal("profile conflation", version)
		}
		seen[p.Digest] = true
		for _, bad := range []string{strings.ToUpper(p.Digest), p.Digest + "0", p.Digest[:63], " " + p.Digest} {
			if got, ok := LookupBuildProfile(bad); ok || got != (QualifiedBuildProfile{}) {
				t.Fatal("nonexact profile", bad)
			}
		}
	}
	for _, bad := range []string{"", "3.7", "v3.7.1", "4.0.1", "../build-profile.json", strings.Repeat("0", 64)} {
		if got, ok := BuildProfile(bad); ok || got != (QualifiedBuildProfile{}) {
			t.Fatal("unknown version", bad)
		}
		if got, ok := LookupBuildProfile(bad); ok || got != (QualifiedBuildProfile{}) {
			t.Fatal("unknown digest", bad)
		}
	}
	a, _ := BuildProfile("3.3.9")
	b, _ := BuildProfile("3.7.1")
	c, _ := BuildProfile("4.0.0")
	if a.Schema == b.Schema || b.Schema != c.Schema || b.Digest == c.Digest {
		t.Fatal("independent source and schema pins lost")
	}
}
