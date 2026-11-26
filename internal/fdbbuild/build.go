// Package fdbbuild admits exact reviewed optional native FDB build profiles.
package fdbbuild

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
)

//go:embed build-profile-fdb-*.json
var profiles embed.FS

type Profile struct {
	Version       string
	Digest        string
	Schema        string
	MirrorProfile string
}

// Lookup accepts only the exact combined STP, Mirror and FDB profile digest.
func Lookup(digest string) (Profile, bool) {
	for _, version := range []string{"3.3.9", "3.7.1", "4.0.0"} {
		data, err := profiles.ReadFile("build-profile-fdb-" + version + ".json")
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		hash := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(hash[:]) != digest {
			continue
		}
		var p struct {
			Format        string `json:"format"`
			ABI           string `json:"abi"`
			Runtime       string `json:"runtime"`
			Schema        string `json:"database_schema_digest"`
			MirrorProfile string `json:"native_mirror_profile_sha256"`
		}
		if json.Unmarshal(data, &p) == nil && p.Format == "ovs-webui.fdb-build-profile.v1" &&
			p.ABI == "ovs-webui.fdb-sample.v1" && p.Runtime == version && hexDigest(p.Schema) && hexDigest(p.MirrorProfile) {
			return Profile{Version: version, Digest: digest, Schema: p.Schema, MirrorProfile: p.MirrorProfile}, true
		}
	}
	return Profile{}, false
}

func hexDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && hex.EncodeToString(data) == value
}
