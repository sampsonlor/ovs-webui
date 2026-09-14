package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=65536,t=3,p=1$"

func validPassword(password string) bool { return len(password) >= 12 && len(password) <= 1024 }

// A versioned, bounded verifier. Unknown parameters never allocate arbitrary RAM.
func derive(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
}
func hashPassword(password string) ([]byte, error) {
	if !validPassword(password) {
		return nil, apitypes.Fail(422, "PASSWORD_POLICY_REJECTED")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	hash := derive(password, salt)
	return []byte(passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)), nil
}
func verifyPassword(password string, verifier []byte) bool {
	salt := make([]byte, 16)
	expected := make([]byte, 32)
	valid := false
	if strings.HasPrefix(string(verifier), passwordPrefix) {
		parts := strings.Split(strings.TrimPrefix(string(verifier), passwordPrefix), "$")
		if len(parts) == 2 {
			s, e1 := base64.RawStdEncoding.DecodeString(parts[0])
			h, e2 := base64.RawStdEncoding.DecodeString(parts[1])
			if e1 == nil && e2 == nil && len(s) == 16 && len(h) == 32 {
				salt = s
				expected = h
				valid = true
			}
		}
	}
	// Unknown and disabled principals incur the same bounded password work.
	actual := derive(password, salt)
	return subtle.ConstantTimeCompare(actual, expected) == 1 && valid
}
func (r *Repository) passwordSlot(ctx context.Context) (func(), error) {
	select {
	case r.passwords <- struct{}{}:
		return func() { <-r.passwords }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, apitypes.Fail(429, "AUTH_BUDGET_EXCEEDED")
	}
}
