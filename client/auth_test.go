package client_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nm1996/beacon/client"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/bootstrap"
	"github.com/nm1996/beacon/internal/config"
)

const (
	serviceToken = "service-token-0123456789"
	readerToken  = "reader-token-0123456789"
)

// newAuthServer starts a real Beacon server with authentication on, which
// accepts serviceToken for everything and readerToken for discovery only,
// and returns its URL.
func newAuthServer(test *testing.T) string {
	test.Helper()

	content := ""
	for _, token := range []struct{ name, token, permissions string }{
		{name: "service", token: serviceToken},
		{name: "reader", token: readerToken, permissions: `permissions = ["discover"]`},
	} {
		hash, err := auth.HashToken(token.token)
		if err != nil {
			test.Fatalf("hash token: %v", err)
		}

		content += "[[tokens]]\nname = \"" + token.name + "\"\nsha256 = \"" + hash + "\"\n" + token.permissions + "\n"
	}

	path := filepath.Join(test.TempDir(), "tokens.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		test.Fatalf("write tokens file: %v", err)
	}

	settings := config.Default()
	settings.Auth = config.AuthConfig{Enabled: true, TokensFile: path}

	components, err := bootstrap.New(settings, slog.New(slog.DiscardHandler), "v9.9.9")
	if err != nil {
		test.Fatalf("build server: %v", err)
	}

	server := httptest.NewServer(components.Server.Handler)
	test.Cleanup(server.Close)

	return server.URL
}

func TestWithTokenAuthenticatesEveryCall(test *testing.T) {
	beacon := mustNew(test, newAuthServer(test), client.WithToken(serviceToken))

	registered, err := beacon.Register(test.Context(), orders)
	if err != nil {
		test.Fatalf("register: %v", err)
	}

	if err := beacon.Heartbeat(test.Context(), registered.ID); err != nil {
		test.Errorf("heartbeat: %v", err)
	}

	instances, err := beacon.Discover(test.Context(), "orders")
	if err != nil || len(instances) != 1 {
		test.Errorf("expected one instance, got %v, %v", instances, err)
	}

	if err := beacon.Unregister(test.Context(), registered.ID); err != nil {
		test.Errorf("unregister: %v", err)
	}
}

func TestMissingOrWrongTokenIsUnauthorized(test *testing.T) {
	baseURL := newAuthServer(test)

	for name, beacon := range map[string]*client.Client{
		"no token":    mustNew(test, baseURL),
		"wrong token": mustNew(test, baseURL, client.WithToken("wrong-token-0123456789")),
	} {
		_, err := beacon.Discover(test.Context(), "")

		if !errors.Is(err, client.ErrUnauthorized) {
			test.Errorf("%s: expected ErrUnauthorized, got %v", name, err)
		}

		var apiError *client.Error
		if !errors.As(err, &apiError) || apiError.Code != "unauthorized" {
			test.Errorf("%s: expected the unauthorized code, got %v", name, err)
		}
	}
}

func TestTokenWithoutPermissionIsForbidden(test *testing.T) {
	beacon := mustNew(test, newAuthServer(test), client.WithToken(readerToken))

	if _, err := beacon.Discover(test.Context(), ""); err != nil {
		test.Fatalf("expected the reader to discover, got %v", err)
	}

	_, err := beacon.Register(test.Context(), orders)

	if !errors.Is(err, client.ErrForbidden) || errors.Is(err, client.ErrUnauthorized) {
		test.Errorf("expected only ErrForbidden, got %v", err)
	}
}

func TestHealthNeedsNoToken(test *testing.T) {
	if _, err := mustNew(test, newAuthServer(test)).Health(test.Context()); err != nil {
		test.Errorf("expected /health without a token, got %v", err)
	}
}

func TestProxyAuthStatusesMatch(test *testing.T) {
	for status, want := range map[int]error{
		http.StatusUnauthorized: client.ErrUnauthorized,
		http.StatusForbidden:    client.ErrForbidden,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "<html>denied</html>", status)
		}))

		_, err := mustNew(test, server.URL).Discover(test.Context(), "")
		server.Close()

		if !errors.Is(err, want) {
			test.Errorf("status %d: expected %v, got %v", status, want, err)
		}
	}
}

func TestNoAuthorizationHeaderWithoutToken(test *testing.T) {
	sawHeader := "unset"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sawHeader = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte("[]"))
	}))
	defer server.Close()

	if _, err := mustNew(test, server.URL).Discover(test.Context(), ""); err != nil {
		test.Fatalf("discover: %v", err)
	}

	if sawHeader != "" {
		test.Errorf("expected no Authorization header, got %q", sawHeader)
	}
}
