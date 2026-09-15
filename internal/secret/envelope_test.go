package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func testRing(t *testing.T) Ring {
	t.Helper()
	r, err := NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{19}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestEnvelopeBindsConsumerIdentityPurposeVersionAndDatabase(t *testing.T) {
	ring := testRing(t)
	plain := NewValue([]byte("synthetic-reusable-secret-35"))
	blob, err := ring.Seal("database-a", TLS, "identity-a", "tls-identity", plain)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := ring.Seal("database-a", TLS, "identity-a", "tls-identity", plain)
	if bytes.Equal(blob, second) {
		t.Fatal("reused nonce")
	}
	value, err := ring.Open("database-a", TLS, "identity-a", "tls-identity", blob)
	if err != nil || !bytes.Equal(value.Bytes(), plain.Bytes()) {
		t.Fatal("round trip", err)
	}
	for _, tc := range []struct{ name, db, partition, id, purpose string }{
		{"database", "database-b", TLS, "identity-a", "tls-identity"}, {"consumer", "database-a", Privileged, "identity-a", "tls-identity"},
		{"id", "database-a", TLS, "identity-b", "tls-identity"}, {"purpose", "database-a", TLS, "identity-a", "password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ring.Open(tc.db, tc.partition, tc.id, tc.purpose, blob); err == nil {
				t.Fatal("ciphertext transplant accepted")
			}
		})
	}
	for _, field := range []string{"format", "provider", "partition", "id", "purpose", "key_version", "nonce", "ciphertext"} {
		t.Run(field, func(t *testing.T) {
			var doc map[string]any
			_ = json.Unmarshal(blob, &doc)
			switch field {
			case "format", "key_version":
				doc[field] = 99
			default:
				doc[field] = "tampered"
			}
			changed, _ := json.Marshal(doc)
			if _, err := ring.Open("database-a", TLS, "identity-a", "tls-identity", changed); err == nil {
				t.Fatal("tamper accepted")
			}
		})
	}
	next, _ := NewRing(2, map[int][]byte{1: ring.Keys[1], 2: bytes.Repeat([]byte{20}, 32)})
	if _, err := next.Open("database-a", TLS, "identity-a", "tls-identity", blob); err != nil {
		t.Fatal(err)
	}
	missing, _ := NewRing(2, map[int][]byte{2: next.Keys[2]})
	if _, err := missing.Open("database-a", TLS, "identity-a", "tls-identity", blob); err == nil {
		t.Fatal("missing restore key accepted")
	}
	wrong, _ := NewRing(1, map[int][]byte{1: bytes.Repeat([]byte{21}, 32)})
	if _, err := wrong.Open("database-a", TLS, "identity-a", "tls-identity", blob); err == nil {
		t.Fatal("wrong key accepted")
	}
}
func TestSecretValuesAreRedactedAtFormattingBoundaries(t *testing.T) {
	raw := "synthetic-private-content-35"
	value := NewValue([]byte(raw))
	data, _ := json.Marshal(map[string]any{"value": value, "ring": testRing(t)})
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("event", "value", value, "ring", testRing(t))
	for _, out := range []string{fmt.Sprint(value), fmt.Sprintf("%#v", value), fmt.Sprint(testRing(t)), string(data), logs.String()} {
		if strings.Contains(out, raw) || strings.Contains(out, "19 19 19") {
			t.Fatal("secret formatting leaked")
		}
	}
	copied := value.Bytes()
	copied[0] = 'X'
	if string(value.Bytes()) != raw {
		t.Fatal("mutable value alias")
	}
}
