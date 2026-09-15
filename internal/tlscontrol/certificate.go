package tlscontrol

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

func KeyPair(certPEM []byte, key secret.Value, host string, roots *x509.CertPool, now time.Time, bootstrap bool) (tls.Certificate, Descriptor, error) {
	return keyPair(certPEM, key, host, roots, now, bootstrap, RecoveryWindow)
}

// StoredKeyPair validates an existing identity through its actual expiry. The
// admission lifetime floor for a new trial must not expire a running identity
// two minutes early merely because webd restarted.
func StoredKeyPair(certPEM []byte, key secret.Value, host string, roots *x509.CertPool, now time.Time, bootstrap bool) (tls.Certificate, Descriptor, error) {
	return keyPair(certPEM, key, host, roots, now, bootstrap, 0)
}
func keyPair(certPEM []byte, key secret.Value, host string, roots *x509.CertPool, now time.Time, bootstrap bool, minimumLifetime time.Duration) (tls.Certificate, Descriptor, error) {
	fail := func() (tls.Certificate, Descriptor, error) {
		return tls.Certificate{}, Descriptor{}, apitypes.Fail(422, "TLS_CANDIDATE_INVALID")
	}
	if len(certPEM) < 1 || len(certPEM) > 65536 || len(key.Bytes()) > 65536 || host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\ \r\n\t") {
		return fail()
	}
	rest := certPEM
	count := 0
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, tail := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return fail()
		}
		count++
		if count > 8 {
			return fail()
		}
		rest = tail
	}
	pair, err := tls.X509KeyPair(certPEM, key.Bytes())
	if err != nil || len(pair.Certificate) != count {
		return fail()
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA || leaf.VerifyHostname(host) != nil || now.Before(leaf.NotBefore) || !leaf.NotAfter.After(now.Add(minimumLifetime)) {
		return fail()
	}
	switch public := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		if public.N.BitLen() < 2048 || public.N.BitLen() > 8192 {
			return fail()
		}
	case *ecdsa.PublicKey:
		if public.Curve != elliptic.P256() && public.Curve != elliptic.P384() && public.Curve != elliptic.P521() {
			return fail()
		}
	case ed25519.PublicKey:
	default:
		return fail()
	}
	if bootstrap {
		if len(pair.Certificate) != 1 || leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) != nil {
			return fail()
		}
	} else {
		intermediates := x509.NewCertPool()
		for _, raw := range pair.Certificate[1:] {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return fail()
			}
			intermediates.AddCert(cert)
		}
		if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return fail()
		}
	}
	pair.Leaf = leaf
	sum := sha256.Sum256(leaf.Raw)
	return pair, Descriptor{Fingerprint: hex.EncodeToString(sum[:]), Hostname: host, NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC()}, nil
}
func Bootstrap(host string, now time.Time) ([]byte, secret.Value, error) {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\ \r\n\t*") {
		return nil, secret.Value{}, apitypes.Fail(422, "TLS_HOSTNAME_INVALID")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, secret.Value{}, secret.ErrUnavailable
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, secret.Value{}, secret.ErrUnavailable
	}
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "OVS WebUI bootstrap"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if ip := net.ParseIP(host); ip != nil {
		leaf.IPAddresses = []net.IP{ip}
	} else {
		leaf.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		return nil, secret.Value{}, secret.ErrUnavailable
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, secret.Value{}, secret.ErrUnavailable
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), secret.NewValue(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), nil
}
