// Package tlstest writes short-lived, self-signed certificates for tests that
// need TLS files on disk.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Certificate is a self-signed server certificate written to a test's
// temporary directory.
type Certificate struct {
	// CertFile is the PEM certificate.
	CertFile string
	// KeyFile is the PEM private key.
	KeyFile string
	// Pool trusts only this certificate, for clients that should verify it.
	Pool *x509.CertPool
}

// WriteCertificate creates a self-signed certificate valid for localhost,
// 127.0.0.1 and ::1, and writes it and its key as PEM files in a temporary
// directory that is removed when the test ends.
func WriteCertificate(test testing.TB) Certificate {
	test.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		test.Fatalf("generate key: %v", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "beacon test"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		test.Fatalf("create certificate: %v", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		test.Fatalf("marshal key: %v", err)
	}

	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		test.Fatalf("parse certificate: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(certificate)

	directory := test.TempDir()
	written := Certificate{
		CertFile: filepath.Join(directory, "cert.pem"),
		KeyFile:  filepath.Join(directory, "key.pem"),
		Pool:     pool,
	}

	writePEM(test, written.CertFile, "CERTIFICATE", der)
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
