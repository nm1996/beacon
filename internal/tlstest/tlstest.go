// Package tlstest writes short-lived certificates for tests that need TLS
// files on disk: self-signed server certificates, and a small CA that issues
// server and client certificates for mTLS.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Certificate is a certificate and key written to a test's temporary
// directory.
type Certificate struct {
	// CertFile is the PEM certificate.
	CertFile string
	// KeyFile is the PEM private key.
	KeyFile string
	// Pool trusts the certificate's issuer, for peers that should verify it.
	Pool *x509.CertPool
	// KeyPair is the certificate and key, ready for a tls.Config.
	KeyPair tls.Certificate
}

// WriteCertificate creates a self-signed certificate valid for localhost,
// 127.0.0.1 and ::1, and writes it and its key as PEM files in a temporary
// directory that is removed when the test ends.
func WriteCertificate(test testing.TB) Certificate {
	test.Helper()

	key := newKey(test)
	template := newTemplate("beacon test")
	template.KeyUsage |= x509.KeyUsageCertSign
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	template.BasicConstraintsValid = true
	template.IsCA = true
	addLocalhost(template)

	certificate := sign(test, template, template, key, key)

	return write(test, certificate, key, poolOf(certificate))
}

// Authority is a test CA that issues server and client certificates.
type Authority struct {
	// CertFile is the CA certificate as PEM, for tls.client_ca_file.
	CertFile string
	// Pool trusts only this CA.
	Pool *x509.CertPool

	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

// NewAuthority creates a CA and writes its certificate to a temporary
// directory that is removed when the test ends.
func NewAuthority(test testing.TB) *Authority {
	test.Helper()

	key := newKey(test)
	template := newTemplate("beacon test CA")
	template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	template.BasicConstraintsValid = true
	template.IsCA = true

	certificate := sign(test, template, template, key, key)

	authority := &Authority{
		CertFile:    filepath.Join(test.TempDir(), "ca.pem"),
		Pool:        poolOf(certificate),
		certificate: certificate,
		key:         key,
	}
	writePEM(test, authority.CertFile, "CERTIFICATE", certificate.Raw)

	return authority
}

// IssueServer issues a server certificate valid for localhost, 127.0.0.1
// and ::1. Its Pool trusts the authority.
func (authority *Authority) IssueServer(test testing.TB) Certificate {
	test.Helper()

	template := newTemplate("beacon test server")
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	addLocalhost(template)

	return authority.issue(test, template)
}

// IssueClient issues a client certificate carrying dnsNames and uris as
// subject alternative names. Its Pool trusts the authority.
func (authority *Authority) IssueClient(test testing.TB, dnsNames []string, uris ...string) Certificate {
	test.Helper()

	template := newTemplate("beacon test client")
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	template.DNSNames = dnsNames

	for _, raw := range uris {
		parsed, err := url.Parse(raw)
		if err != nil {
			test.Fatalf("parse URI %q: %v", raw, err)
		}

		template.URIs = append(template.URIs, parsed)
	}

	return authority.issue(test, template)
}

func (authority *Authority) issue(test testing.TB, template *x509.Certificate) Certificate {
	test.Helper()

	key := newKey(test)
	certificate := sign(test, template, authority.certificate, key, authority.key)

	return write(test, certificate, key, authority.Pool)
}

func newKey(test testing.TB) *ecdsa.PrivateKey {
	test.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		test.Fatalf("generate key: %v", err)
	}

	return key
}

// newTemplate returns a template valid from a minute ago for an hour, with
// a random serial number so certificates from one CA never collide.
func newTemplate(commonName string) *x509.Certificate {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		serial = big.NewInt(time.Now().UnixNano())
	}

	now := time.Now()

	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
}

func addLocalhost(template *x509.Certificate) {
	template.DNSNames = []string{"localhost"}
	template.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
}

func sign(test testing.TB, template, parent *x509.Certificate, key, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	test.Helper()

	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		test.Fatalf("create certificate: %v", err)
	}

	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		test.Fatalf("parse certificate: %v", err)
	}

	return certificate
}

func poolOf(certificate *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(certificate)

	return pool
}

// write saves certificate and key as PEM files in a fresh temporary
// directory and returns them along with pool.
func write(test testing.TB, certificate *x509.Certificate, key *ecdsa.PrivateKey, pool *x509.CertPool) Certificate {
	test.Helper()

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		test.Fatalf("marshal key: %v", err)
	}

	directory := test.TempDir()
	written := Certificate{
		CertFile: filepath.Join(directory, "cert.pem"),
		KeyFile:  filepath.Join(directory, "key.pem"),
		Pool:     pool,
		KeyPair:  tls.Certificate{Certificate: [][]byte{certificate.Raw}, PrivateKey: key, Leaf: certificate},
	}

	writePEM(test, written.CertFile, "CERTIFICATE", certificate.Raw)
	writePEM(test, written.KeyFile, "PRIVATE KEY", keyDER)

	return written
}

func writePEM(test testing.TB, path, blockType string, contents []byte) {
	test.Helper()

	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: contents})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		test.Fatalf("write %s: %v", path, err)
	}
}
