package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/postgres/postgrestest"
	"github.com/nm1996/beacon/internal/registry/registrytest"
)

// openEmpty opens a registry in a schema of its own.
func openEmpty(t *testing.T) *Registry {
	t.Helper()

	registry, err := Open(t.Context(), postgrestest.URL(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(registry.Close)

	return registry
}

func TestRegistryContract(t *testing.T) {
	if os.Getenv("BEACON_TEST_POSTGRES_URL") == "" {
		t.Skip("BEACON_TEST_POSTGRES_URL is not set")
	}

	registrytest.Run(t, func(t *testing.T) discovery.Registry {
		return openEmpty(t)
	})
}

func TestOpenMigratesOnceWhenNodesStartTogether(t *testing.T) {
	url := postgrestest.URL(t)

	errs := make(chan error, 4)

	for range 4 {
		go func() {
			registry, err := Open(context.Background(), url)
			if err == nil {
				registry.Close()
			}

			errs <- err
		}()
	}

	for range 4 {
		if err := <-errs; err != nil {
			t.Errorf("open: %v", err)
		}
	}

	registry, err := Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	var versions int
	if err := registry.pool.QueryRow(t.Context(), "SELECT count(*) FROM beacon_schema_migrations").Scan(&versions); err != nil {
		t.Fatal(err)
	}

	if versions != 1 {
		t.Errorf("expected the one migration recorded once, got %d rows", versions)
	}
}

func TestOpenFailsForAnUnreachableDatabase(t *testing.T) {
	if _, err := Open(t.Context(), "postgres://beacon@127.0.0.1:1/beacon?connect_timeout=1"); err == nil {
		t.Fatal("expected an error")
	}
}
