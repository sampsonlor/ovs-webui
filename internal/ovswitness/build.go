package ovswitness

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
)

//go:embed build-profile.json
var buildProfile []byte

// BuildProfileDigest pins the reviewed source/patch/registry and the two
// experimental build variants. A root-supplied manifest must name this profile;
// a version string or a command's existence cannot enroll another build.
func BuildProfileDigest() string {
	h := sha256.Sum256(buildProfile)
	return hex.EncodeToString(h[:])
}

func BuildSchemaDigest() string {
	var p struct {
		Schema string `json:"database_schema_digest"`
	}
	_ = json.Unmarshal(buildProfile, &p)
	return p.Schema
}
