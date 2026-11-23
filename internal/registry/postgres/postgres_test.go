package postgres

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/nm1996/beacon/internal/cluster"
	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/heartbeats"
	"github.com/nm1996/beacon/internal/registry/postgres/postgrestest"
	"github.com/nm1996/beacon/internal/registry/registrytest"
)

// openEmpty opens a registry in a schema of its own.
func openEmpty(t *testing.T) *PostgresRegistry {
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

	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}

	if versions != len(files) {
		t.Errorf("expected each of the %d migrations recorded once, got %d rows", len(files), versions)
	}
}

func TestOpenFailsForAnUnreachableDatabase(t *testing.T) {
	if _, err := Open(t.Context(), "postgres://beacon@127.0.0.1:1/beacon?connect_timeout=1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestNodesAreRecordedAndForgotten(t *testing.T) {
	registry := openEmpty(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)

	for _, node := range []cluster.Node{
		{ID: "b", Address: "10.0.0.2:8080", Version: "v0.3.0", StartedAt: now, LastSeen: now},
		{ID: "a", Address: "10.0.0.1:8080", Version: "v0.3.0", StartedAt: now, LastSeen: now},
		{ID: "old", Address: "10.0.0.3:8080", Version: "v0.2.0", StartedAt: now, LastSeen: now.Add(-time.Minute)},
	} {
		if err := registry.Beat(ctx, node); err != nil {
			t.Fatal(err)
		}
	}

	// Recording again updates rather than duplicates.
	if err := registry.Beat(ctx, cluster.Node{ID: "a", Address: "10.0.0.9:8080", Version: "v0.3.1", StartedAt: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}

	live, err := registry.Live(ctx, now.Add(-cluster.LiveFor))
	if err != nil {
		t.Fatal(err)
	}

	if len(live) != 2 || live[0].ID != "a" || live[0].Address != "10.0.0.9:8080" || live[1].ID != "b" || !live[1].LastSeen.Equal(now) {
		t.Errorf("expected a, updated, and b, got %+v", live)
	}

	if err := registry.Leave(ctx, "a"); err != nil {
		t.Fatal(err)
	}

	if live, _ := registry.Live(ctx, now.Add(-cluster.LiveFor)); len(live) != 1 || live[0].ID != "b" {
		t.Errorf("expected only b after a left, got %+v", live)
	}
}

func TestTouchManyWritesHeartbeatsInOneStatement(t *testing.T) {
	registry := openEmpty(t)
	ctx := t.Context()
	start := time.Now().UTC().Truncate(time.Microsecond)

	for _, id := range []string{"a", "b"} {
		instance := discovery.Instance{ID: id, Name: "orders", Address: "10.0.0.1", Port: 8081, RegisteredAt: start, LastHeartbeat: start}
		if err := registry.Create(ctx, instance, discovery.Limits{Total: 10}); err != nil {
			t.Fatal(err)
		}
	}

	// b already holds a later heartbeat than the batch brings.
	if err := registry.Touch(ctx, "b", start.Add(time.Hour), discovery.OwnerCheck{Any: true}); err != nil {
		t.Fatal(err)
	}

	anyOwner := discovery.OwnerCheck{Any: true}

	missing, err := registry.TouchMany(ctx, []heartbeats.Heartbeat{
		{ID: "a", At: start.Add(time.Minute), Check: anyOwner},
		{ID: "b", At: start.Add(time.Minute), Check: anyOwner},
		{ID: "gone", At: start.Add(time.Minute), Check: anyOwner},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(missing) != 1 || missing[0] != "gone" {
		t.Errorf("expected only gone reported missing, got %v", missing)
	}

	for id, want := range map[string]time.Time{"a": start.Add(time.Minute), "b": start.Add(time.Hour)} {
		instance, err := registry.Get(ctx, id)
		if err != nil || !instance.LastHeartbeat.Equal(want) {
			t.Errorf("%s: expected last heartbeat %s, got %s, %v", id, want, instance.LastHeartbeat, err)
		}
	}
}

func TestTouchManyChecksTheOwner(t *testing.T) {
	registry := openEmpty(t)
	ctx := t.Context()
	start := time.Now().UTC().Truncate(time.Microsecond)

	instance := discovery.Instance{ID: "pod-1", Name: "orders", Address: "10.0.0.1", Port: 8081, Owner: "bob", RegisteredAt: start, LastHeartbeat: start}
	if err := registry.Create(ctx, instance, discovery.Limits{Total: 10}); err != nil {
		t.Fatal(err)
	}

	unchanged, err := registry.TouchMany(ctx, []heartbeats.Heartbeat{
		{ID: "pod-1", At: start.Add(time.Minute), Check: discovery.OwnerCheck{Owner: "alice"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(unchanged) != 1 {
		t.Errorf("expected alice's heartbeat for bob's instance to be refused, got %v", unchanged)
	}

	if stored, _ := registry.Get(ctx, "pod-1"); !stored.LastHeartbeat.Equal(start) {
		t.Errorf("expected bob's instance untouched, got %s", stored.LastHeartbeat)
	}
}
