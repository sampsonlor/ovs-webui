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
	var profile struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(buildProfile, &profile) != nil || len(profile.Files) != 8 || !hex64(BuildProfileDigest()) || !hex64(BuildSchemaDigest()) {
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
	columns := InputColumns()
	delete(columns, "Bridge")
	columns["Port"][0] = "other"
	if len(InputColumns()) != 4 || InputColumns()["Port"][0] == "other" {
		t.Fatal("registry is mutable")
	}
}
