package discovery_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// startCleanup runs service.RunCleanup in the background and returns a
// channel that receives its result once it stops.
func startCleanup(ctx context.Context, service *discovery.Service) <-chan error {
	done := make(chan error, 1)

	go func() {
		done <- service.RunCleanup(ctx)
	}()

	return done
}

// newCleanupService returns a Service with a 3s TTL swept every second.
func newCleanupService(t *testing.T, registry discovery.Registry) *discovery.Service {
	t.Helper()

	service, err := discovery.New(registry, discovery.Config{TTL: 3 * time.Second, CleanupInterval: time.Second}, discard)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return service
}

func TestRunCleanupRemovesStaleInstances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		service := newCleanupService(t, memory.New())
		stale := mustRegister(t, service, "stale-service")
		active := mustRegister(t, service, "active-service")

		startCleanup(ctx, service)

		// Only the active instance keeps sending heartbeats.
		time.Sleep(2 * time.Second)
		if err := service.Heartbeat(ctx, active.ID); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}

		// At the 4s tick the stale instance is 4s old and the active one 2s old.
		time.Sleep(2*time.Second + time.Millisecond)
		synctest.Wait()

		if err := service.Heartbeat(ctx, stale.ID); !errors.Is(err, discovery.ErrInstanceNotFound) {
			t.Errorf("expected the stale instance to be removed, heartbeat gave %v", err)
		}

		if got := len(mustDiscover(t, service, "active-service")); got != 1 {
			t.Errorf("expected the active instance to remain, got %d", got)
		}
	})
}

func TestRunCleanupKeepsInstancesWithinTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		service := newCleanupService(t, memory.New())
		mustRegister(t, service, "payment-service")

		startCleanup(ctx, service)

		// Exactly at the TTL the instance is not yet stale.
		time.Sleep(3 * time.Second)
		synctest.Wait()

		if got := len(mustDiscover(t, service, "")); got != 1 {
			t.Errorf("expected the instance within TTL to remain, got %d", got)
		}
	})
}

func TestRunCleanupStopsWhenContextIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())

		done := startCleanup(ctx, newCleanupService(t, memory.New()))

		time.Sleep(5 * time.Second)
		cancel()
		synctest.Wait()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("expected nil error after cancellation, got %v", err)
			}
		default:
			t.Fatal("expected cleanup to stop after cancellation")
		}
	})
}

// flakyRegistry fails DeleteStale a given number of times, then behaves
// like the registry it wraps.
type flakyRegistry struct {
	discovery.Registry
	failures atomic.Int32
}

// DeleteStale fails while failures remain.
func (registry *flakyRegistry) DeleteStale(ctx context.Context, cutoff time.Time) (int, error) {
	if registry.failures.Add(-1) >= 0 {
		return 0, errors.New("backend unavailable")
	}

	return registry.Registry.DeleteStale(ctx, cutoff)
}

func TestRunCleanupKeepsRunningAfterAFailedSweep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		registry := &flakyRegistry{Registry: memory.New()}
		registry.failures.Store(4)

		service := newCleanupService(t, registry)
		mustRegister(t, service, "stale-service")

		startCleanup(ctx, service)

		// Sweeps 1 to 4 fail; the instance turns stale after 3s, so the
		// first working sweep at 5s must remove it.
		time.Sleep(5*time.Second + time.Millisecond)
		synctest.Wait()

		if got := len(mustDiscover(t, service, "")); got != 0 {
			t.Errorf("expected cleanup to recover and remove the stale instance, %d remain", got)
		}
	})
}
