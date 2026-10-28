package ovswitness

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
)

//go:embed build-profile.json build-profile-3.7.1.json build-profile-4.0.0.json build-profile-mirror-*.json
var buildProfiles embed.FS

// QualifiedBuildProfile pins reviewed source/patch/registry bytes and the two
// experimental build variants. A root-supplied manifest must name its digest.
type QualifiedBuildProfile struct {
	Version string
	Digest  string
	Schema  string
}

// BuildProfile selects only a compiled, reviewed source version. Neither a
// filename, caller-supplied version string nor a manifest can enroll new bytes.
func BuildProfile(version string) (QualifiedBuildProfile, bool) {
	name := ""
	switch version {
	case "3.3.9":
		name = "build-profile.json"
	case "3.7.1", "4.0.0":
		name = "build-profile-" + version + ".json"
	default:
		return QualifiedBuildProfile{}, false
	}
	data, err := buildProfiles.ReadFile(name)
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	var p struct {
		Schema string `json:"database_schema_digest"`
	}
	if err != nil || json.Unmarshal(data, &p) != nil || !hex64(p.Schema) {
		return QualifiedBuildProfile{}, false
	}
	h := sha256.Sum256(data)
	return QualifiedBuildProfile{Version: version, Digest: hex.EncodeToString(h[:]), Schema: p.Schema}, true
}

// LookupBuildProfile requires the exact digest of a compiled profile. Shared
// database schemas do not make different upstream source profiles equivalent.
func LookupBuildProfile(digest string) (QualifiedBuildProfile, bool) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		p, ok := BuildProfile(version)
		if ok && p.Digest == digest {
			return p, true
		}
	}
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		data, err := buildProfiles.ReadFile("build-profile-mirror-" + version + ".json")
		var p struct {
			Schema string `json:"database_schema_digest"`
		}
		h := sha256.Sum256(data)
		if err == nil && hex.EncodeToString(h[:]) == digest && json.Unmarshal(data, &p) == nil && hex64(p.Schema) {
			return QualifiedBuildProfile{Version: version, Digest: digest, Schema: p.Schema}, true
		}
	}
	return QualifiedBuildProfile{}, false
}
