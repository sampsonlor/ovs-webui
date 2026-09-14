package tlscontrol

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"math/big"
	"testing"
	"time"
)

func TestCertificateValidationRejectsUntrustedWrongHostExpiredAndMismatchedKeys(t *testing.T) {
	now := time.Now()
	cert, key, err := Bootstrap("console.example", now)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(cert)
	pair, descriptor, err := KeyPair(cert, key, "console.example", roots, now, false)
	if err != nil || len(pair.Certificate) != 1 || len(descriptor.Fingerprint) != 64 {
		t.Fatal(err)
	}
	nearExpiry := pair.Leaf.NotAfter.Add(-time.Minute)
	if _, _, err := KeyPair(cert, key, "console.example", roots, nearExpiry, false); err == nil {
		t.Fatal("short-lived candidate admitted")
	}
	if _, _, err := StoredKeyPair(cert, key, "console.example", roots, nearExpiry, false); err != nil {
		t.Fatal("stored identity expired before actual expiry", err)
	}
	_, other, _ := Bootstrap("console.example", now)
	for _, tc := range []struct {
		name  string
		cert  []byte
		key   secret.Value
		host  string
		roots *x509.CertPool
		now   time.Time
	}{
		{"untrusted chain", cert, key, "console.example", x509.NewCertPool(), now}, {"wrong SAN", cert, key, "other.example", roots, now},
		{"mismatched key", cert, other, "console.example", roots, now}, {"expired", cert, key, "console.example", roots, now.Add(100 * 24 * time.Hour)},
		{"not yet valid", cert, key, "console.example", roots, now.Add(-24 * time.Hour)}, {"trailing garbage", append(append([]byte{}, cert...), []byte("malformed")...), key, "console.example", roots, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := KeyPair(tc.cert, tc.key, tc.host, tc.roots, tc.now, false); err == nil {
				t.Fatal("invalid TLS identity accepted")
			}
		})
	}
}
func TestCertificateValidatesChainAndServerUsage(t *testing.T) {
	now := time.Now()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	root, _ := x509.ParseCertificate(caDER)
	roots := x509.NewCertPool()
	roots.AddCert(root)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"console.example"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	test := func(key any, public any, want bool) {
		t.Helper()
		der, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, caKey)
		if err != nil {
			t.Fatal(err)
		}
		priv, _ := x509.MarshalPKCS8PrivateKey(key)
		_, _, err = KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), secret.NewValue(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})), "console.example", roots, now, false)
		if (err == nil) != want {
			t.Fatalf("valid=%v error=%v", want, err)
		}
	}
	test(leafKey, &leafKey.PublicKey, true)
	leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	test(leafKey, &leafKey.PublicKey, false)
	leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	test(weak, &weak.PublicKey, false)
}
func TestTLSLeaseFailsClosedAcrossDeadlineBootAndClockChanges(t *testing.T) {
	c := Clock{"boot-a", int64(time.Hour), time.Unix(200000, 0)}
	s := State{TrialID: "candidate", BootID: c.BootID, StartedNS: c.NS, DeadlineNS: c.NS + int64(RecoveryWindow), DeadlineWall: c.Wall.Add(RecoveryWindow).Unix()}
	if s.Expired(c) {
		t.Fatal("fresh lease expired")
	}
	for _, changed := range []Clock{{"boot-b", c.NS, c.Wall}, {"", c.NS, c.Wall}, {c.BootID, s.DeadlineNS, c.Wall}, {c.BootID, c.NS - 1, c.Wall}, {c.BootID, c.NS, c.Wall.Add(-time.Second)}, {c.BootID, c.NS, c.Wall.Add(RecoveryWindow)}} {
		if !s.Expired(changed) {
			t.Fatal("unsafe lease renewed", changed)
		}
	}
	if (State{}).Expired(Clock{}) {
		t.Fatal("empty trial expired")
	}
}
