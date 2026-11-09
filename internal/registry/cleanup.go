package registry

import (
	"context"
	"errors"
	"log"
	"time"
)

// RunCleanup removes stale instances every interval until ctx is cancelled.
// An instance is stale once it has gone longer than ttl without a heartbeat.
//
// It blocks, so callers run it in their own goroutine and stop it by
// cancelling ctx. It returns nil after cancellation, or an error straight
// away if interval or ttl is not positive.
func (registry *Registry) RunCleanup(ctx context.Context, interval, ttl time.Duration) error {
	if interval <= 0 {
		return errors.New("cleanup interval must be positive")
	}

	if ttl <= 0 {
		return errors.New("service TTL must be positive")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if removed := registry.Cleanup(ttl); removed > 0 {
				log.Printf("removed %d stale service instance(s)", removed)
			}
		}
	}
}
