package client_test

import (
	"errors"
	"log/slog"
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
	ordersToken   = "orders-token-0123456789"
	paymentsToken = "payments-token-0123456789"
	operatorToken = "operator-token-0123456789"
)

// newOwnershipServer starts a real Beacon with authentication on and three
// callers: orders and payments, which may register, and operator, which may
// also manage other callers' instances. It returns the base URL.
func newOwnershipServer(test *testing.T) string {
	test.Helper()

	content := ""
	for _, entry := range []struct{ name, token, permissions string }{
		{name: "orders", token: ordersToken},
		{name: "payments", token: paymentsToken},
		{name: "operator", token: operatorToken, permissions: `permissions = ["register", "manage"]`},
	} {
		hash, err := auth.HashToken(entry.token)
		if err != nil {
			test.Fatalf("hash token: %v", err)
		}

		content += "[[tokens]]\nname = \"" + entry.name + "\"\nsha256 = \"" + hash + "\"\n" + entry.permissions + "\n"
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

func TestOtherServicesCannotTouchAnInstance(test *testing.T) {
	baseURL := newOwnershipServer(test)

	owner := mustNew(test, baseURL, client.WithToken(ordersToken))
	other := mustNew(test, baseURL, client.WithToken(paymentsToken))

	registered, err := owner.Register(test.Context(), orders)
	if err != nil {
		test.Fatalf("register: %v", err)
	}

	for name, err := range map[string]error{
		"heartbeat":  other.Heartbeat(test.Context(), registered.ID),
		"unregister": other.Unregister(test.Context(), registered.ID),
	} {
		if !errors.Is(err, client.ErrNotOwned) || !errors.Is(err, client.ErrForbidden) {
			test.Errorf("%s: expected ErrNotOwned, which is also ErrForbidden, got %v", name, err)
		}
	}

	// The other service can still see the instance, as discovery is for.
	instances, err := other.Discover(test.Context(), "orders")
	if err != nil || len(instances) != 1 {
		test.Errorf("expected the other service to discover the instance, got %v, %v", instances, err)
	}

	if err := owner.Heartbeat(test.Context(), registered.ID); err != nil {
		test.Errorf("expected the owner's heartbeat to work, got %v", err)
	}

	if err := owner.Unregister(test.Context(), registered.ID); err != nil {
		test.Errorf("expected the owner to unregister, got %v", err)
	}
}

func TestOperatorWithManageCanRemoveAnyInstance(test *testing.T) {
	baseURL := newOwnershipServer(test)

	owner := mustNew(test, baseURL, client.WithToken(ordersToken))
	operator := mustNew(test, baseURL, client.WithToken(operatorToken))

	lease, err := owner.Announce(test.Context(), orders)
	if err != nil {
		test.Fatalf("announce: %v", err)
	}

	if err := operator.Unregister(test.Context(), lease.ID()); err != nil {
		test.Fatalf("expected the operator to remove the instance, got %v", err)
	}

	// The lease's Close then finds nothing to unregister, which it accepts.
	if err := lease.Close(test.Context()); err != nil {
		test.Errorf("close lease after the operator removed its instance: %v", err)
	}
}

func TestPlainForbiddenIsNotNotOwned(test *testing.T) {
	beacon := mustNew(test, newAuthServer(test), client.WithToken(readerToken))

	_, err := beacon.Register(test.Context(), orders)

	if !errors.Is(err, client.ErrForbidden) || errors.Is(err, client.ErrNotOwned) {
		test.Errorf("expected a missing permission to be ErrForbidden only, got %v", err)
	}
}
