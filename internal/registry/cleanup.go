package registry

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// CleanupConfig controls how stale instances are removed.
type CleanupConfig struct {
	// Interval is how often the registry looks for stale instances.
	Interval time.Duration
	// TTL is how long an instance may go without a heartbeat before it is
	// considered stale.
	TTL time.Duration
}

// validate reports the first setting that is not positive.
func (config CleanupConfig) validate() error {
	if config.Interval <= 0 {
		return errors.New("cleanup interval must be positive")
	}

	if config.TTL <= 0 {
		return errors.New("service TTL must be positive")
	}

	return nil
}

// RunCleanup removes stale instances every config.Interval until ctx is
// cancelled, and logs how many it removed each time it removes any.
//
// It blocks, so callers run it in their own goroutine and stop it by
// cancelling ctx. It returns nil after cancellation, or an error straight
// away if config is invalid.
func (registry *Registry) RunCleanup(ctx context.Context, config CleanupConfig, logger *slog.Logger) error {
	if err := config.validate(); err != nil {
		return err
	}

	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if removed := registry.Cleanup(config.TTL); removed > 0 {
				logger.Info("removed stale service instances", slog.Int("count", removed))
			}
		}
	}
}
