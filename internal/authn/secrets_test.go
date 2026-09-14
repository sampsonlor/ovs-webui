package authn

import (
	"bytes"
	"testing"
)

func TestSecretDomainSeparationAndEnvelopeBinding(t *testing.T) {
	secret := Secret("ovsg_")
	if !ValidSecret(secret, "ovsg_") || ValidSecret(secret, "ovst_") || ValidSecret(secret+"=", "ovsg_") {
		t.Fatal("credential format confusion")
	}
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte(secret)
	aad := []byte("webd/session/one")
	first, err := Seal(key, plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seal(key, plain, aad)
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("nonce reused")
	}
	if out, err := Unseal(key, first, aad); err != nil || !bytes.Equal(out, plain) {
		t.Fatal("decrypt failed")
	}
	for _, badAAD := range [][]byte{[]byte("manager/session/one"), []byte("webd/session/two")} {
		if _, err = Unseal(key, first, badAAD); err == nil {
			t.Fatal("envelope transplanted")
		}
	}
	first[len(first)-1] ^= 1
	if _, err = Unseal(key, first, aad); err == nil {
		t.Fatal("tamper accepted")
	}
}
