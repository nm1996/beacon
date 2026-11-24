// Package postgrestest gives each test its own empty PostgreSQL schema, so
// tests in different packages can share one test database at once.
package postgrestest

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// URL returns a connection string for the database in
// BEACON_TEST_POSTGRES_URL whose search_path is a new, empty schema, which
// it drops when the test ends. It skips the test when the variable is not
// set.
func URL(test testing.TB) string {
	test.Helper()

	base := os.Getenv("BEACON_TEST_POSTGRES_URL")
	if base == "" {
		test.Skip("BEACON_TEST_POSTGRES_URL is not set")
	}

	schema := "beacon_test_" + strings.ToLower(rand.Text())

	conn, err := pgx.Connect(test.Context(), base)
	if err != nil {
		test.Fatalf("connect to the test database: %v", err)
	}
	defer conn.Close(test.Context())

	if _, err := conn.Exec(test.Context(), "CREATE SCHEMA "+schema); err != nil {
		test.Fatalf("create schema: %v", err)
	}

	test.Cleanup(func() {
		// The test's context has ended by now.
		ctx := context.Background()

		cleanup, err := pgx.Connect(ctx, base)
		if err != nil {
			test.Errorf("connect to drop schema %s: %v", schema, err)
			return
		}
		defer cleanup.Close(ctx)

		if _, err := cleanup.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			test.Errorf("drop schema %s: %v", schema, err)
		}
	})

	parsed, err := url.Parse(base)
	if err != nil {
		test.Fatalf("parse BEACON_TEST_POSTGRES_URL: %v", err)
	}

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	return parsed.String()
}
