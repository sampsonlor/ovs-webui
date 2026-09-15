// Package secret provides consumer-bound, versioned envelopes. It exposes no
// network read-secret operation; only the owning service can open its partition.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"regexp"
)

const Provider = "local-aes256gcm"
const TLS = "webd-tls"
const Session = "webd-session"
const Privileged = "mgrd-privileged"
const MaximumBytes = 192 << 10

type unavailable struct{}

func (unavailable) Error() string        { return "SECRET_UNAVAILABLE" }
func (unavailable) RepositoryRejection() {}

var ErrUnavailable error = unavailable{}
var token = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)

// Value cannot accidentally serialize into ordinary JSON, fmt or slog output.
// Consumers must explicitly unwrap it at their private execution boundary.
type Value struct{ raw []byte }

func NewValue(raw []byte) Value            { return Value{append([]byte(nil), raw...)} }
func (v Value) Bytes() []byte              { return append([]byte(nil), v.raw...) }
func (Value) String() string               { return "[REDACTED]" }
func (Value) GoString() string             { return "[REDACTED]" }
func (Value) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

type Envelope struct {
	Format     int    `json:"format"`
	Provider   string `json:"provider"`
	Partition  string `json:"partition"`
	ID         string `json:"id"`
	Purpose    string `json:"purpose"`
	KeyVersion int    `json:"key_version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type Ring struct {
	Active int
	Keys   map[int][]byte `json:"-"`
}

func (Ring) String() string       { return "[REDACTED KEYRING]" }
func (Ring) GoString() string     { return "[REDACTED KEYRING]" }
func (Ring) LogValue() slog.Value { return slog.StringValue("[REDACTED KEYRING]") }
func NewRing(active int, keys map[int][]byte) (Ring, error) {
	if active < 1 || active > 16 || len(keys[active]) != 32 || len(keys) > 16 {
		return Ring{}, ErrUnavailable
	}
	r := Ring{Active: active, Keys: map[int][]byte{}}
	for version, key := range keys {
		if version < 1 || version > 16 || len(key) != 32 {
			return Ring{}, ErrUnavailable
		}
		r.Keys[version] = append([]byte(nil), key...)
	}
	return r, nil
}
func valid(partition, id, purpose string) bool {
	return (partition == TLS || partition == Session || partition == Privileged) && token.MatchString(id) && token.MatchString(purpose)
}
func associated(database string, e Envelope) []byte {
	b, _ := json.Marshal([]any{"ovs-secret", database, e.Format, e.Provider, e.Partition, e.ID, e.Purpose, e.KeyVersion})
	return b
}
func (r Ring) Seal(database, partition, id, purpose string, value Value) ([]byte, error) {
	if !token.MatchString(database) || !valid(partition, id, purpose) || len(value.raw) > MaximumBytes || len(r.Keys[r.Active]) != 32 {
		return nil, ErrUnavailable
	}
	e := Envelope{Format: 2, Provider: Provider, Partition: partition, ID: id, Purpose: purpose, KeyVersion: r.Active, Nonce: make([]byte, 12)}
	if _, err := rand.Read(e.Nonce); err != nil {
		return nil, ErrUnavailable
	}
	block, err := aes.NewCipher(r.Keys[r.Active])
	if err != nil {
		return nil, ErrUnavailable
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	e.Ciphertext = g.Seal(nil, e.Nonce, value.raw, associated(database, e))
	return json.Marshal(e)
}
func (r Ring) Open(database, partition, id, purpose string, blob []byte) (Value, error) {
	var e Envelope
	if len(blob) > 2*MaximumBytes || json.Unmarshal(blob, &e) != nil || e.Format != 2 || e.Provider != Provider || e.Partition != partition || e.ID != id || e.Purpose != purpose || !valid(partition, id, purpose) || len(r.Keys[e.KeyVersion]) != 32 || len(e.Nonce) != 12 {
		return Value{}, ErrUnavailable
	}
	block, err := aes.NewCipher(r.Keys[e.KeyVersion])
	if err != nil {
		return Value{}, ErrUnavailable
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return Value{}, ErrUnavailable
	}
	plain, err := g.Open(nil, e.Nonce, e.Ciphertext, associated(database, e))
	if err != nil || len(plain) > MaximumBytes {
		return Value{}, ErrUnavailable
	}
	return NewValue(plain), nil
}
func (r Ring) Digest(version int, purpose string, body []byte) (string, error) {
	if len(r.Keys[version]) != 32 || !token.MatchString(purpose) {
		return "", ErrUnavailable
	}
	h := hmac.New(sha256.New, r.Keys[version])
	h.Write([]byte("ovs-secret-fingerprint/" + purpose + "/"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}
