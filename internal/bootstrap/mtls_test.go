package bootstrap

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/tlstest"
)

// mtlsSettings returns settings with TLS served by a certificate from
// authority, client certificates from authority accepted, and auth enabled
// with a tokens file holding identities.
func mtlsSettings(test *testing.T, authority *tlstest.Authority, identities string) config.Config {
	test.Helper()

	server := authority.IssueServer(test)

	path := filepath.Join(test.TempDir(), "tokens.toml")
	if err := os.WriteFile(path, []byte(identities), 0o600); err != nil {
		test.Fatalf("write tokens file: %v", err)
	}

	settings := config.Default()
	settings.TLS = config.TLSConfig{CertFile: server.CertFile, KeyFile: server.KeyFile, ClientCAFile: authority.CertFile}
	settings.Auth = config.AuthConfig{Enabled: true, TokensFile: path}

	return settings
}

func TestNewAsksForClientCertificatesWithoutRequiringThem(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	settings := mtlsSettings(test, authority, "[[certificates]]\nname = \"orders\"\ndns = \"orders.internal\"\n")

	tlsConfig := mustNew(test, settings, "v0.0.0-test").Server.TLSConfig

	// Requiring a certificate would make /health and bearer tokens
	// unreachable for clients without one.
	if tlsConfig.ClientAuth != tls.VerifyClientCertIfGiven {
		test.Errorf("expected VerifyClientCertIfGiven, got %v", tlsConfig.ClientAuth)
	}

	if tlsConfig.ClientCAs == nil {
		test.Error("expected the client CA pool to be set")
	}
}

func TestNewLeavesClientCertificatesOffWithoutClientCA(test *testing.T) {
	certificate := tlstest.WriteCertificate(test)

	settings := config.Default()
	settings.TLS = config.TLSConfig{CertFile: certificate.CertFile, KeyFile: certificate.KeyFile}

	tlsConfig := mustNew(test, settings, "v0.0.0-test").Server.TLSConfig

	if tlsConfig.ClientAuth != tls.NoClientCert || tlsConfig.ClientCAs != nil {
		test.Errorf("expected no client certificates to be requested, got %v", tlsConfig.ClientAuth)
	}
}

func TestNewRejectsUnusableClientCA(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	identities := "[[certificates]]\nname = \"orders\"\ndns = \"orders.internal\"\n"

	notPEM := filepath.Join(test.TempDir(), "not-pem.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		test.Fatalf("write file: %v", err)
	}

	cases := []struct {
		name    string
		caFile  string
		wantErr string
	}{
		{name: "missing file", caFile: filepath.Join(test.TempDir(), "missing.pem"), wantErr: "load TLS client CA file"},
		{name: "no PEM certificates", caFile: notPEM, wantErr: "no PEM certificates found"},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			settings := mtlsSettings(test, authority, identities)
			settings.TLS.ClientCAFile = testCase.caFile

			_, err := New(settings, discard, "v0.0.0-test")

			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				test.Errorf("expected an error containing %q, got %v", testCase.wantErr, err)
			}
		})
	}
}

func TestNewRejectsClientCAWithoutCertificateEntries(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	settings := authSettings(test)
	withCA := mtlsSettings(test, authority, "")
	settings.TLS = withCA.TLS

	_, err := New(settings, discard, "v0.0.0-test")

	if err == nil || !strings.Contains(err.Error(), "has no [[certificates]] entries") {
		test.Errorf("expected a missing certificates error, got %v", err)
	}
}

func TestNewWarnsAboutCertificateEntriesWithoutClientCA(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	settings := mtlsSettings(test, authority, "[[certificates]]\nname = \"orders\"\ndns = \"orders.internal\"\n")
	settings.TLS.ClientCAFile = ""

	var logs bytes.Buffer

	if _, err := New(settings, slog.New(slog.NewTextHandler(&logs, nil)), "v0.0.0-test"); err != nil {
		test.Fatalf("new: %v", err)
	}

	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "tls.client_ca_file is not set") {
		test.Errorf("expected a warning about unused certificate entries, logs:\n%s", logs.String())
	}
}
