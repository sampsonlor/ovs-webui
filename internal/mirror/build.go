package mirror

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// Profiles enroll reviewed source components, not arbitrary version strings or
// binaries. A root-provisioned manifest must separately pin the actual binary.
//
//go:embed build-profile-*.json
var profiles embed.FS

type BuildProfile struct {
	Digest  string
	Schemas []string `json:"allowed_database_schemas"`
}

func LookupBuildProfile(digest, schema, variant string) bool {
	if !slices.Contains([]string{"fixed", "witness-enabled", "witness-sanitized"}, variant) {
		return false
	}
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		data, err := profiles.ReadFile("build-profile-" + version + ".json")
		var p BuildProfile
		hash := sha256.Sum256(data)
		if err == nil && hex.EncodeToString(hash[:]) == digest && json.Unmarshal(data, &p) == nil && slices.Contains(p.Schemas, schema) {
			return true
		}
	}
	return false
}
