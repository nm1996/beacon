package registry

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

// startCleanup runs RunCleanup in the background and returns a channel that
// receives its result once it stops.
func startCleanup(ctx context.Context, r *Registry, interval, ttl time.Duration) <-chan error {
	config := CleanupConfig{Interval: interval, TTL: ttl}

	done := make(chan error, 1)

	go func() {
		done <- r.RunCleanup(ctx, config, slog.New(slog.DiscardHandler))
	}()

	return done
}

func TestRunCleanupRemovesStaleServices(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		r := New()
		staleID := mustRegister(t, r, newService("stale-service"))
		activeID := mustRegister(t, r, newService("active-service"))

		startCleanup(ctx, r, time.Second, 3*time.Second)

		// Only the active service keeps sending heartbeats.
		time.Sleep(2 * time.Second)
		if err := r.Heartbeat(activeID); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}

		// At the 4s tick the stale service is 4s old and the active one 2s old.
		time.Sleep(2*time.Second + time.Millisecond)
		synctest.Wait()

		if err := r.Heartbeat(staleID); err == nil {
			t.Error("expected stale service to be removed")
		}

		if len(r.GetServices("active-service")) != 1 {
			t.Error("expected active service to remain")
		}
	})
}

func TestRunCleanupKeepsServicesWithinTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		r := New()
		mustRegister(t, r, newService("payment-service"))

		startCleanup(ctx, r, time.Second, 3*time.Second)

		// Exactly at the TTL the service is not yet stale.
		time.Sleep(3 * time.Second)
		synctest.Wait()

		if len(r.GetServices("")) != 1 {
			t.Error("expected service within TTL to remain")
		}
	})
}

func TestRunCleanupStopsWhenContextIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())

		done := startCleanup(ctx, New(), time.Second, time.Minute)

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

func TestRunCleanupRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		ttl      time.Duration
	}{
		{name: "zero interval", interval: 0, ttl: time.Minute},
		{name: "negative interval", interval: -time.Second, ttl: time.Minute},
		{name: "zero ttl", interval: time.Second, ttl: 0},
		{name: "negative ttl", interval: time.Second, ttl: -time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := New().RunCleanup(t.Context(), CleanupConfig{Interval: tt.interval, TTL: tt.ttl}, slog.New(slog.DiscardHandler))

			if err == nil {
				t.Error("expected an error for invalid settings")
			}
		})
	}
}
