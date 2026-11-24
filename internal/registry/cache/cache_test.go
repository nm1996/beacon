package cache

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// countingRegistry counts the List calls that reach the wrapped registry.
type countingRegistry struct {
	discovery.Registry
	lists atomic.Int32
}

// List counts the call and forwards it.
func (registry *countingRegistry) List(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	registry.lists.Add(1)
	return registry.Registry.List(ctx, query)
}

// fixture returns a cached memory registry holding ids under "orders", all
// with a heartbeat at now, and the counter below the cache.
func fixture(t *testing.T, ttl time.Duration, ids ...string) (*Registry, *countingRegistry) {
	t.Helper()

	backend := &countingRegistry{Registry: memory.New()}
	cached := New(backend, ttl).(*Registry)

	for _, id := range ids {
		create(t, cached, id)
	}

	return cached, backend
}

// create stores an orders instance with id through registry.
func create(t *testing.T, registry discovery.Registry, id string) {
	t.Helper()

	now := time.Now()
	instance := discovery.Instance{ID: id, Name: "orders", Address: "10.0.0.1", Port: 8081, RegisteredAt: now, LastHeartbeat: now}

	if err := registry.Create(t.Context(), instance, discovery.Limits{Total: math.MaxInt}); err != nil {
		t.Fatal(err)
	}
}

// ids lists the IDs of instances.
func ids(instances []discovery.Instance) string {
	result := make([]string, 0, len(instances))
	for _, instance := range instances {
		result = append(result, instance.ID)
	}

	return fmt.Sprint(result)
}

func TestLookupsWithinTheTTLAreServedFromTheCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cached, backend := fixture(t, time.Second, "a", "b")

		for range 10 {
			if _, err := cached.List(t.Context(), discovery.ListQuery{Name: "orders"}); err != nil {
				t.Fatal(err)
			}
		}

		if got := backend.lists.Load(); got != 1 {
			t.Errorf("expected one lookup to reach the backend, got %d", got)
		}

		time.Sleep(time.Second)

		if _, err := cached.List(t.Context(), discovery.ListQuery{Name: "orders"}); err != nil {
			t.Fatal(err)
		}

		if got := backend.lists.Load(); got != 2 {
			t.Errorf("expected the expired entry to be fetched again, got %d lookups", got)
		}
	})
}

func TestWritesThroughTheCacheClearIt(t *testing.T) {
	cached, _ := fixture(t, time.Hour, "a")
	ctx := t.Context()

	if _, err := cached.List(ctx, discovery.ListQuery{Name: "orders"}); err != nil {
		t.Fatal(err)
	}
	create(t, cached, "b")

	if got, _ := cached.List(ctx, discovery.ListQuery{Name: "orders"}); ids(got) != "[a b]" {
		t.Errorf("expected a create to show at once, got %s", ids(got))
	}

	if err := cached.Delete(ctx, "a", discovery.OwnerCheck{Any: true}); err != nil {
		t.Fatal(err)
	}

	if got, _ := cached.List(ctx, discovery.ListQuery{Name: "orders"}); ids(got) != "[b]" {
		t.Errorf("expected a delete to show at once, got %s", ids(got))
	}

	if _, err := cached.DeleteStale(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if got, _ := cached.List(ctx, discovery.ListQuery{Name: "orders"}); len(got) != 0 {
		t.Errorf("expected removing stale instances to show at once, got %s", ids(got))
	}
}

func TestHeartbeatsDoNotClearTheCache(t *testing.T) {
	cached, backend := fixture(t, time.Hour, "a")
	ctx := t.Context()

	if _, err := cached.List(ctx, discovery.ListQuery{Name: "orders"}); err != nil {
		t.Fatal(err)
	}

	if err := cached.Touch(ctx, "a", time.Now(), discovery.OwnerCheck{Any: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := cached.List(ctx, discovery.ListQuery{Name: "orders"}); err != nil {
		t.Fatal(err)
	}

	if got := backend.lists.Load(); got != 1 {
		t.Errorf("expected heartbeats to leave the cache in place, got %d lookups", got)
	}
}

func TestPagesFromTheCacheAreExact(t *testing.T) {
	cached, _ := fixture(t, time.Hour, "a", "b", "c", "d", "e")
	ctx := t.Context()

	for _, tt := range []struct {
		query discovery.ListQuery
		want  string
	}{
		{query: discovery.ListQuery{Name: "orders", Limit: 2}, want: "[a b]"},
		{query: discovery.ListQuery{Name: "orders", After: "b", Limit: 2}, want: "[c d]"},
		{query: discovery.ListQuery{Name: "orders", After: "d", Limit: 2}, want: "[e]"},
		{query: discovery.ListQuery{Name: "orders", After: "bb"}, want: "[c d e]"},
		{query: discovery.ListQuery{Name: "orders", HeartbeatSince: time.Now().Add(time.Hour)}, want: "[]"},
		{query: discovery.ListQuery{Name: "payments"}, want: "[]"},
		{query: discovery.ListQuery{}, want: "[a b c d e]"},
	} {
		got, err := cached.List(ctx, tt.query)
		if err != nil || ids(got) != tt.want {
			t.Errorf("%+v: expected %s, got %s, %v", tt.query, tt.want, ids(got), err)
		}
	}
}

func TestZeroTTLTurnsTheCacheOff(t *testing.T) {
	backend := memory.New()

	if New(backend, 0) != discovery.Registry(backend) {
		t.Error("expected the backend itself without a TTL")
	}
}

// slowRegistry's first List reads at once but returns only once release
// is closed, as a slow query whose result is out of date by then.
type slowRegistry struct {
	discovery.Registry
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

// List reads, and on the first call holds the result until release.
func (registry *slowRegistry) List(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	instances, err := registry.Registry.List(ctx, query)

	if registry.calls.Add(1) == 1 {
		close(registry.started)
		<-registry.release
	}

	return instances, err
}

func TestALookupInFlightDuringADeleteIsNotCached(t *testing.T) {
	backend := memory.New()
	create(t, backend, "a")

	slow := &slowRegistry{Registry: backend, started: make(chan struct{}), release: make(chan struct{})}
	cached := New(slow, time.Hour).(*Registry)
	ctx := t.Context()

	done := make(chan struct{})

	go func() {
		defer close(done)

		// Reads "a" before the delete below, returns after it.
		_, _ = cached.List(ctx, discovery.ListQuery{Name: "orders"})
	}()

	<-slow.started

	if err := cached.Delete(ctx, "a", discovery.OwnerCheck{Any: true}); err != nil {
		t.Fatal(err)
	}

	close(slow.release)
	<-done

	if got, _ := cached.List(ctx, discovery.ListQuery{Name: "orders"}); len(got) != 0 {
		t.Errorf("expected the lookup that raced the delete not to be cached, got %s", ids(got))
	}
}

func TestTheCacheHoldsABoundedNumberOfNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cached, _ := fixture(t, time.Second)

		for index := range maxEntries + 10 {
			if _, err := cached.List(t.Context(), discovery.ListQuery{Name: fmt.Sprintf("probe-%d", index)}); err != nil {
				t.Fatal(err)
			}
		}

		if got := len(cached.entries); got > maxEntries {
			t.Errorf("expected at most %d names cached, got %d", maxEntries, got)
		}
	})
}
