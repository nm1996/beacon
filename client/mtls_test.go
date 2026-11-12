package client_test

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
	"github.com/nm1996/beacon/internal/tlstest"
)

// newMTLSServer starts a real Beacon over TLS that accepts client
// certificates from authority. Its tokens file lets orders.internal do
// everything, spiffe://example.org/reader only discover, and serviceToken
// do everything. It returns the base URL.
func newMTLSServer(test *testing.T, authority *tlstest.Authority) string {
	test.Helper()

	hash, err := auth.HashToken(serviceToken)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	identities := "[[tokens]]\nname = \"service\"\nsha256 = \"" + hash + "\"\n\n" +
		"[[certificates]]\nname = \"orders\"\ndns = \"orders.internal\"\n\n" +
		"[[certificates]]\nname = \"reader\"\nuri = \"spiffe://example.org/reader\"\npermissions = [\"discover\"]\n"

	path := filepath.Join(test.TempDir(), "tokens.toml")
	if err := os.WriteFile(path, []byte(identities), 0o600); err != nil {
		test.Fatalf("write tokens file: %v", err)
	}

	server := authority.IssueServer(test)

	settings := config.Default()
	settings.TLS = config.TLSConfig{CertFile: server.CertFile, KeyFile: server.KeyFile, ClientCAFile: authority.CertFile}
	settings.Auth = config.AuthConfig{Enabled: true, TokensFile: path}

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		test.Fatalf("build server: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatalf("listen: %v", err)
	}

	served := make(chan error, 1)
	go func() {
		served <- components.Server.ServeTLS(listener, "", "")
	}()

	test.Cleanup(func() {
		if err := components.Server.Close(); err != nil {
			test.Errorf("close server: %v", err)
		}

		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			test.Errorf("expected ErrServerClosed, got %v", err)
		}
	})

	return "https://" + listener.Addr().String()
}

// tlsClient returns a Beacon client that trusts authority and presents
// certificates, if any.
func tlsClient(test *testing.T, baseURL string, authority *tlstest.Authority, certificates []tls.Certificate, options ...client.Option) *client.Client {
	test.Helper()

	transport := &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      authority.Pool,
		Certificates: certificates,
		MinVersion:   tls.VersionTLS12,
	}}
	test.Cleanup(transport.CloseIdleConnections)

	options = append(options, client.WithHTTPClient(&http.Client{Timeout: 5 * time.Second, Transport: transport}))

	return mustNew(test, baseURL, options...)
}

func TestClientCertificateAuthenticates(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	baseURL := newMTLSServer(test, authority)

	orders := authority.IssueClient(test, []string{"orders.internal"})
	beacon := tlsClient(test, baseURL, authority, []tls.Certificate{orders.KeyPair})

	lease, err := beacon.Announce(test.Context(), client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
	if err != nil {
		test.Fatalf("announce with a client certificate: %v", err)
	}

	instances, err := beacon.Discover(test.Context(), "orders")
	if err != nil || len(instances) != 1 {
		test.Errorf("expected one instance, got %v, %v", instances, err)
	}

	if err := lease.Close(test.Context()); err != nil {
		test.Errorf("close lease: %v", err)
	}
}

func TestClientCertificatePermissionsApply(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	baseURL := newMTLSServer(test, authority)

	reader := authority.IssueClient(test, nil, "spiffe://example.org/reader")
	beacon := tlsClient(test, baseURL, authority, []tls.Certificate{reader.KeyPair})

	if _, err := beacon.Discover(test.Context(), ""); err != nil {
		test.Fatalf("expected the reader certificate to discover, got %v", err)
	}

	if _, err := beacon.Register(test.Context(), orders); !errors.Is(err, client.ErrForbidden) {
		test.Errorf("expected ErrForbidden for a discover-only certificate, got %v", err)
	}
}

func TestWithoutClientCertificate(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	baseURL := newMTLSServer(test, authority)

	anonymous := tlsClient(test, baseURL, authority, nil)

	if _, err := anonymous.Health(test.Context()); err != nil {
		test.Errorf("expected /health without a certificate, got %v", err)
	}

	if _, err := anonymous.Discover(test.Context(), ""); !errors.Is(err, client.ErrUnauthorized) {
		test.Errorf("expected ErrUnauthorized without credentials, got %v", err)
	}

	withToken := tlsClient(test, baseURL, authority, nil, client.WithToken(serviceToken))
	if _, err := withToken.Discover(test.Context(), ""); err != nil {
		test.Errorf("expected a bearer token to keep working alongside mTLS, got %v", err)
	}
}

func TestUnlistedClientCertificateIsUnauthorized(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	baseURL := newMTLSServer(test, authority)

	billing := authority.IssueClient(test, []string{"billing.internal"})
	beacon := tlsClient(test, baseURL, authority, []tls.Certificate{billing.KeyPair})

	if _, err := beacon.Discover(test.Context(), ""); !errors.Is(err, client.ErrUnauthorized) {
		test.Errorf("expected ErrUnauthorized for a trusted but unlisted certificate, got %v", err)
	}
}

func TestClientCertificateFromAnotherCAIsRejected(test *testing.T) {
	authority := tlstest.NewAuthority(test)
	baseURL := newMTLSServer(test, authority)

	impostor := tlstest.NewAuthority(test).IssueClient(test, []string{"orders.internal"})
	beacon := tlsClient(test, baseURL, authority, []tls.Certificate{impostor.KeyPair})

	_, err := beacon.Discover(test.Context(), "")

	// The handshake fails, so no HTTP response, and no *client.Error, exists.
	var apiError *client.Error
	if err == nil || errors.As(err, &apiError) {
		test.Errorf("expected the TLS handshake to fail, got %v", err)
	}
}
