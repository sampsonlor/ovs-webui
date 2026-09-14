package authn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

func Secret(prefix string) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system randomness unavailable")
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}
func Hash(value string) [32]byte { return sha256.Sum256([]byte(value)) }
func ValidSecret(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil && len(b) == 32 && value == prefix+base64.RawURLEncoding.EncodeToString(b)
}

// Versioned AEAD envelope; AAD binds consumer, cookie hash and database identity.
// Session and manager keys are separate files and never part of ordinary DB backup.
func Seal(key, plain, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{1}, nonce...)
	return a.Seal(out, nonce, plain, aad), nil
}
func Unseal(key, envelope, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(envelope) < 1+a.NonceSize()+a.Overhead() || envelope[0] != 1 {
		return nil, errors.New("SESSION_ENVELOPE_INVALID")
	}
	return a.Open(nil, envelope[1:1+a.NonceSize()], envelope[1+a.NonceSize():], aad)
}
